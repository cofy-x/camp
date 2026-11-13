package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func createIntentWork(t *testing.T, s *Store, actor context.Context, room Room, writer Agent, start bool) orchestration.WorkItem {
	t.Helper()
	w, e := s.CreateWorkItemWithBudget(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Follow the release request", Start: start, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	return w
}

func TestWorkIntentAtomicMessageStartAndFollowUsesFiniteDefaults(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	var message Message
	if e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		seq, e := q.NextMessageSequence(ctx, room.ID)
		if e != nil {
			return e
		}
		message = Message{ID: newID(), RoomID: room.ID, Seq: seq, AuthorKind: "agent", AgentID: writer.ID, Content: "The exact assistant result to follow.", CreatedAt: now(), UpdatedAt: now()}
		return insertMessage(ctx, q, message, "")
	}); e != nil {
		t.Fatal(e)
	}
	defaults, e := s.WorkItemDefaults(actor, room.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	input := orchestration.WorkItemInput{RequestID: "atomic-message-start", Goal: "Follow the saved result", MessageID: message.ID, Start: true, Follow: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DueAt: "2026-01-01", ExpiresAt: defaults.ExpiresAt}
	w, e := s.CreateWorkItemWithBudget(actor, room.ID, input, roomBudget)
	if e != nil || w.Status != "running" || w.CompletionMode != "human" || w.CompletionCriteria != "" || w.ExpiresAt != defaults.ExpiresAt || w.DueAt != "2026-01-01" {
		t.Fatal(w, e)
	}
	if len(w.Watches) != 1 || w.Watches[0].ThreadKey != room.ID+":"+message.ID || len(w.Evidence) != 1 {
		t.Fatal("message follow broadened to Room discussion", w)
	}
	task, e := s.GetTaskState(ctx, w.ActiveTaskID)
	if e != nil || task.MaxTurns != defaults.Execution.MaxTurns || task.MaxModelRequests != defaults.Execution.MaxModelRequests || task.InputRevision != 1 {
		t.Fatal(task, e)
	}
	run, e := s.GetRun(ctx, task.ActiveRunID)
	if e != nil || run.TaskInputRevision != task.InputRevision {
		t.Fatal("initial execution froze incomplete source context", run, e)
	}
	event, e := s.GetAutomationEvent(actor, "room:"+message.ID)
	if e != nil || event.Content != message.Content || event.Status != "context" || event.ActorKind != "agent" {
		t.Fatal("original fact was rewritten or became authority", event, e)
	}
	reply := sendAutomationDiscussion(t, s, actor, room, "Unrelated Room discussion").Message
	if reply.ID == "" {
		t.Fatal(reply)
	}
	current, _ := s.GetWorkItem(actor, w.ID)
	if len(current.Evidence) != len(w.Evidence) {
		t.Fatal("follow consumed unrelated Room discussion", current)
	}
	if _, e = s.SendMessage(actor, room.ID, SendMessageInput{RequestID: newID(), ThreadRootID: message.ID, Content: "A reply to the followed result"}, roomBudget); e != nil {
		t.Fatal(e)
	}
	current, _ = s.GetWorkItem(actor, w.ID)
	if len(current.Evidence) != len(w.Evidence)+1 {
		t.Fatal("follow did not associate its replies", current)
	}
	replay, e := s.CreateWorkItemWithBudget(actor, room.ID, input, Budget{})
	if e != nil || replay.ID != w.ID || replay.ActiveTaskID != w.ActiveTaskID {
		t.Fatal("lost response admitted a second execution", replay, e)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_items", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
}

func TestWorkIntentFailedAtomicStartLeavesNoMatterOrSourceMutation(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	m := sendAutomationDiscussion(t, s, actor, room, "Keep this original").Message
	input := orchestration.WorkItemInput{RequestID: "atomic-failure", Goal: "Prepare a result", MessageID: m.ID, Start: true, Follow: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
	if _, e := s.db.Exec("CREATE TRIGGER reject_first_work_run BEFORE INSERT ON runs BEGIN SELECT RAISE(ABORT,'reject first Run'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateWorkItemWithBudget(actor, room.ID, input, roomBudget); e == nil {
		t.Fatal("partial start succeeded")
	}
	for _, table := range []string{"work_items", "collaboration_tasks", "work_item_watches", "work_item_requests"} {
		assertScalar(t, s, "SELECT count(*) FROM "+table, 0)
	}
	assertScalar(t, s, "SELECT count(*) FROM orchestration_records", 1)
	original, e := s.GetAutomationEvent(actor, "room:"+m.ID)
	if e != nil || original.Content != m.Content {
		t.Fatal(original, e)
	}
	if _, e = s.db.Exec("DROP TRIGGER reject_first_work_run"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateWorkItemWithBudget(actor, room.ID, input, roomBudget); e != nil {
		t.Fatal(e)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
}

func TestWorkIntentMissingCriteriaRequiresHumanCompletion(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w := createIntentWork(t, s, actor, room, writer, false)
	if w.CompletionMode != "human" || w.CompletionCriteria != "" {
		t.Fatal("invented completion criteria", w)
	}
	if e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		completed, e := completeWorkItem(ctx, q, &w, "A model proposes completion.")
		if e != nil {
			return e
		}
		if completed {
			t.Fatal("model completed a human-confirmed matter")
		}
		return saveWorkItem(ctx, q, &w)
	}); e != nil {
		t.Fatal(e)
	}
	if w.Status != "waiting_for_person" || w.QuestionKind != orchestration.WorkQuestionCompletion {
		t.Fatal(w)
	}
	w, e := s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "human-complete", Revision: w.Revision, Kind: "complete"}, Budget{})
	if e != nil || w.Status != "completed" {
		t.Fatal(w, e)
	}
	found := false
	for _, ref := range w.Evidence {
		event, e := s.GetAutomationEvent(actor, ref.EventID)
		if e != nil {
			t.Fatal(e)
		}
		found = found || event.Kind == "workitem.confirmation" && event.ActorID == actorID(actor)
	}
	if !found {
		t.Fatal("completion lost the actual human confirmation", w.Evidence)
	}
}

func TestWorkIntentAnswerTargetsExactActiveQuestionWithoutGrantExpansion(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w := createIntentWork(t, s, actor, room, writer, true)
	task, e := s.GetTaskState(ctx, w.ActiveTaskID)
	if e != nil {
		t.Fatal(e)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: "question", Kind: "ask_person", Question: "Which audience?"})
	task, e = s.GetTaskState(ctx, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	w, e = s.GetWorkItem(actor, w.ID)
	if e != nil {
		t.Fatal(e)
	}
	if w.Execution == nil || w.Execution.Question == nil {
		t.Fatal("matter did not expose actual Task question", w)
	}
	input := orchestration.WorkItemOperation{RequestID: "answer-active", Revision: w.Revision, Kind: "answer", TaskID: task.ID, TaskRevision: task.Revision - 1, QuestionID: task.Question.ID, Content: "The engineering team"}
	_, e = s.OperateWorkItem(actor, w.ID, input, Budget{})
	requireCode(t, e, "revision_conflict")
	input.TaskRevision = task.Revision
	answered, e := s.OperateWorkItem(actor, w.ID, input, Budget{})
	if e != nil || answered.Execution == nil || answered.Execution.Status != "queued" {
		t.Fatal(answered, e)
	}
	current, e := s.GetTaskState(ctx, task.ID)
	if e != nil || current.Question.Answer != input.Content || current.MaxTurns != task.MaxTurns || current.MaxModelRequests != task.MaxModelRequests || answered.Usage.Tasks != 1 || answered.Usage.ReservedModelRequests != 24 {
		t.Fatal("answer expanded or lost finite authorization", current, answered, e)
	}
	replay, e := s.OperateWorkItem(actor, w.ID, input, Budget{})
	if e != nil || replay.Revision != answered.Revision {
		t.Fatal("answer replay changed original outcome", replay, e)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
	runs, _ := s.ListTaskRuns(ctx, task.ID)
	if len(runs) != 2 {
		t.Fatal("answer created duplicate rounds", runs)
	}
}

func TestWorkIntentOneStepStopPersistsUntilActiveCleanupAndNeverReopens(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "running"}[running], func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			w := createIntentWork(t, s, actor, room, writer, true)
			task, e := s.GetTaskState(ctx, w.ActiveTaskID)
			if e != nil {
				t.Fatal(e)
			}
			run, e := s.GetRun(ctx, task.ActiveRunID)
			if e != nil {
				t.Fatal(e)
			}
			var attempt Attempt
			if running {
				enableTaskInputs(t, s, run.RunnerID)
				run, attempt = prepareTaskTestAttempt(t, s, run)
			}
			w, e = s.GetWorkItem(actor, w.ID)
			if e != nil {
				t.Fatal(e)
			}
			stop := orchestration.WorkItemOperation{RequestID: "stop-once", Revision: w.Revision, Kind: "stop"}
			stopped, e := s.OperateWorkItem(actor, w.ID, stop, Budget{})
			if e != nil || !stopped.StopRequested {
				t.Fatal(stopped, e)
			}
			if running {
				if stopped.Status != "stopping" {
					t.Fatal("active cleanup was claimed complete", stopped)
				}
				if e = s.BeginAttemptModel(ctx, attempt.ID, run.RunnerID); e == nil {
					t.Fatal("stop retained model authority")
				}
				run.Status = "cancelled"
				if _, e = s.FinishRemoteRun(ctx, attempt.ID, run.RunnerID, "stop-cleanup", strings.Repeat("f", 64), run, "Stopped", false, true); e != nil {
					t.Fatal(e)
				}
			}
			current, e := s.GetWorkItem(actor, w.ID)
			if e != nil || current.Status != "cancelled" || current.ActiveTaskID != "" {
				t.Fatal("cleanup did not finish stop", current, e)
			}
			replay, e := s.OperateWorkItem(actor, w.ID, stop, Budget{})
			if e != nil || replay.Revision != stopped.Revision || replay.Status != stopped.Status {
				t.Fatal("stop replay lost its original receipt", replay, e)
			}
			_, e = s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: current.Revision, Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
			requireCode(t, e, "work_item_finished")
			assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
		})
	}
}

func TestWorkIntentLaterDelegationAndAnswerContinueAreExplicit(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	w := createIntentWork(t, s, actor, room, writer, false)
	in := orchestration.WorkItemOperation{RequestID: "delegate", Revision: w.Revision, Kind: "delegate", PolicyID: policy.ID, PolicyRevision: policy.Revision + 1}
	_, e := s.OperateWorkItem(actor, w.ID, in, Budget{})
	requireCode(t, e, "revision_conflict")
	in.PolicyRevision = policy.Revision
	w, e = s.OperateWorkItem(actor, w.ID, in, Budget{})
	if e != nil || w.PolicyID != policy.ID || w.DelegationPolicyRevision != policy.Revision {
		t.Fatal(w, e)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 0)
	w, e = s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "pause-delegation", Revision: w.Revision, Kind: "delegate", PolicyID: ""}, Budget{})
	if e != nil || w.PolicyID != "" {
		t.Fatal("later delegation was only written to JSON, not authority columns", w, e)
	}
	w, e = s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "question", Revision: w.Revision, Kind: "request_judgment", Question: "Should I continue?"}, Budget{})
	if e != nil {
		t.Fatal(e)
	}
	w, e = s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "answer-start", Revision: w.Revision, Kind: "answer_continue", Content: "Yes, prepare the release", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
	if e != nil || w.Status != "running" || w.Usage.Tasks != 1 {
		t.Fatal(w, e)
	}
	task, e := s.GetTaskState(ctx, w.ActiveTaskID)
	if e != nil || task.InputRevision < 1 {
		t.Fatal("explicit answer was not frozen before execution", task, e)
	}
}

func TestWorkIntentDefaultsBusinessDueAndAuthorizationExpiryAreSeparate(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	defaults, e := s.WorkItemDefaults(actor, room.ID, "")
	if e != nil || defaults.Execution.MaxTurns != 6 || defaults.Execution.MaxModelRequests != 24 || defaults.Budget.MaxModelRequests != 96 {
		t.Fatal(defaults, e)
	}
	expiry, e := time.Parse(time.RFC3339Nano, defaults.ExpiresAt)
	if e != nil || expiry.Before(time.Now().Add(29*24*time.Hour)) {
		t.Fatal(defaults, e)
	}
	w, e := s.CreateWorkItem(actor, room.ID, orchestration.WorkItemInput{RequestID: "business-due", Goal: "Handle an overdue delivery", DueAt: "2020-01-01"})
	if e != nil || workExpired(w) || w.DueAt != "2020-01-01" || w.ExpiresAt != w.Deadline {
		t.Fatal(w, e)
	}
	// A default proposal grants no task/model work and carries no execution secret.
	raw, _ := json.Marshal(defaults)
	if strings.Contains(string(raw), "runtimeKey") || strings.Contains(string(raw), "credential") {
		t.Fatal(string(raw))
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	_ = writer
}

func TestWorkIntentOwnerCanStopChildStartedByAdministrator(t *testing.T) {
	s, admin, owner, room, writer, _ := taskFixture(t)
	w, e := s.CreateWorkItem(owner, room.ID, orchestration.WorkItemInput{RequestID: "owner-record", Goal: "Handle my request"})
	if e != nil {
		t.Fatal(e)
	}
	w, e = s.OperateWorkItem(admin, w.ID, orchestration.WorkItemOperation{RequestID: "admin-start", Revision: w.Revision, Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.GetTaskState(ctx, w.ActiveTaskID)
	if e != nil || task.RequestedByUserID != actorID(admin) {
		t.Fatal(task, e)
	}
	stopped, e := s.OperateWorkItem(owner, w.ID, orchestration.WorkItemOperation{RequestID: "owner-stop", Revision: w.Revision, Kind: "stop"}, Budget{})
	if e != nil || stopped.Status != "cancelled" {
		t.Fatal("parent owner could not revoke their own matter", stopped, e)
	}
	child, e := s.GetTaskState(ctx, task.ID)
	if e != nil || child.Status != "cancelled" || child.RequestedByUserID != actorID(admin) {
		t.Fatal("stop changed attribution or retained child execution", child, e)
	}
	journal, e := s.OrchestrationPage(owner, room.ID, 0, nil, 20)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, record := range journal.Items {
		if record.Trigger.Kind == orchestration.TaskCancel && record.Trigger.ActorID == actorID(owner) {
			found = true
		}
	}
	if !found {
		t.Fatal("stop impersonated the original administrator", journal)
	}
}

func TestWorkIntentStopRetiresPendingEffectsWithoutLosingUnknownReceipts(t *testing.T) {
	s, actor, w, _, _ := workActionFixture(t)
	claim, e := s.ClaimAutomationDelivery(ctx)
	if e != nil || claim == nil {
		t.Fatal(claim, e)
	}
	completeTestWorkAction(t, s, claim, "unknown", 0)
	w, e = s.GetWorkItem(actor, w.ID)
	if e != nil {
		t.Fatal(e)
	}
	stopped, e := s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "stop-effects", Revision: w.Revision, Kind: "stop"}, Budget{})
	if e != nil || stopped.Status != "cancelled" {
		t.Fatal(stopped, e)
	}
	deliveries, e := s.TaskAutomationDeliveries(actor, claim.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	var unknown orchestration.Delivery
	for _, delivery := range deliveries {
		if delivery.Status != "withdrawn" || !delivery.Withdrawn {
			t.Fatal("stopped matter retained dispatch work", delivery)
		}
		if delivery.ID == claim.ID {
			unknown = delivery
		}
	}
	if len(unknown.Attempts) != 1 || unknown.Attempts[0].Outcome != "unknown" {
		t.Fatal("stop erased an uncertain earlier POST", unknown)
	}
	if next, e := s.ClaimAutomationDelivery(ctx); e != nil || next != nil {
		t.Fatal("stop allowed another POST claim", next, e)
	}
	if _, e = s.OperateAutomationDelivery(actor, unknown.ID, "reconcile", AutomationDeliveryInput{RequestID: "check-stopped-effect", Revision: unknown.Revision}); e != nil {
		t.Fatal(e)
	}
	check, e := s.ClaimAutomationDelivery(ctx)
	if e != nil || check == nil || !check.Reconcile || !check.Withdrawn {
		t.Fatal("stop prevented read-only receipt recovery", check, e)
	}
	completeTestWorkAction(t, s, check, "sent", 401)
	current, e := s.GetWorkItem(actor, w.ID)
	if e != nil || current.Status != "cancelled" {
		t.Fatal("late receipt reopened stopped matter", current, e)
	}
}

func TestWorkIntentStaleTaskAnswerCannotBecomeMatterJudgment(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w := createIntentWork(t, s, actor, room, writer, false)
	w, e := s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "ask-matter", Revision: w.Revision, Kind: "request_judgment", Question: "Approve?"}, Budget{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "stale-task-answer", Revision: w.Revision, Kind: "answer", TaskID: "ended-task", TaskRevision: 3, QuestionID: "old-question", Content: "Yes"}, Budget{})
	requireCode(t, e, "question_answered")
	current, e := s.GetWorkItem(actor, w.ID)
	if e != nil || current.Question != w.Question || current.Status != "waiting_for_person" {
		t.Fatal(current, e)
	}
}

func TestWorkIntentTypedCompletionConfirmationRetainsExternalEffectGate(t *testing.T) {
	s, actor, w, _, _ := workActionFixture(t, "")
	if e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		ok, e := completeWorkItem(ctx, q, &w, "The model proposes completion.")
		if e != nil {
			return e
		}
		if ok {
			t.Fatal("model bypassed human confirmation")
		}
		return saveWorkItem(ctx, q, &w)
	}); e != nil {
		t.Fatal(e)
	}
	w, e := s.GetWorkItem(actor, w.ID)
	if e != nil || w.Status != "waiting_for_person" || w.QuestionKind != orchestration.WorkQuestionCompletion {
		t.Fatal(w, e)
	}
	view, e := func() (orchestration.WorkItemSummary, error) {
		var out orchestration.WorkItemSummary
		err := s.readTransaction(ctx, func(tx *sql.Tx) error { var e error; out, e = candidateWorkSummary(ctx, sqlgen.New(tx), w); return e })
		return out, err
	}()
	if e != nil || view.QuestionKind != orchestration.WorkQuestionCompletion {
		t.Fatal("frozen summary lost the typed confirmation", view, e)
	}
	confirmed, e := s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "confirm-with-pending-effects", Revision: w.Revision, Kind: "complete", Comment: "The requested document is complete."}, Budget{})
	if e != nil || confirmed.Status != "waiting" || confirmed.QuestionKind != "" || confirmed.Question != "" || !strings.Contains(confirmed.WaitCondition, "receipts") {
		t.Fatal("human confirmation claimed an unconfirmed external effect", confirmed, e)
	}
	found := false
	for _, ref := range confirmed.Evidence {
		fact, e := s.GetAutomationEvent(actor, ref.EventID)
		if e != nil {
			t.Fatal(e)
		}
		found = found || fact.Kind == "workitem.confirmation" && fact.Content == "The requested document is complete." && fact.ActorID == actorID(actor)
	}
	if !found {
		t.Fatal("confirmation lost the actual authored evidence", confirmed.Evidence)
	}
	for index := range 2 {
		claim, e := s.ClaimAutomationDelivery(ctx)
		if e != nil || claim == nil {
			t.Fatal(claim, e)
		}
		completeTestWorkAction(t, s, claim, "sent", int64(500+index))
	}
	current, e := s.GetWorkItem(actor, w.ID)
	if e != nil {
		t.Fatal(e)
	}
	if current.Status != "completed" || current.QuestionKind != "" || current.Question != "" {
		t.Fatal("confirmed effects required another human confirmation", current)
	}
	replayed, e := s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "confirm-with-pending-effects", Revision: w.Revision, Kind: "complete", Comment: "The requested document is complete."}, Budget{})
	if e != nil || replayed.Status != confirmed.Status || replayed.Revision != confirmed.Revision {
		t.Fatal("current completion rewrote the original operation receipt", replayed, e)
	}
}

func TestWorkIntentAutomaticSourceCannotReplaceTypedHumanQuestion(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	message := sendAutomationDiscussion(t, s, actor, room, "Original request").Message
	w, e := s.CreateWorkItem(actor, room.ID, orchestration.WorkItemInput{RequestID: "human-question-matter", Goal: "Follow the request", PolicyID: policy.ID, EvidenceIDs: []string{"room:" + message.ID}})
	if e != nil {
		t.Fatal(e)
	}
	w, e = s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: "need-human", Revision: w.Revision, Kind: "request_judgment", Question: "Choose the delivery audience."}, Budget{})
	if e != nil {
		t.Fatal(e)
	}
	var rejected error
	if e = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a := orchestration.Assessment{Outcome: "wait", WorkItemID: w.ID, WaitCondition: "New context", Reason: "Incoming event"}
		_, rejected = selectAssessmentWorkItem(ctx, q, orchestration.Candidate{Policy: policy, Events: []orchestration.SourceEvent{{ID: "new-context", Seq: w.DelegationStartAfter + 1}}, WorkItems: []orchestration.WorkItemSummary{workSummary(w)}}, &a)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	requireCode(t, rejected, "invalid_work_item")
	current, e := s.GetWorkItem(actor, w.ID)
	if e != nil || current.Question != w.Question || current.QuestionKind != orchestration.WorkQuestionJudgment {
		t.Fatal(current, e)
	}
}
