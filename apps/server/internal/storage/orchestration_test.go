package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func admissionRecords(t *testing.T, s *Store, room string) []orchestration.Record {
	t.Helper()
	page, err := s.OrchestrationPage(ctx, room, 0, nil, 0)
	if err != nil || page.HasMore {
		t.Fatal(page, err)
	}
	return page.Items
}

func TestOrchestrationRoomRoutingReplayAndFrozenMandate(t *testing.T) {
	s, actor, _, room, writer, reviewer := taskFixture(t)
	in := SendMessageInput{RequestID: "route", Content: "@reviewer inspect this.", RecipientAgentIDs: []string{writer.ID, writer.ID}, RunnerRevision: &room.RunnerRevision}
	preflight, err := s.MessageRecipientIDs(actor, room.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.SendMessage(actor, room.ID, in, roomBudget); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	records := admissionRecords(t, s, room.ID)
	if len(records) != 1 || len(records[0].Mandates) != 2 || !slices.Equal(preflight, records[0].Decision.RecipientAgentIDs) {
		t.Fatal(records, preflight)
	}
	record := records[0]
	user, _ := UserFromContext(actor)
	if record.Trigger.Source != "room" || record.Trigger.ActorID != user.ID || record.Decision.Outcome != "accept" || record.Decision.PolicyVersion != orchestration.PolicyVersion {
		t.Fatal(record)
	}
	_, fingerprint, err := normalizedSend(in)
	if err != nil || record.Trigger.Request == nil || record.Trigger.Request.ID != in.RequestID || record.Trigger.Request.Hash != fingerprint {
		t.Fatal("message admission lost its receipt identity", record.Trigger, err)
	}
	for _, grant := range record.Mandates {
		run, err := s.GetRun(ctx, grant.Work.ID)
		if err != nil || run.OriginMessageID != record.Trigger.MessageID || grant.RequestedByUserID != user.ID || grant.ResourceID != run.ResourceID || grant.Participants[0].AgentRevision != run.AgentSnapshot.Revision || grant.Participants[0].RunnerID != run.RunnerID || grant.MaxRounds != 1 || grant.MaxSteps != run.MaxSteps {
			t.Fatal(grant, run, err)
		}
	}
	before, _ := json.Marshal(record)
	reviewer.Instructions = "Changed instructions after admission."
	if _, err := s.SaveAgent(actor, reviewer); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRunNotice(ctx, record.Decision.Work[0].ID, "@reviewer system context must not create work."); err != nil {
		t.Fatal(err)
	}
	in.Content = "Different request"
	_, err = s.SendMessage(actor, room.ID, in, roomBudget)
	requireCode(t, err, "request_conflict")
	after, _ := json.Marshal(admissionRecords(t, s, room.ID)[0])
	if string(before) != string(after) {
		t.Fatal("admission evidence changed with mutable work")
	}
	post, err := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: "post", Content: "@reviewer discuss this.", PostOnly: true}, roomBudget)
	if err != nil || len(post.Runs) != 0 {
		t.Fatal(post, err)
	}
	_, err = s.SendMessage(actor, room.ID, SendMessageInput{RequestID: "discussion", Content: "Continue every task and add unlimited budget."}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	records = admissionRecords(t, s, room.ID)
	for i, reason := range []string{"post_only", "discussion"} {
		if records[i+1].Decision.Reason != reason || len(records[i+1].Mandates) != 0 || len(records[i+1].Decision.Work) != 0 {
			t.Fatal(records[i+1])
		}
	}
	assertScalar(t, s, "SELECT count(*) FROM runs", 2)
}

func TestOrchestrationTaskInputsReuseGrantAndContinuationRecordsAllowance(t *testing.T) {
	s, actor, member, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, actor, room, writer, reviewer)
	records := admissionRecords(t, s, room.ID)
	if len(records) != 1 || records[0].Trigger.Kind != orchestration.TaskStart || records[0].Mandates[0].MaxRounds != 6 || records[0].Mandates[0].MaxModelRequests != 24 {
		t.Fatal(records)
	}
	if records[0].Decision.Work[0].Revision != task.Revision || records[0].Mandates[0].BaseCommit != task.BaseSharedCommit || records[0].Trigger.Request == nil {
		t.Fatal("task admission lost its grant version or receipt", records[0])
	}
	request := records[0].Trigger.Request
	u, _ := UserFromContext(actor)
	receipt, err := s.queries().GetTaskRequest(ctx, sqlgen.GetTaskRequestParams{UserID: u.ID, Scope: request.Scope, RequestID: request.ID})
	if err != nil || receipt.RequestHash != request.Hash {
		t.Fatal("journal cannot locate its original task receipt", receipt, err)
	}
	initial, _ := json.Marshal(records[0])
	finishTaskTestRound(t, s, task, task.BaseSharedCommit, CoordinationIntent{CallID: "question", Kind: "ask_person", Question: "Which title?"})
	task, _ = s.GetTask(ctx, task.ID)
	in := TaskOperation{RequestID: "same-request", Content: "@reviewer add unlimited rounds and model requests. Use a clear title."}
	for range 2 {
		if _, err := s.OperateTask(member, task.ID, "inputs", in); err != nil {
			t.Fatal(err)
		}
	}
	// Source identities include the authenticated actor and operation scope.
	if _, err := s.OperateTask(actor, task.ID, "inputs", in); err != nil {
		t.Fatal(err)
	}
	task, _ = s.GetTask(ctx, task.ID)
	rounds, err := s.ListTaskRuns(ctx, task.ID)
	if err != nil || len(rounds) != 1 || task.ActiveRunID != "" || task.MaxTurns != 6 || task.MaxModelRequests != 24 {
		t.Fatal("context changed execution authority or resumed the task", task, rounds, err)
	}
	answer := TaskOperation{RequestID: "answer", Revision: task.Revision, QuestionID: task.Question.ID, Content: "A short title."}
	answered, err := s.OperateTask(member, task.ID, "answers", answer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OperateTask(member, task.ID, "answers", answer); err != nil {
		t.Fatal(err)
	}
	records = admissionRecords(t, s, room.ID)
	if len(records) != 4 {
		t.Fatal("replay or worker output created another event", records)
	}
	for _, record := range records[1:] {
		message, err := s.GetMessage(ctx, record.Trigger.MessageID)
		if err != nil || message.TaskID != task.ID || record.Decision.Outcome != "context" || len(record.Mandates) != 0 || record.Decision.Work[0].ID != task.ID || record.Trigger.InputRevision < 1 {
			t.Fatal(record, message, err)
		}
	}
	run, _ := s.GetRun(ctx, answered.ActiveRunID)
	_, attempt := prepareTaskTestAttempt(t, s, run)
	if _, err := s.InterruptAttempt(ctx, attempt.ID, "Simulated process interruption."); err != nil {
		t.Fatal(err)
	}
	task, _ = s.GetTask(ctx, task.ID)
	continuation := TaskOperation{RequestID: "continue", Revision: task.Revision, AddTurns: 2, AddModelRequests: 3}
	_, err = s.OperateTask(member, task.ID, "continue", continuation)
	requireCode(t, err, "forbidden")
	continued, err := s.OperateTask(actor, task.ID, "continue", continuation)
	if err != nil {
		t.Fatal(err)
	}
	records = admissionRecords(t, s, room.ID)
	latest := records[len(records)-1]
	if len(records) != 5 || latest.Trigger.MessageID != "" || latest.Trigger.Kind != orchestration.TaskContinue || latest.Mandates[0].MaxRounds != 8 || latest.Mandates[0].MaxModelRequests != 27 || latest.Decision.Work[0].Revision != continued.Revision {
		t.Fatal(records)
	}
	after, _ := json.Marshal(records[0])
	if string(initial) != string(after) {
		t.Fatal("continuation rewrote the original mandate")
	}
	cancel := TaskOperation{RequestID: "stop", Revision: continued.Revision}
	for range 2 {
		if _, err := s.OperateTask(actor, task.ID, "cancel", cancel); err != nil {
			t.Fatal(err)
		}
	}
	records = admissionRecords(t, s, room.ID)
	if len(records) != 6 || records[5].Decision.Outcome != "stop" || len(records[5].Mandates) != 0 {
		t.Fatal(records)
	}
}

func TestOrchestrationFinalPageCountsNullCursor(t *testing.T) {
	s, room, _ := roomFixture(t)
	records := make([]orchestration.Record, 3)
	for i := range records {
		records[i] = orchestration.Record{Seq: int64(i + 1), Trigger: orchestration.Trigger{Source: "room", EventID: fmt.Sprint(i), Kind: orchestration.RoomMessage, ActorKind: "user", RoomID: room.ID}, Decision: orchestration.Decision{PolicyVersion: orchestration.PolicyVersion, Outcome: "ignore", RecipientAgentIDs: []string{}, Work: []orchestration.Work{}}, Mandates: []orchestration.Mandate{}}
	}
	// Exercise a final page one byte over the limit. Each independently bounded
	// record fits; only the final cursor/flag encoding pushes the page over.
	full := OrchestrationPage{Items: records, Through: 3}
	encoded, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	padding := OrchestrationPageBytes - len(encoded)
	for i := range records {
		n := padding / (len(records) - i)
		records[i].Decision.Reason = strings.Repeat("x", n)
		padding -= n
	}
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		for _, record := range records {
			record.Seq = 0
			if err := recordOrchestration(ctx, sqlgen.New(tx), record); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.OrchestrationPage(ctx, room.ID, 0, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(page)
	if err != nil || len(encoded)+1 > OrchestrationPageBytes || !page.HasMore || page.NextAfter == nil {
		t.Fatal("final wire shape exceeded its byte budget", len(encoded)+1, page.HasMore, err)
	}
	next, err := s.OrchestrationPage(ctx, room.ID, *page.NextAfter, &page.Through, 20)
	if err != nil || next.HasMore || next.NextAfter != nil || len(page.Items)+len(next.Items) != len(records) {
		t.Fatal("byte-bound continuation lost a record", next, err)
	}
}

func TestOrchestrationTaskOperationFailurePreservesQuestionAndReceipts(t *testing.T) {
	for _, action := range []string{"inputs", "answers", "continue", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			task := startTask(t, s, actor, room, writer)
			finishTaskTestRound(t, s, task, task.BaseSharedCommit, CoordinationIntent{CallID: "question", Kind: "ask_person", Question: "Which audience?"})
			task, err := s.GetTask(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			in := TaskOperation{RequestID: "retry-operation", Revision: task.Revision}
			switch action {
			case "inputs", "answers":
				in.Content = "The internal team."
				in.QuestionID = task.Question.ID
			case "continue":
				in.AddTurns, in.AddModelRequests = 1, 2
			}
			before, err := s.queries().GetTask(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, table := range []string{"messages", "runs", "task_inputs", "workspace_events", "task_requests", "orchestration_records", "inbox"} {
				var count int
				if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				counts[table] = count
			}
			if _, err := s.db.Exec("CREATE TRIGGER reject_admission BEFORE INSERT ON orchestration_records BEGIN SELECT RAISE(ABORT, 'forced failure'); END"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.OperateTask(actor, task.ID, action, in); err == nil {
				t.Fatal("accepted a command without its admission evidence")
			}
			after, err := s.queries().GetTask(ctx, task.ID)
			if err != nil || before != after {
				t.Fatal("failed admission changed the task or consumed its question", err)
			}
			for table, count := range counts {
				assertScalar(t, s, "SELECT count(*) FROM "+table, count)
			}
			if _, err := s.db.Exec("DROP TRIGGER reject_admission"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := s.OperateTask(actor, task.ID, action, in); err != nil {
					t.Fatal("the exact request could not recover", err)
				}
			}
			assertScalar(t, s, "SELECT count(*) FROM orchestration_records", counts["orchestration_records"]+1)
		})
	}
}

func TestOrchestrationLegacyJournalSurvivesReopenAndReplay(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "journal.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	actor, _ := teamUsers(t, s)
	room, err := s.SeedRoom(actor, Resource{ID: "files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	in := SendMessageInput{RequestID: "old-journal", Content: "@assistant An explicitly requested job."}
	original, err := s.SendMessage(actor, room.ID, in, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	// Earlier journal rows did not capture request references. Preserve those
	// bytes rather than inferring new historical facts from the surviving work.
	if _, err := s.db.Exec("UPDATE orchestration_records SET value=json_remove(value,'$.trigger.request')"); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.db.QueryRow("SELECT value FROM orchestration_records").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	s = restored
	replayed, err := s.SendMessage(actor, room.ID, in, roomBudget)
	if err != nil || !replayed.Replayed || replayed.Message.ID != original.Message.ID || replayed.Runs[0].ID != original.Runs[0].ID {
		t.Fatal("restart changed the accepted request", replayed, err)
	}
	records := admissionRecords(t, s, room.ID)
	var after string
	if err := s.db.QueryRow("SELECT value FROM orchestration_records").Scan(&after); err != nil || before != after || len(records) != 1 || records[0].Trigger.Request != nil {
		t.Fatal("restart or replay rewrote historical evidence", err)
	}
	in.RequestID = "new-journal"
	if _, err := s.SendMessage(actor, room.ID, in, roomBudget); err != nil {
		t.Fatal(err)
	}
	records = admissionRecords(t, s, room.ID)
	if len(records) != 2 || records[1].Trigger.Request == nil || records[1].Trigger.Request.ID != in.RequestID {
		t.Fatal("new admissions did not capture their receipt", records)
	}
}

func TestOrchestrationFailuresRollBackAdmissionAndWork(t *testing.T) {
	for _, table := range []string{"orchestration_records", "task_requests"} {
		t.Run(table, func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			if _, err := s.db.Exec("CREATE TRIGGER reject_admission BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT, 'forced failure'); END"); err != nil {
				t.Fatal(err)
			}
			_, err := s.CreateTask(actor, room.ID, CreateTaskInput{RequestID: "retry", Goal: "A task.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
			if err == nil {
				t.Fatal("accepted a partial task")
			}
			for _, table := range []string{"messages", "runs", "collaboration_tasks", "orchestration_records", "task_requests"} {
				assertScalar(t, s, "SELECT count(*) FROM "+table, 0)
			}
			if _, err := s.db.Exec("DROP TRIGGER reject_admission"); err != nil {
				t.Fatal(err)
			}
			startTask(t, s, actor, room, writer)
			assertScalar(t, s, "SELECT count(*) FROM orchestration_records", 1)
		})
	}
	for _, table := range []string{"orchestration_records", "workspace_events"} {
		t.Run("message_"+table, func(t *testing.T) {
			s, room, _ := roomFixture(t)
			if _, err := s.db.Exec("CREATE TRIGGER reject_admission BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT, 'forced failure'); END"); err != nil {
				t.Fatal(err)
			}
			// A discussion's final event is written after its admission record.
			if _, err := s.SendMessage(ctx, room.ID, SendMessageInput{RequestID: "post", Content: "A note."}, roomBudget); err == nil {
				t.Fatal("accepted a partial message")
			}
			assertScalar(t, s, "SELECT count(*) FROM messages", 0)
			assertScalar(t, s, "SELECT count(*) FROM orchestration_records", 0)
		})
	}
}

func TestOrchestrationPagingBoundsAndMentionLimit(t *testing.T) {
	s, room, agent := roomFixture(t)
	ids := []string{agent.ID}
	mentions := "@" + agent.Handle
	for i := 1; i < 17; i++ {
		a, err := s.SaveAgent(ctx, Agent{Name: fmt.Sprint("Agent ", i), Handle: fmt.Sprintf("member%d", i), Access: "read", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.JoinRoom(ctx, room.ID, a.ID); err != nil {
			t.Fatal(err)
		}
		mentions += " @" + a.Handle
		if i < 16 {
			ids = append(ids, a.ID)
		}
	}
	_, err := s.MessageRecipientIDs(ctx, room.ID, SendMessageInput{Content: mentions})
	requireCode(t, err, "invalid_message")
	_, err = s.SendMessage(ctx, room.ID, SendMessageInput{RequestID: "too-many", Content: mentions}, roomBudget)
	requireCode(t, err, "invalid_message")
	assertScalar(t, s, "SELECT count(*) FROM orchestration_records", 0)
	for i := range 20 {
		if _, err := s.SendMessage(ctx, room.ID, SendMessageInput{RequestID: fmt.Sprint(i), Content: "Explicit work.", RecipientAgentIDs: ids}, roomBudget); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.OrchestrationPage(ctx, room.ID, 0, nil, 20)
	if err != nil || !page.HasMore || len(page.Items) == 0 || len(page.Items) >= 20 {
		t.Fatal("byte bound was not exercised", page, err)
	}
	through := page.Through
	if _, err := s.SendMessage(ctx, room.ID, SendMessageInput{RequestID: "later", Content: "A later note."}, roomBudget); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for {
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > OrchestrationPageBytes || len(page.Items) > OrchestrationPageLimit {
			t.Fatal("oversized page", len(encoded), len(page.Items))
		}
		for _, record := range page.Items {
			if seen[record.Seq] || record.Seq > through {
				t.Fatal("duplicated or unfrozen record", record.Seq)
			}
			seen[record.Seq] = true
		}
		if !page.HasMore {
			break
		}
		page, err = s.OrchestrationPage(ctx, room.ID, *page.NextAfter, &through, 20)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 20 {
		t.Fatal("lost admission records", len(seen))
	}
	for _, after := range []int64{-1, through + 2} {
		_, err := s.OrchestrationPage(ctx, room.ID, after, &through, 0)
		requireCode(t, err, "invalid_page")
	}
	for _, limit := range []int{-1, 21} {
		_, err := s.OrchestrationPage(ctx, room.ID, 0, nil, limit)
		requireCode(t, err, "invalid_page")
	}
	_, err = s.OrchestrationPage(ctx, "missing", 0, nil, 0)
	requireCode(t, err, "not_found")
}

// seedLegacyTask writes a pre-journal task fixture without calling today's
// admission path against an older schema. Its original Message and Run identities
// remain available to the input-log and orchestration migration tests.
func seedLegacyTask(t *testing.T, s *Store, actor context.Context, room Room, writer Agent) CollaborationTask {
	t.Helper()
	u, _ := UserFromContext(actor)
	stamp := now()
	task := CollaborationTask{ID: newID(), RoomID: room.ID, ResourceID: room.ResourceID, RequestedByUserID: u.ID, Goal: "A historical task.", Status: "queued", Revision: 1, Participants: []TaskParticipant{{Agent: writer, RunnerID: room.RunnerID}}, MaxTurns: 6, MaxModelRequests: 24, Budget: roomBudget, BaseSharedCommit: strings.Repeat("a", 40), CheckpointCommit: strings.Repeat("a", 40), Inputs: []TaskInput{}, Environment: json.RawMessage(`{}`), CreatedAt: stamp, UpdatedAt: stamp}
	task.OriginMessageID = newID()
	task.ThreadRootID = task.OriginMessageID
	if err := s.transaction(actor, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		seq, err := q.NextMessageSequence(actor, room.ID)
		if err != nil {
			return err
		}
		if err := insertMessage(actor, q, Message{ID: task.OriginMessageID, TaskID: task.ID, RoomID: room.ID, Seq: seq, AuthorKind: "user", AuthorUserID: u.ID, AuthorName: u.Name, Content: task.Goal, CreatedAt: stamp, UpdatedAt: stamp}, ""); err != nil {
			return err
		}
		raw, _ := json.Marshal(task)
		if err := q.CreateTask(actor, sqlgen.CreateTaskParams{ID: task.ID, RoomID: room.ID, RequestedByUserID: u.ID, Status: task.Status, Revision: task.Revision, Snapshot: string(raw), CreatedAt: stamp, UpdatedAt: stamp}); err != nil {
			return err
		}
		if err := enqueueTaskRound(actor, q, &task, writer.ID, task.Goal); err != nil {
			return err
		}
		return saveTask(actor, q, &task)
	}); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestOrchestrationMigrationPreservesLegacyWorkWithoutInventingDecisions(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 17)); err != nil {
		t.Fatal(err)
	}
	actor, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, actor, "legacy")
	room, writer := remoteTestRoom(t, s, actor, runner)
	task := seedLegacyTask(t, s, actor, room, writer)
	before, err := s.queries().GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.queries().GetTask(ctx, task.ID)
	if err != nil || before != after {
		t.Fatal("historical task changed", err)
	}
	assertScalar(t, s, "SELECT count(*) FROM messages", 1)
	assertScalar(t, s, "SELECT count(*) FROM runs", 1)
	assertScalar(t, s, "SELECT count(*) FROM orchestration_records", 0)
	if _, err := s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: "new-input", Content: "An explicitly admitted new input."}); err != nil {
		t.Fatal(err)
	}
	records := admissionRecords(t, s, room.ID)
	if len(records) != 1 || records[0].Trigger.Kind != orchestration.TaskInput || len(records[0].Mandates) != 0 {
		t.Fatal(records)
	}
}
