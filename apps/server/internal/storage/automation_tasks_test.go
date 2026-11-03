package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func subscribeTaskTest(t *testing.T, s *Store, actor context.Context, task CollaborationTask, source, thread string) orchestration.Subscription {
	t.Helper()
	sub, err := s.SetTaskSubscription(actor, task.ID, orchestration.SubscriptionInput{RequestID: newID(), SourceID: source, ThreadKey: thread, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestAutomationSubscriptionsFanOutIndependentInputsAndFreezeHistory(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	first, second := startTask(t, s, admin, room, writer), startTask(t, s, admin, room, writer)
	for _, task := range []CollaborationTask{first, second} {
		subscribeTaskTest(t, s, admin, task, "room", room.ID+":room")
	}
	request := SendMessageInput{RequestID: newID(), Content: "New evidence for both collaborations."}
	result, err := s.SendMessage(admin, room.ID, request, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	var inputs []TaskInput
	for _, task := range []CollaborationTask{first, second} {
		page, err := s.TaskInputsPage(admin, task.ID, 0, nil, 20)
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		input := page.Items[0]
		if input.MessageID != result.Message.ID || input.ID == input.MessageID || input.SourceEventID != "room:"+result.Message.ID || input.Content != request.Content || input.AuthorUserID != result.Message.AuthorUserID {
			t.Fatal("lost independent input identity or provenance", input)
		}
		inputs = append(inputs, input)
		for frontier, present := range map[int64]bool{0: false, 1: true} {
			history, err := s.TaskContextMessages(ctx, room.ID, "", 0, result.Message.Seq, task.ID, frontier)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, message := range history {
				found = found || message.ID == result.Message.ID
			}
			if found != present {
				t.Fatalf("task %s frontier %d exposed message=%v", task.ID, frontier, found)
			}
		}
	}
	if inputs[0].ID == inputs[1].ID {
		t.Fatal("fan-out reused an input identity")
	}
	if _, err = s.SendMessage(admin, room.ID, request, roomBudget); err != nil {
		t.Fatal(err)
	}
	for _, task := range []CollaborationTask{first, second} {
		state, _ := s.GetTaskState(ctx, task.ID)
		if state.InputRevision != 1 || state.MaxTurns != task.MaxTurns || state.MaxModelRequests != task.MaxModelRequests || state.ActiveRunID != task.ActiveRunID {
			t.Fatal("delivery replay scheduled work or expanded allowance", state)
		}
	}
	if _, err = s.db.Exec("CREATE TRIGGER reject_subscription_input BEFORE INSERT ON task_inputs BEGIN SELECT RAISE(ABORT,'reject input'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(admin, room.ID, SendMessageInput{RequestID: newID(), Content: "This whole source transaction must roll back."}, roomBudget); err == nil {
		t.Fatal("source committed without its subscribed inputs")
	}
	assertScalar(t, s, "SELECT count(*) FROM messages WHERE content='This whole source transaction must roll back.'", 0)
}

func TestAutomationSubscriptionReceiptsScopeAndTerminalTask(t *testing.T) {
	s, admin, member, room, writer, _ := taskFixture(t)
	task := startTask(t, s, admin, room, writer)
	in := orchestration.SubscriptionInput{RequestID: newID(), SourceID: "room", ThreadKey: room.ID + ":room", Enabled: true}
	if _, err := s.SetTaskSubscription(member, task.ID, in); err == nil {
		t.Fatal("another member managed the subscription")
	}
	sub, err := s.SetTaskSubscription(admin, task.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.SetTaskSubscription(admin, task.ID, in)
	if err != nil || replay != sub {
		t.Fatal("subscription replay changed its result", replay, err)
	}
	in.Enabled = false
	if _, err = s.SetTaskSubscription(admin, task.ID, in); err == nil {
		t.Fatal("receipt accepted changed content")
	}
	invalid := orchestration.SubscriptionInput{RequestID: newID(), SourceID: "room", ThreadKey: "another-room:room", Enabled: true}
	if _, err = s.SetTaskSubscription(admin, task.ID, invalid); err == nil {
		t.Fatal("subscribed outside the task Room")
	}
	state, _ := s.GetTaskState(ctx, task.ID)
	if state.InputProtocol != 1 {
		t.Fatal("subscription did not opt into bounded input batches")
	}
	if _, err = s.OperateTask(admin, task.ID, "cancel", TaskOperation{RequestID: newID(), Revision: state.Revision, PagedResponse: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(admin, room.ID, SendMessageInput{RequestID: newID(), Content: "Cancelled tasks cannot be resumed by subscribed discussion."}, roomBudget); err != nil {
		t.Fatal(err)
	}
	state, _ = s.GetTaskState(ctx, task.ID)
	if state.InputRevision != 0 || state.Status != "cancelled" {
		t.Fatal("delivery revived or appended to a cancelled task", state)
	}
	in.RequestID, in.Enabled, in.Revision = newID(), true, sub.Revision
	if _, err = s.SetTaskSubscription(admin, task.ID, in); err == nil {
		t.Fatal("enabled subscription on finished task")
	}
}

func TestTaskInputIdentityMigrationPreservesBytesAndReceipts(t *testing.T) {
	s, actor, task, receipt := legacyTaskInputs(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 20)); err != nil {
		t.Fatal(err)
	}
	type original struct {
		ID, Value string
		Seq       int64
	}
	var before []original
	rows, err := s.db.Query("SELECT id,value,seq FROM task_inputs WHERE task_id=? ORDER BY seq", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var item original
		if err = rows.Scan(&item.ID, &item.Value, &item.Seq); err != nil {
			t.Fatal(err)
		}
		before = append(before, item)
	}
	rows.Close()
	if err = s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, item := range before {
		var id, messageID, value string
		var seq int64
		if err = s.db.QueryRow("SELECT id,message_id,value,seq FROM task_inputs WHERE id=?", item.ID).Scan(&id, &messageID, &value, &seq); err != nil || id != item.ID || messageID != item.ID || value != item.Value || seq != item.Seq {
			t.Fatal("migration rewrote an original", id, messageID, value, seq, err)
		}
	}
	var after string
	if err = s.db.QueryRow("SELECT result FROM task_requests WHERE request_id='legacy-receipt'").Scan(&after); err != nil || after != receipt {
		t.Fatal("migration rewrote a historical receipt", err)
	}
	replay, err := s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: "legacy-receipt", Content: "Original input two."})
	if err != nil || replay.Inputs[0].ID != task.Inputs[0].ID || replay.Inputs[0].MessageID != "" {
		t.Fatal("historical replay changed wire input identity", replay, err)
	}
}

func TestAutomationAnswerRequiresSentQuestionAndPreservesExternalAttribution(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, admin, room)
	receiveTestGitHub(t, s, source, testGitHubPayload("Original issue", "Original context."))
	policy, err := s.SaveAutomationPolicy(admin, room.ID, source.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Follow explicitly requested work.", AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, MaxTurns: 6, MaxModelRequests: 24, DailyEvaluations: 10, DailyTasks: 3, AllowAnswers: true})
	if err != nil {
		t.Fatal(err)
	}
	task := startTask(t, s, admin, room, writer)
	subscribeTaskTest(t, s, admin, task, source.ID, "123:3")
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: newID(), Kind: "ask_person", Question: "Which title?"})
	task, _ = s.GetTaskState(ctx, task.ID)
	answer := "A title  with\n  authored spacing."
	event := orchestration.SourceEvent{ID: "github:" + newID(), RoomID: room.ID, SourceID: source.ID, ThreadKey: "123:3", Kind: "issue_comment.created", ActorID: "7", ActorLogin: "external-person", ActorKind: "user", Content: "/camp\tanswer\t" + task.Question.ID + "\n" + answer, OccurredAt: now()}
	callAnswer := func() bool {
		t.Helper()
		handled := false
		if err := s.transaction(ctx, func(tx *sql.Tx) (err error) { handled, err = answerAutomationEvent(ctx, tx, event, policy); return err }); err != nil {
			t.Fatal(err)
		}
		return handled
	}
	if callAnswer() {
		t.Fatal("an unissued external question was answered")
	}
	deliveryID := newID()
	delivery, _ := json.Marshal(orchestration.Delivery{ID: deliveryID, TaskID: task.ID, SourceID: source.ID, PolicyID: policy.ID, RepositoryID: 123, Number: 4, QuestionID: task.Question.ID, Status: "sent"})
	if _, err = s.db.Exec(`INSERT INTO automation_deliveries(id,task_id,source_id,policy_id,revision,status,value,credential_ref,created_at) VALUES(?,?,?,?,1,'sent',?,'',?)`, deliveryID, task.ID, source.ID, policy.ID, string(delivery), now()); err != nil {
		t.Fatal(err)
	}
	if callAnswer() {
		t.Fatal("A question delivered to another discussion authorized this answer")
	}
	if _, err = s.db.Exec(`UPDATE automation_deliveries SET value=json_set(value,'$.number',3) WHERE id=?`, deliveryID); err != nil {
		t.Fatal(err)
	}
	event.ActorKind = "bot"
	if callAnswer() {
		t.Fatal("A bot answered a human question")
	}
	event.ActorKind = "user"
	if !callAnswer() {
		t.Fatal("valid external answer did not continue the task")
	}
	answered, _ := s.GetTaskState(ctx, task.ID)
	if answered.Status != "queued" || answered.Question.Answer != answer || answered.Question.AnsweredByUserID != "" || answered.Question.AnsweredByExternalActorID != "7" || answered.Question.AnswerSourceEventID != event.ID || answered.MaxTurns != task.MaxTurns || answered.MaxModelRequests != task.MaxModelRequests {
		t.Fatal("answer lost authorship, spacing or finite grant", answered)
	}
	inputs, err := s.TaskInputsPage(admin, task.ID, 0, nil, 20)
	if err != nil || len(inputs.Items) != 1 || inputs.Items[0].AuthorUserID != "" || inputs.Items[0].ExternalActorID != "7" || inputs.Items[0].Content != event.Content {
		t.Fatal("external input was attributed to a member", inputs, err)
	}
	if callAnswer() {
		t.Fatal("the same question was answered twice")
	}
}

func TestAutomationTaskCreationUsesSponsorAndFiniteGrantWithoutHumanAuthorship(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	owner, _ := UserFromContext(admin)
	policy, err := s.SaveAutomationPolicy(admin, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Accept explicit requests.", AllowedActorIDs: []string{owner.ID}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, MaxTurns: 2, MaxModelRequests: 5, DailyEvaluations: 10, DailyTasks: 2})
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.SendMessage(admin, room.ID, SendMessageInput{RequestID: newID(), Content: "Please prepare a useful document."}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	event := orchestration.SourceEvent{ID: "room:" + message.Message.ID, RoomID: room.ID, SourceID: "room", ThreadKey: room.ID + ":room", MessageID: message.Message.ID, Content: message.Message.Content, ActorKind: "user", ActorID: owner.ID, OccurredAt: now()}
	candidate := orchestration.Candidate{ID: newID(), Policy: policy, Events: []orchestration.SourceEvent{event}, Budget: orchestration.ExecutionBudget{Model: roomBudget.Model, RuntimeKey: roomBudget.RuntimeKey, MaxTokens: roomBudget.MaxTokens, MaxSteps: roomBudget.MaxSteps, TimeoutSeconds: roomBudget.TimeoutSeconds}}
	var task CollaborationTask
	if err = s.transaction(ctx, func(tx *sql.Tx) (err error) {
		task, err = createAutomationTask(ctx, tx, candidate, orchestration.Assessment{Outcome: "accept", Goal: "Prepare a useful document."})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	origin, err := s.GetMessage(ctx, task.OriginMessageID)
	if err != nil || origin.AuthorKind != "system" || origin.AuthorUserID != "" || task.RequestedByUserID != owner.ID || task.MaxTurns != 2 || task.MaxModelRequests != 5 || task.AutomationPolicyID != policy.ID || task.AutomationPolicyRevision != policy.Revision || task.InputRevision != 1 {
		t.Fatal("automation impersonated a member or widened its grant", task, origin, err)
	}
	subs, err := s.TaskSubscriptions(admin, task.ID)
	if err != nil || len(subs) != 1 || subs[0].ThreadKey != event.ThreadKey {
		t.Fatal(subs, err)
	}
	if _, err = s.db.Exec("UPDATE automation_policies SET enabled=0 WHERE id=?", policy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(admin, room.ID, SendMessageInput{RequestID: newID(), Content: strings.Repeat("Context ", 100)}, roomBudget); err != nil {
		t.Fatal(err)
	}
	state, _ := getTask(ctx, sqlgen.New(s.db), task.ID)
	if state.InputRevision != 1 || state.Status != "queued" {
		t.Fatal("disabled intake affected existing execution or accepted new input", state)
	}
}
