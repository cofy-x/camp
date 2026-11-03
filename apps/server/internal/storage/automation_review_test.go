package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestAutomationPolicyRejectsUnexecutableParticipantSetsBeforeAssessment(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	u, _ := UserFromContext(actor)
	base := orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Accept requested work.", AllowedActorIDs: []string{u.ID}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2}
	duplicate := base
	duplicate.ParticipantAgentIDs = []string{writer.ID, writer.ID}
	if _, err := s.SaveAutomationPolicy(actor, room.ID, "room", duplicate); err == nil {
		t.Fatal("duplicate participants would permanently prevent task admission")
	}
	tooMany := base
	for index := range 8 {
		a, err := s.CreateRoomAgent(actor, room.ID, Agent{Name: fmt.Sprintf("Participant %d", index), Handle: fmt.Sprintf("participant-%d", index), Enabled: true, Access: "read", PreferredRunnerID: room.RunnerID})
		if err != nil {
			t.Fatal(err)
		}
		tooMany.ParticipantAgentIDs = append(tooMany.ParticipantAgentIDs, a.ID)
	}
	if _, err := s.SaveAutomationPolicy(actor, room.ID, "room", tooMany); err == nil {
		t.Fatal("policy admitted more participants than a task can execute")
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_policies", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 0)
}

func TestAutomationPolicyReplayPrecedesNewValidation(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	accepted := testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	// An older version could accept duplicates. Its immutable receipt remains
	// authoritative even though a fresh request must now reject that input.
	input := orchestration.PolicyInput{RequestID: "historical-policy", Mode: "auto", Rules: "Original policy.", ParticipantAgentIDs: []string{writer.ID, writer.ID}, MaxTurns: 6, MaxModelRequests: 24}
	body, _ := json.Marshal([]any{"policy", room.ID, "room", input})
	value, _ := json.Marshal(AutomationReceipt{Kind: "policy", Policy: &accepted})
	if err := s.queries().SaveAutomationRequest(ctx, sqlgen.SaveAutomationRequestParams{UserID: actorID(actor), RequestID: input.RequestID, RequestHash: remoteHash(body), Result: string(value)}); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.SaveAutomationPolicy(actor, room.ID, "room", input)
	if err != nil || replayed.ID != accepted.ID || replayed.Revision != accepted.Revision {
		t.Fatal("new validation invalidated an accepted policy receipt", replayed, err)
	}
	input.RequestID = "fresh-policy"
	if _, err = s.SaveAutomationPolicy(actor, room.ID, "room", input); err == nil {
		t.Fatal("fresh request bypassed participant validation")
	}
}

func TestAutomationFanOutCopiesCannotLeakPendingInputThroughSharedThreadHistory(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, actor, room)
	receiveTestGitHub(t, s, source, testGitHubPayload("Original", "Initial source context."))
	first := startTask(t, s, actor, room, writer)
	second, err := s.CreateTask(actor, room.ID, CreateTaskInput{RequestID: newID(), Goal: "Another goal in the same shared Thread.", ThreadRootID: first.ThreadRootID, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []CollaborationTask{first, second} {
		subscribeTaskTest(t, s, actor, task, source.ID, "123:3")
	}
	pendingText := "PENDING-INPUT-MUST-NOT-LEAK"
	body := strings.Repeat("<", 10000) + pendingText + strings.Repeat("<", 10000)
	receiveTestGitHub(t, s, source, testGitHubPayload("Large input", body))
	first, _ = s.GetTaskState(ctx, first.ID)
	enableTaskInputs(t, s, first.Participants[0].RunnerID)
	run, err := s.GetRun(ctx, first.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err = s.ClaimRemoteRun(ctx, run.ID, run.RunnerID, time.Minute)
	if err != nil || run.TaskInputRevision != 1 || first.InputRevision < 2 {
		t.Fatal("fixture did not freeze a partial escaped input batch", run, first.InputRevision, err)
	}
	for _, test := range []struct {
		task    CollaborationTask
		through int64
	}{{first, 1}, {second, 0}} {
		messages, err := s.TaskContextMessages(ctx, room.ID, first.ThreadRootID, 0, run.ContextThroughSeq, test.task.ID, test.through)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range messages {
			if strings.Contains(message.Content, pendingText) || (test.through == 0 && strings.Contains(message.Content, strings.Repeat("<", 32))) {
				t.Fatal("another task's duplicate projection bypassed the frozen input interval", test.task.ID, message.ID)
			}
		}
	}
}

func TestAutomationTaskScopeDistinguishesSourceReplacement(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	oldSource := testGitHubSource(t, s, actor, room)
	original := receiveTestGitHub(t, s, oldSource, testGitHubPayload("Old source", "Original work."))
	policy, err := s.SaveAutomationPolicy(actor, room.ID, oldSource.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Accept new requested work.", AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2})
	if err != nil {
		t.Fatal(err)
	}
	candidate := orchestration.Candidate{ID: newID(), Policy: policy, Events: []orchestration.SourceEvent{{ID: "github:" + original.ID, ExternalEventID: original.ID, RoomID: room.ID, SourceID: oldSource.ID, ThreadKey: "123:3", Content: original.Context(), ActorID: "7", ActorKind: "user", OccurredAt: now()}}, Budget: orchestration.ExecutionBudget{Model: roomBudget.Model, RuntimeKey: roomBudget.RuntimeKey, MaxTokens: roomBudget.MaxTokens, MaxSteps: roomBudget.MaxSteps, TimeoutSeconds: roomBudget.TimeoutSeconds}}
	if err = s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := createAutomationTask(ctx, tx, candidate, orchestration.Assessment{Outcome: "accept", Goal: "Handle the original request."})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateGitHubSource(actor, oldSource.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: oldSource.Revision, Enabled: new(false)}, ""); err != nil {
		t.Fatal(err)
	}
	newSource := testGitHubSource(t, s, actor, room)
	if _, err = s.SaveAutomationPolicy(actor, room.ID, newSource.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "observe", Rules: "Accept new requested work.", AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2}); err != nil {
		t.Fatal(err)
	}
	receiveTestGitHub(t, s, newSource, testGitHubPayload("New source", "A separate request under new source identity."))
	claimed := claimAutomation(t, s)
	if claimed.Policy.SourceID != newSource.ID || len(claimed.Tasks) != 0 {
		t.Fatal("old source work was treated as receiving new-source context", claimed)
	}
}

func TestAutomationTaskRoutingFactsFreezeOnlyCurrentBatchReceipts(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, actor, room)
	original := receiveTestGitHub(t, s, source, testGitHubPayload("Initial issue", "Original manually accepted goal."))
	task, err := s.CreateTask(actor, room.ID, CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), SourceEventID: original.ID, Goal: "Handle the initial issue.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveAutomationPolicy(actor, room.ID, source.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "observe", Rules: "Accept new requested work; existing delivered context is separate.", AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2}); err != nil {
		t.Fatal(err)
	}
	receiveTestGitHub(t, s, source, testGitHubPayload("New context", "Not subscribed yet."))
	unsubscribed := claimAutomation(t, s)
	if len(unsubscribed.Tasks) != 1 || unsubscribed.Tasks[0].ID != task.ID || unsubscribed.Tasks[0].ReceivesInputs || len(unsubscribed.Tasks[0].ReceivedEventIDs) != 0 {
		t.Fatal("manual source relation implied delivery or was omitted", unsubscribed.Tasks)
	}
	subscribeTaskTest(t, s, actor, task, source.ID, "123:3")
	if err = s.CompleteAutomationEvaluation(ctx, unsubscribed.ID, orchestration.Assessment{Outcome: "accept", Goal: "A second goal.", Reason: "A new request."}); err != nil {
		t.Fatal(err)
	}
	row, _ := s.queries().GetAutomationEvaluation(ctx, unsubscribed.ID)
	if row.Status != "needs_attention" {
		t.Fatal("routing change did not fence the assessment", row.Status)
	}
	followup := receiveTestGitHub(t, s, source, testGitHubPayload("Subscribed context", "This event was delivered."))
	claimed := claimAutomation(t, s)
	if len(claimed.Tasks) != 1 || !claimed.Tasks[0].ReceivesInputs || len(claimed.Tasks[0].ReceivedEventIDs) != 1 || claimed.Tasks[0].ReceivedEventIDs[0] != "github:"+followup.ID {
		t.Fatal("missing actual frozen delivery evidence", claimed.Tasks)
	}
	// Later intake changes the task input revision, but cannot alter the facts
	// about the batch already sent to this assessment.
	receiveTestGitHub(t, s, source, testGitHubPayload("Later context", "This remains for the next assessment."))
	if err = s.CompleteAutomationEvaluation(ctx, claimed.ID, orchestration.Assessment{Outcome: "ignore", Reason: "This batch is context already delivered to its task."}); err != nil {
		t.Fatal(err)
	}
	row, _ = s.queries().GetAutomationEvaluation(ctx, claimed.ID)
	if row.Status != "observed" {
		t.Fatal("later input spuriously invalidated an earlier frozen batch", row.Status)
	}
}

func TestAutomationOversizedContextPersistsOnlyUnavailableBoundedEvidence(t *testing.T) {
	s, actor, member, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	for range 3 {
		sendAutomationDiscussion(t, s, actor, room, strings.Repeat("<", 8000))
	}
	candidate, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget)
	if err != nil || candidate != nil {
		t.Fatal("oversized context was sent to an assessment worker", candidate, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations WHERE status='needs_attention'", 1)
	assertScalar(t, s, "SELECT sum(evaluations) FROM automation_usage", 0)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	var id, raw string
	if err = s.db.QueryRow("SELECT id,candidate FROM automation_evaluations").Scan(&id, &raw); err != nil || len(raw) > automationAssessmentContextBytes {
		t.Fatal("unbounded candidate was persisted", len(raw), err)
	}
	if strings.Contains(raw, strings.Repeat("<", 32)) || strings.Contains(raw, strings.Repeat(`\u003c`, 32)) {
		t.Fatal("unavailable audit duplicated source bodies")
	}
	view, err := s.GetAutomationEvaluation(member, id)
	if err != nil || view.Assessment == nil || view.Assessment.Outcome != "unavailable" || !view.ContextLimited {
		t.Fatal(view, err)
	}
	var original string
	if err = s.db.QueryRow("SELECT value FROM automation_events WHERE source_id='room' LIMIT 1").Scan(&original); err != nil || !strings.Contains(original, strings.Repeat(`\u003c`, 32)) {
		t.Fatal("bounded evidence altered the immutable source original", err)
	}
	// Recovery must not reinterpret the bounded audit candidate as an approved
	// model response or claim it again.
	if err = s.CompleteAutomationEvaluation(ctx, id, orchestration.Assessment{Outcome: "accept", Goal: "An invented result", Reason: "Delayed callback"}); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestAutomationOversizedTaskContextRetainsHonestOmissionMetadata(t *testing.T) {
	s, actor, member, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	for index := range 5 {
		task, err := s.CreateTask(actor, room.ID, CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Goal: fmt.Sprintf("%d %s", index, strings.Repeat("<", 8000)), ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
		if err != nil {
			t.Fatal(err)
		}
		subscribeTaskTest(t, s, actor, task, "room", room.ID+":room")
	}
	sendAutomationDiscussion(t, s, actor, room, "An explicitly new request.")
	candidate, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget)
	if err != nil || candidate != nil {
		t.Fatal(candidate, err)
	}
	var id, raw string
	if err = s.db.QueryRow("SELECT id,candidate FROM automation_evaluations").Scan(&id, &raw); err != nil || len(raw) > automationAssessmentContextBytes {
		t.Fatal(len(raw), err)
	}
	view, err := s.GetAutomationEvaluation(member, id)
	if err != nil || view.TotalTasks != 5 || !view.TasksOmitted || !view.ContextLimited || len(view.Tasks) == 0 || len(view.Tasks) >= 5 {
		t.Fatal("bounded audit concealed omitted task context", len(view.Tasks), view.TotalTasks, view.TasksOmitted, err)
	}
}
