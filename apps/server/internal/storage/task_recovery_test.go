package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskPendingPublicationSurvivesDatabaseReopen(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	s, e := Open(filename)
	if e != nil {
		t.Fatal(e)
	}
	admin, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, admin, "alpha")
	room, writer := remoteTestRoom(t, s, admin, runner)
	task := startTask(t, s, admin, room, writer)
	run, a := finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "done", Kind: "finish", Summary: "Ready for review."})
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(filename)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	pending, e := s.GetTask(ctx, task.ID)
	if e != nil || pending.Status != "publishing" || pending.ActiveAttemptID != "" {
		t.Fatal(pending, e)
	}
	if e = s.ReconcileTask(ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	// Completion replay is answered by its original receipt after publication.
	replay, e := s.FinishRemoteRun(ctx, a.ID, runner.ID, "complete-"+a.ID, strings.Repeat("f", 64), run, "lost response", true, true)
	if e != nil || replay.ID != run.ID {
		t.Fatal(replay, e)
	}
	if e = s.ReconcileTask(ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	current, e := s.GetTask(ctx, task.ID)
	if e != nil || current.Status != "completed" || current.PublishedCommit != run.ResultCommit {
		t.Fatal(current, e)
	}
	inbox, e := s.ListInbox(admin)
	if e != nil || len(inbox) != 1 {
		t.Fatal(inbox, e)
	}
}
func TestTwoTasksPublishOnlyOneSharedBaseline(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	first := startTask(t, s, admin, room, writer)
	second := startTask(t, s, admin, room, writer)
	finishTaskTestRound(t, s, first, strings.Repeat("b", 40), CoordinationIntent{CallID: "first", Kind: "finish", Summary: "First draft."})
	finishTaskTestRound(t, s, second, strings.Repeat("c", 40), CoordinationIntent{CallID: "second", Kind: "finish", Summary: "Second draft."})
	if e := s.ReconcileTask(ctx, first.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.ReconcileTask(ctx, second.ID); e != nil {
		t.Fatal(e)
	}
	first, _ = s.GetTask(ctx, first.ID)
	second, _ = s.GetTask(ctx, second.ID)
	if first.Status != "completed" || second.Status != "needs_attention" || second.CheckpointCommit != strings.Repeat("c", 40) {
		t.Fatal(first, second)
	}
	publications, e := s.ListPublications(ctx, room.ResourceID, 0)
	if e != nil || len(publications) != 1 || publications[0].TaskID != first.ID {
		t.Fatal(publications, e)
	}
}
