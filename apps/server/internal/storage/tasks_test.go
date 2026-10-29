package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func taskFixture(t *testing.T) (*Store, context.Context, context.Context, Room, Agent, Agent) {
	t.Helper()
	s := testStore(t)
	admin, member := teamUsers(t, s)
	a, _ := enrollTestRunner(t, s, admin, "alpha")
	b, _ := enrollTestRunner(t, s, member, "bravo")
	room, writer := remoteTestRoom(t, s, admin, a)
	reviewer, e := s.CreateRoomAgent(member, room.ID, Agent{Name: "Reviewer", Handle: "reviewer", Access: "read", Enabled: true, PreferredRunnerID: b.ID})
	if e != nil {
		t.Fatal(e)
	}
	return s, admin, member, room, writer, reviewer
}
func startTask(t *testing.T, s *Store, actor context.Context, room Room, agents ...Agent) CollaborationTask {
	t.Helper()
	ids := []string{}
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	task, e := s.CreateTask(actor, room.ID, CreateTaskInput{RequestID: newID(), Goal: "Prepare and review a useful document.", ParticipantAgentIDs: ids, FirstAgentID: agents[0].ID}, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	return task
}
func finishTaskTestRound(t *testing.T, s *Store, task CollaborationTask, commit string, intent CoordinationIntent) (Run, Attempt) {
	t.Helper()
	r, e := s.GetRun(ctx, task.ActiveRunID)
	if e != nil {
		t.Fatal(e)
	}
	r, a := prepareTaskTestAttempt(t, s, r)
	r.Status = "succeeded"
	r.ResultCommit = commit
	r.Intent = &intent
	out, e := s.FinishRemoteRun(ctx, a.ID, r.RunnerID, "complete-"+a.ID, strings.Repeat("f", 64), r, "Round completed.", true, true)
	if e != nil {
		t.Fatal(e)
	}
	return out, a
}
func TestTaskHandoffDraftAndIndependentPublication(t *testing.T) {
	s, admin, _, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	original := task.BaseSharedCommit
	first, a := finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "call-1", Kind: "handoff", TargetAgentID: reviewer.ID, Instruction: "Review the draft."})
	task, e := s.GetTask(ctx, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	if task.Status != "queued" || task.CurrentAgentID != reviewer.ID || task.TurnsUsed != 1 {
		t.Fatal(task)
	}
	resource, _ := s.GetResource(ctx, room.ResourceID)
	if resource.CurrentCommit != original || first.Published {
		t.Fatal("intermediate round published")
	}
	next, e := s.GetRun(ctx, task.ActiveRunID)
	if e != nil || next.InputCommit != first.ResultCommit || next.RunnerID == first.RunnerID {
		t.Fatal("handoff did not cross fixed runners", next, e)
	}
	replay, e := s.FinishRemoteRun(ctx, a.ID, first.RunnerID, "complete-"+a.ID, strings.Repeat("f", 64), first, "duplicate", true, true)
	if e != nil || replay.ID != first.ID {
		t.Fatal(e)
	}
	runs, _ := s.ListTaskRuns(ctx, task.ID)
	if len(runs) != 2 {
		t.Fatal("duplicate handoff", len(runs))
	}
	// Read-only review can hand work back, but cannot publish changed drafts.
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: "call-2", Kind: "handoff", TargetAgentID: writer.ID, Instruction: "Review passed. Run final checks and finish."})
	task, _ = s.GetTask(ctx, task.ID)
	last, _ := finishTaskTestRound(t, s, task, strings.Repeat("c", 40), CoordinationIntent{CallID: "call-3", Kind: "finish", Summary: "Document ready for human review."})
	task, _ = s.GetTask(ctx, task.ID)
	if task.Status != "publishing" || task.ActiveRunID != "" {
		t.Fatal(task)
	}
	// Publication succeeds without runner participation and is repeatable.
	for range 2 {
		if e = s.ReconcileTask(ctx, task.ID); e != nil {
			t.Fatal(e)
		}
	}
	task, _ = s.GetTask(ctx, task.ID)
	resource, _ = s.GetResource(ctx, room.ResourceID)
	if task.Status != "completed" || resource.CurrentCommit != last.ResultCommit {
		t.Fatal(task, resource)
	}
	inbox, _ := s.ListInbox(admin)
	if len(inbox) != 1 || inbox[0].Kind != "task_finished" || inbox[0].TaskID != task.ID {
		t.Fatal("intermediate notifications leaked", inbox)
	}
	publications, _ := s.ListPublications(ctx, room.ResourceID, 0)
	if len(publications) != 1 {
		t.Fatal("duplicate publication", publications)
	}
}
func TestTaskWaitingQuestionFirstAnswerAndFiniteBudget(t *testing.T) {
	s, admin, member, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "ask", Kind: "ask_person", Question: "Which title?", Options: []string{"A", "B"}})
	task, _ = s.GetTask(ctx, task.ID)
	if task.Status != "waiting_for_person" || task.ActiveRunID != "" || task.ActiveAttemptID != "" {
		t.Fatal(task)
	}
	if _, e := s.AcquireResourceLease(ctx, room.ResourceID, "human", time.Now().Add(time.Minute)); e != nil {
		t.Fatal("wait holds shared writer", e)
	}
	_, e := s.OperateTask(member, task.ID, "cancel", TaskOperation{RequestID: "forbidden", Revision: task.Revision})
	requireCode(t, e, "forbidden")
	answer := TaskOperation{RequestID: "answer", Revision: task.Revision, QuestionID: task.Question.ID, Content: "A"}
	answered, e := s.OperateTask(member, task.ID, "answers", answer)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.OperateTask(member, task.ID, "answers", answer)
	if e != nil || replay.ActiveRunID != answered.ActiveRunID {
		t.Fatal(e)
	}
	answer.RequestID = "second"
	answer.Revision = answered.Revision
	_, e = s.OperateTask(admin, task.ID, "answers", answer)
	requireCode(t, e, "question_answered")
	r, _ := s.GetRun(ctx, answered.ActiveRunID)
	r, a := prepareTaskTestAttempt(t, s, r)
	for range answered.MaxModelRequests {
		if e = s.BeginAttemptModel(ctx, a.ID, r.RunnerID); e != nil {
			t.Fatal(e)
		}
		if e = s.EndAttemptModel(ctx, a.ID); e != nil {
			t.Fatal(e)
		}
	}
	requireCode(t, s.BeginAttemptModel(ctx, a.ID, r.RunnerID), "task_budget_exhausted")
	charged, _ := s.GetTask(ctx, task.ID)
	if charged.ModelRequestsUsed != charged.MaxModelRequests {
		t.Fatal(charged)
	}
	if _, e = s.InterruptAttempt(ctx, a.ID, "Model request allowance exhausted."); e != nil {
		t.Fatal(e)
	}
	paused, _ := s.GetTask(ctx, task.ID)
	if paused.Status != "needs_attention" || paused.ActiveRunID != "" {
		t.Fatal(paused)
	}
}
func TestTaskDraftDoesNotBlockHumanAndConflictPreservesDraft(t *testing.T) {
	s, admin, _, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	r, _ := s.GetRun(ctx, task.ActiveRunID)
	r, a := prepareTaskTestAttempt(t, s, r)
	humanCommit := strings.Repeat("d", 40)
	_, e := s.Publish(admin, PublishInput{ResourceID: room.ResourceID, ExpectedCommit: task.BaseSharedCommit, Commit: humanCommit, Kind: "edit", RequestID: "human", RequestHash: strings.Repeat("e", 64)})
	if e != nil {
		t.Fatal("task blocks human writer", e)
	}
	r.Status = "succeeded"
	r.ResultCommit = strings.Repeat("b", 40)
	r.Intent = &CoordinationIntent{CallID: "finish", Kind: "finish", Summary: "Ready"}
	if _, e = s.FinishRemoteRun(ctx, a.ID, r.RunnerID, "done", strings.Repeat("f", 64), r, "Ready", true, true); e != nil {
		t.Fatal(e)
	}
	if e = s.ReconcileTask(ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	task, _ = s.GetTask(ctx, task.ID)
	if task.Status != "needs_attention" || task.CheckpointCommit != r.ResultCommit {
		t.Fatal(task)
	}
	resource, _ := s.GetResource(ctx, room.ResourceID)
	if resource.CurrentCommit != humanCommit {
		t.Fatal("human files overwritten")
	}
	continued, e := s.OperateTask(admin, task.ID, "continue", TaskOperation{RequestID: "latest", Revision: task.Revision, AgentID: writer.ID, FromLatest: true})
	if e != nil {
		t.Fatal(e)
	}
	if continued.CheckpointCommit != humanCommit || continued.PreviousDraftCommit != r.ResultCommit || continued.BaseSharedCommit != humanCommit {
		t.Fatal(continued)
	}
}
func TestTaskCancelWinsAndLateInputRequiresAnotherRound(t *testing.T) {
	s, admin, member, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	r, _ := s.GetRun(ctx, task.ActiveRunID)
	r, a := prepareTaskTestAttempt(t, s, r)
	task, e := s.OperateTask(member, task.ID, "inputs", TaskOperation{RequestID: "more", Content: "Add a recovery section."})
	if e != nil {
		t.Fatal(e)
	}
	r.Status = "succeeded"
	r.ResultCommit = strings.Repeat("b", 40)
	r.Intent = &CoordinationIntent{CallID: "finish", Kind: "finish", Summary: "Ready"}
	if _, e = s.FinishRemoteRun(ctx, a.ID, r.RunnerID, "done", strings.Repeat("f", 64), r, "Ready", true, true); e != nil {
		t.Fatal(e)
	}
	task, _ = s.GetTask(ctx, task.ID)
	if task.Status != "queued" || task.ActiveRunID == r.ID {
		t.Fatal("unprocessed input allowed finish", task)
	}
	r, _ = s.GetRun(ctx, task.ActiveRunID)
	r, a = prepareTaskTestAttempt(t, s, r)
	task, _ = s.GetTask(ctx, task.ID)
	task, e = s.OperateTask(admin, task.ID, "cancel", TaskOperation{RequestID: "stop", Revision: task.Revision})
	if e != nil {
		t.Fatal(e)
	}
	requireCode(t, s.BeginAttemptModel(ctx, a.ID, r.RunnerID), "run_cancelled")
	r.Status = "succeeded"
	r.ResultCommit = strings.Repeat("c", 40)
	r.Intent = &CoordinationIntent{CallID: "late", Kind: "handoff", TargetAgentID: reviewer.ID, Instruction: "Do not dispatch"}
	if _, e = s.FinishRemoteRun(ctx, a.ID, r.RunnerID, "late", strings.Repeat("e", 64), r, "late", true, true); e != nil {
		t.Fatal(e)
	}
	task, _ = s.GetTask(ctx, task.ID)
	runs, _ := s.ListTaskRuns(ctx, task.ID)
	if task.Status != "cancelled" || task.ActiveRunID != "" || len(runs) != 2 {
		t.Fatal(task, len(runs))
	}
}

func prepareTaskTestAttempt(t *testing.T, s *Store, r Run) (Run, Attempt) {
	t.Helper()
	r, a, e := s.ClaimRemoteRun(ctx, r.ID, r.RunnerID, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	command, _ := json.Marshal(map[string]string{"id": a.CommandID, "operationId": a.ID, "kind": "run.start"})
	if e = s.SaveAttemptCommand(ctx, a.ID, command, remoteHash([]byte(a.ID))); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptAttempt(ctx, a.ID, r.RunnerID); e != nil {
		t.Fatal(e)
	}
	return r, a
}

func TestTaskWaitingRevocationIsStickyAndSessionsAreIsolated(t *testing.T) {
	s, admin, _, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	r, _ := s.GetRun(ctx, task.ActiveRunID)
	r, a := prepareTaskTestAttempt(t, s, r)
	taskConv, e := s.GetConversation(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	ordinary := r
	ordinary.TaskID = ""
	chatConv, e := s.GetConversation(ctx, ordinary)
	if e != nil {
		t.Fatal(e)
	}
	if taskConv.ID == chatConv.ID || taskConv.SessionID == chatConv.SessionID {
		t.Fatal("task shared native session with chat")
	}
	r.Status = "succeeded"
	r.Intent = &CoordinationIntent{CallID: "ask", Kind: "ask_person", Question: "Approve title?"}
	r.ResultCommit = strings.Repeat("b", 40)
	if _, e = s.FinishRemoteRun(ctx, a.ID, r.RunnerID, "done", strings.Repeat("f", 64), r, "Question", true, true); e != nil {
		t.Fatal(e)
	}
	if e = s.LeaveRoom(admin, room.ID, reviewer.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.JoinRoom(admin, room.ID, reviewer.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.ReconcileTask(ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	task, _ = s.GetTask(ctx, task.ID)
	if task.Status != "needs_attention" || task.ActiveRunID != "" {
		t.Fatal("regrant revived waiting task", task)
	}
	_, e = s.OperateTask(admin, task.ID, "answers", TaskOperation{RequestID: "stale-answer", Revision: task.Revision, QuestionID: task.Question.ID, Content: "Yes"})
	requireCode(t, e, "question_answered")
}

func TestTaskBudgetPauseRetainsApprovedHandoffTargetAndInstruction(t *testing.T) {
	s, admin, _, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	for turn := 1; turn < task.MaxTurns; turn++ {
		finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "self", Kind: "handoff", TargetAgentID: writer.ID, Instruction: "Keep drafting."})
		task, _ = s.GetTask(ctx, task.ID)
	}
	const instruction = "Review the draft for missing recovery instructions."
	finishTaskTestRound(t, s, task, strings.Repeat("c", 40), CoordinationIntent{CallID: "review", Kind: "handoff", TargetAgentID: reviewer.ID, Instruction: instruction})
	task, _ = s.GetTask(ctx, task.ID)
	if task.Status != "needs_attention" || task.CurrentAgentID != reviewer.ID || task.Handoff != instruction || task.ActiveRunID != "" {
		t.Fatal("quota pause lost approved handoff", task)
	}
	resumed, err := s.OperateTask(admin, task.ID, "continue", TaskOperation{RequestID: "more-budget", Revision: task.Revision, AddTurns: 1})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.GetRun(ctx, resumed.ActiveRunID)
	if err != nil || run.AgentID != reviewer.ID || run.Prompt != instruction {
		t.Fatal("continuation changed pending step", run, err)
	}
}
func TestTaskHandoffDatabaseFailureRollsBackCompletionAndRetriesSameReceipt(t *testing.T) {
	s, admin, _, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	run, err := s.GetRun(ctx, task.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	run, attempt := prepareTaskTestAttempt(t, s, run)
	beforeRoom, _ := s.GetRoom(ctx, room.ID)
	if _, err = s.db.Exec(`CREATE TRIGGER reject_next_round BEFORE INSERT ON runs BEGIN SELECT RAISE(ABORT,'injected queue failure'); END;`); err != nil {
		t.Fatal(err)
	}
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	run.Intent = &CoordinationIntent{CallID: "handoff", Kind: "handoff", TargetAgentID: reviewer.ID, Instruction: "Review."}
	_, err = s.FinishRemoteRun(ctx, attempt.ID, run.RunnerID, "completion", strings.Repeat("f", 64), run, "Done", true, true)
	if err == nil {
		t.Fatal("database failure became committed task attention")
	}
	current, _ := s.GetTask(ctx, task.ID)
	savedRun, _ := s.GetRun(ctx, run.ID)
	savedAttempt, _ := s.GetAttempt(ctx, attempt.ID)
	currentRoom, _ := s.GetRoom(ctx, room.ID)
	if current.ActiveRunID != run.ID || runTerminal(savedRun.Status) || savedAttempt.CompletionID != "" || currentRoom.LastMessageSeq != beforeRoom.LastMessageSeq {
		t.Fatal("partial completion survived transaction rollback", current, savedRun, savedAttempt)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_next_round`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishRemoteRun(ctx, attempt.ID, run.RunnerID, "completion", strings.Repeat("f", 64), run, "Done", true, true); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetTask(ctx, task.ID)
	if current.Status != "queued" || current.CurrentAgentID != reviewer.ID {
		t.Fatal(current)
	}
	rounds, _ := s.ListTaskRuns(ctx, task.ID)
	if len(rounds) != 2 {
		t.Fatal("retry repeated handoff", rounds)
	}
}

func TestTaskPublicationRollsBackIfWriterLeaseCannotRelease(t *testing.T) {
	s, admin, _, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "finish", Kind: "finish", Summary: "Ready."})
	if _, err := s.db.Exec(`CREATE TRIGGER reject_task_lease_release BEFORE UPDATE ON resource_writer_leases WHEN NEW.owner='' BEGIN SELECT RAISE(ABORT,'injected release failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileTask(ctx, task.ID); err == nil {
		t.Fatal("publication committed despite failed lease release")
	}
	current, _ := s.GetTask(ctx, task.ID)
	resource, _ := s.GetResource(ctx, room.ResourceID)
	if current.Status != "publishing" || resource.CurrentCommit != task.BaseSharedCommit {
		t.Fatal("publication escaped rollback", current, resource)
	}
	publications, _ := s.ListPublications(ctx, room.ResourceID, 0)
	if len(publications) != 0 {
		t.Fatal(publications)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_task_lease_release`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetTask(ctx, task.ID)
	if current.Status != "completed" {
		t.Fatal(current)
	}
	if _, err := s.AcquireResourceLease(ctx, room.ResourceID, "human", time.Now().Add(time.Minute)); err != nil {
		t.Fatal("completed task retained writer lease", err)
	}
}

func TestTaskInputsBeforePublicationUseClaimedContextSnapshot(t *testing.T) {
	s, admin, member, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "finish", Kind: "finish", Summary: "Ready."})
	task, err := s.OperateTask(member, task.ID, "inputs", TaskOperation{RequestID: "before-publication", Content: "Include a concrete recovery procedure."})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	task, _ = s.GetTask(ctx, task.ID)
	if task.Status != "queued" || task.ActiveRunID == "" {
		t.Fatal("pending input was published without processing", task)
	}
	resource, _ := s.GetResource(ctx, room.ResourceID)
	if resource.CurrentCommit != task.BaseSharedCommit {
		t.Fatal("early publication", resource)
	}
	run, _ := s.GetRun(ctx, task.ActiveRunID)
	run, _ = prepareTaskTestAttempt(t, s, run)
	task, err = s.OperateTask(member, task.ID, "inputs", TaskOperation{RequestID: "after-claim", Content: "This later input must wait for another round."})
	if err != nil {
		t.Fatal(err)
	}
	brief := task.TaskBrief(run)
	if !strings.Contains(brief, "concrete recovery procedure") || strings.Contains(brief, "later input must wait") || run.TaskInputRevision != 1 {
		t.Fatal("task context crossed claimed input revision", brief, run.TaskInputRevision)
	}
	messages, err := s.ContextMessages(ctx, run.RoomID, run.ThreadRootID, 0, run.ContextThroughSeq)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "later input must wait") {
			t.Fatal("late message leaked into claimed conversation context")
		}
	}
}
func TestTaskPublicationRevocationIsStickyAndReaderCompletionDoesNotWrite(t *testing.T) {
	t.Run("revoke before publication", func(t *testing.T) {
		s, admin, _, room, writer, reviewer := taskFixture(t)
		task := startTask(t, s, admin, room, writer, reviewer)
		finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "finish", Kind: "finish", Summary: "Ready."})
		reviewer.Enabled = false
		reviewer, err := s.SaveAgent(admin, reviewer)
		if err != nil {
			t.Fatal(err)
		}
		reviewer.Enabled = true
		if _, err = s.SaveAgent(admin, reviewer); err != nil {
			t.Fatal(err)
		}
		if err = s.ReconcileTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
		current, _ := s.GetTask(ctx, task.ID)
		resource, _ := s.GetResource(ctx, room.ResourceID)
		if current.Status != "needs_attention" || resource.CurrentCommit != task.BaseSharedCommit {
			t.Fatal("restored grant published a revoked task", current, resource)
		}
	})
	t.Run("reader keeps concurrent human edit", func(t *testing.T) {
		s, admin, _, room, _, reader := taskFixture(t)
		task := startTask(t, s, admin, room, reader)
		finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: "read-only-finish", Kind: "finish", Summary: "Reviewed the supplied snapshot."})
		_, err := s.Publish(admin, PublishInput{ResourceID: room.ResourceID, ExpectedCommit: task.BaseSharedCommit, Commit: strings.Repeat("e", 40), Kind: "edit", RequestID: "human", RequestHash: strings.Repeat("f", 64)})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ReconcileTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
		current, _ := s.GetTask(ctx, task.ID)
		resource, _ := s.GetResource(ctx, room.ResourceID)
		if current.Status != "completed" || resource.CurrentCommit != strings.Repeat("e", 40) {
			t.Fatal("reader completion changed shared files", current, resource)
		}
		publications, _ := s.ListPublications(ctx, room.ResourceID, 0)
		if len(publications) != 1 || publications[0].Kind != "edit" {
			t.Fatal("reader created a write publication", publications)
		}
	})
}
