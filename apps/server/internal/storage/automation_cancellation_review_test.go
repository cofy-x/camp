package storage

import (
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestAutomationCancellationPreservesAnotherTasksPendingThreadRequest(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	first := startTask(t, s, actor, room, writer)
	second := startTask(t, s, actor, room, writer)
	for _, task := range []CollaborationTask{first, second} {
		subscribeTaskTest(t, s, actor, task, "room", room.ID+":room")
	}
	request := sendAutomationDiscussion(t, s, actor, room, "Start a separate third task to prepare a different document.")
	before, err := s.GetTaskState(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err = s.GetTaskState(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OperateTask(actor, first.ID, "cancel", TaskOperation{RequestID: newID(), Revision: first.Revision, InputProtocol: 1, PagedResponse: true}); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget)
	if err != nil || candidate == nil || len(candidate.Events) != 1 || candidate.Events[0].ID != "room:"+request.Message.ID {
		t.Fatal("cancelling one follower consumed another active follower's independent pending request", candidate, err)
	}
	if len(candidate.Tasks) != 2 {
		t.Fatal("assessment lost related active and cancelled goals", candidate.Tasks)
	}
	for _, task := range candidate.Tasks {
		if (task.ID == first.ID && task.ReceivesInputs) || (task.ID == second.ID && !task.ReceivesInputs) {
			t.Fatal("cancellation changed the other task's routing facts", candidate.Tasks)
		}
	}
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "accept", Goal: "Prepare the separate third document.", Reason: "This is new work outside the two existing goals."}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListRoomTaskStates(ctx, room.ID)
	if err != nil || len(tasks) != 3 {
		t.Fatal("the independent third request was lost", tasks, err)
	}
	after, err := s.GetTaskState(ctx, second.ID)
	if err != nil || after.Revision != before.Revision || after.ActiveRunID != before.ActiveRunID || after.Status != before.Status || after.InputRevision != 1 {
		t.Fatal("cancelling the first task changed the second task", before, after, err)
	}
}

func TestAutomationCancellationInvalidatesRunningAssessmentWithAnotherFollower(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	first := startTask(t, s, actor, room, writer)
	second := startTask(t, s, actor, room, writer)
	for _, task := range []CollaborationTask{first, second} {
		subscribeTaskTest(t, s, actor, task, "room", room.ID+":room")
	}
	sendAutomationDiscussion(t, s, actor, room, "A request evaluated with both tasks still active.")
	candidate := claimAutomation(t, s)
	first, err := s.GetTaskState(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OperateTask(actor, first.ID, "cancel", TaskOperation{RequestID: newID(), Revision: first.Revision, InputProtocol: 1, PagedResponse: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "accept", Goal: "A stale third goal.", Reason: "Decided before the cancellation."}); err != nil {
		t.Fatal(err)
	}
	row, err := s.queries().GetAutomationEvaluation(ctx, candidate.ID)
	if err != nil || row.Status != "needs_attention" {
		t.Fatal("changed task routing did not invalidate the running assessment", row.Status, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 2)
	request := sendAutomationDiscussion(t, s, actor, room, "A new independent request after the cancellation.")
	next := claimAutomation(t, s)
	if len(next.Events) != 1 || next.Events[0].ID != "room:"+request.Message.ID {
		t.Fatal("new intake replayed the old assessment or lost fresh authorization", next.Events)
	}
	if err = s.CompleteAutomationEvaluation(ctx, next.ID, orchestration.Assessment{Outcome: "accept", Goal: "A fresh third goal.", Reason: "Authorized after cancellation."}); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 3)
}

func TestAutomationCancellationKeepsCutoffWhenOtherSubscriberHasFinished(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	second := startTask(t, s, actor, room, writer)
	first := startTask(t, s, actor, room, writer)
	for _, task := range []CollaborationTask{first, second} {
		subscribeTaskTest(t, s, actor, task, "room", room.ID+":room")
	}
	enableTaskInputs(t, s, second.Participants[0].RunnerID)
	finishTaskTestRound(t, s, second, "", CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "The second goal is complete."})
	if err := s.ReconcileTask(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	second, err := s.GetTaskState(ctx, second.ID)
	if err != nil || second.Status != "completed" {
		t.Fatal(second, err)
	}
	sendAutomationDiscussion(t, s, actor, room, "Old context before the last active task is cancelled.")
	first, err = s.GetTaskState(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OperateTask(actor, first.ID, "cancel", TaskOperation{RequestID: newID(), Revision: first.Revision, InputProtocol: 1, PagedResponse: true}); err != nil {
		t.Fatal(err)
	}
	if candidate, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); err != nil || candidate != nil {
		t.Fatal("a finished subscriber kept the cancelled lane's old backlog runnable", candidate, err)
	}
	request := sendAutomationDiscussion(t, s, actor, room, "A fresh request after the cancellation boundary.")
	next := claimAutomation(t, s)
	if len(next.Events) != 1 || next.Events[0].ID != "room:"+request.Message.ID {
		t.Fatal("the cancellation boundary suppressed a later request", next.Events)
	}
}
