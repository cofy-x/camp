package storage

import (
	"testing"
)

func TestAutomationExplicitRunStillFansOutContextWithoutAnotherAuthorization(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	first := startTask(t, s, actor, room, writer)
	second := startTask(t, s, actor, room, writer)
	for _, task := range []CollaborationTask{first, second} {
		subscribeTaskTest(t, s, actor, task, "room", room.ID+":room")
	}
	enableTaskInputs(t, s, first.Participants[0].RunnerID)
	run, err := s.GetRun(ctx, first.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	run, attempt := prepareTaskTestAttempt(t, s, run)
	if run.TaskInputRevision != 0 {
		t.Fatal("fixture did not freeze the attempt before intake", run.TaskInputRevision)
	}
	message := sendAutomationDiscussion(t, s, actor, room, "@"+writer.Handle+" prepare a separate explicit result.")
	if len(message.Runs) != 1 {
		t.Fatal("the explicit request did not retain exactly one ordinary Run", message.Runs)
	}
	var delivered []TaskInput
	for _, task := range []CollaborationTask{first, second} {
		page, err := s.TaskInputsPage(actor, task.ID, 0, nil, 20)
		if err != nil || len(page.Items) != 1 {
			t.Fatal("explicit request was dropped from a followed conversation", task.ID, page, err)
		}
		input := page.Items[0]
		if input.MessageID != message.Message.ID || input.SourceEventID != "room:"+message.Message.ID || input.Content != message.Message.Content || input.AuthorUserID != message.Message.AuthorUserID || input.AuthorKind != "user" {
			t.Fatal("explicit context lost its original identity or attribution", input)
		}
		delivered = append(delivered, input)
	}
	if delivered[0].ID == delivered[1].ID {
		t.Fatal("explicit request reused the same task-local input identity")
	}
	stored, err := s.GetRun(ctx, run.ID)
	if err != nil || stored.TaskInputRevision != run.TaskInputRevision || stored.TaskInputAfter != run.TaskInputAfter {
		t.Fatal("later fan-out changed the executing attempt's frozen input range", stored, err)
	}
	token, _, err := s.TaskReadCapability(ctx, attempt.ID, run.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := s.ReadAttemptTaskInputs(ctx, attempt.ID, token, 0, 20)
	if err != nil || len(frozen.Items) != 0 || frozen.Through != 0 {
		t.Fatal("an already frozen attempt read the later explicit request", frozen, err)
	}
	// Even a broader message cutoff must not smuggle a pending original into
	// the old input frontier through general Room history.
	history, err := s.TaskContextMessages(ctx, room.ID, "", 0, message.Message.Seq, first.ID, run.TaskInputRevision)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range history {
		if item.ID == message.Message.ID {
			t.Fatal("pending explicit context bypassed the frozen input frontier")
		}
	}
	event, err := s.queries().GetAutomationEvent(ctx, "room:"+message.Message.ID)
	if err != nil || event.Status != "accepted" || event.Reason != "explicit_run" {
		t.Fatal("the explicit Run became a pending automatic decision", event, err)
	}
	if candidate, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); err != nil || candidate != nil {
		t.Fatal("explicit Run intake admitted an additional automatic assessment", candidate, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 2)
	assertScalar(t, s, "SELECT count(*) FROM runs", 3)
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 0)
	posted, err := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: newID(), Content: "@" + writer.Handle + " post-only discussion.", PostOnly: true}, roomBudget)
	if err != nil || len(posted.Runs) != 0 {
		t.Fatal(posted, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM task_inputs", 2)
}
