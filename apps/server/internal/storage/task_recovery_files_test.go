package storage

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func failedCapturedTaskRound(t *testing.T, s *Store, actor context.Context, task CollaborationTask, commit string, cleanup bool) (Run, CollaborationTask) {
	t.Helper()
	run, err := s.GetRun(actor, task.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	run, attempt := prepareTaskTestAttempt(t, s, run)
	if err = s.BeginAttemptModel(actor, attempt.ID, run.RunnerID); err != nil {
		t.Fatal(err)
	}
	if err = s.EndAttemptModel(actor, attempt.ID); err != nil {
		t.Fatal(err)
	}
	run.Status = "failed"
	run.Error = "Execution reached its observed step limit."
	run.ResultCommit = commit
	run.Files = []RunFile{{Path: "src/app.ts", Status: "added", After: "partial implementation"}}
	run.Checks = json.RawMessage(`[{"name":"build","status":"failed"}]`)
	run, err = s.FinishRemoteRun(actor, attempt.ID, run.RunnerID, "captured-"+attempt.ID, strings.Repeat("f", 64), run, "Partial files were captured.", false, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.GetTaskState(actor, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run, task
}

func TestCapturedFilesRecoveryKeepsPartialWorkPrivateAndOriginalReceiptStable(t *testing.T) {
	s, admin, member, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	if _, err := s.OperateTask(member, task.ID, "inputs", TaskOperation{RequestID: "pending-input", Content: "Keep the notes local and keyboard accessible."}); err != nil {
		t.Fatal(err)
	}
	run, paused := failedCapturedTaskRound(t, s, admin, task, strings.Repeat("b", 40), true)
	candidate, err := s.TaskRecoveryCandidate(admin, task.ID)
	if err != nil || candidate == nil || candidate.RunID != run.ID || candidate.FileCount != 1 || paused.CheckpointCommit != task.CheckpointCommit {
		t.Fatal("failed files were promoted automatically or could not be selected explicitly", candidate, err)
	}
	// Shared Room edits do not force captured work to be published or rebased.
	humanCommit := strings.Repeat("c", 40)
	if _, err = s.db.Exec(`UPDATE resources SET current_commit=? WHERE id=?`, humanCommit, room.ResourceID); err != nil {
		t.Fatal(err)
	}
	in := TaskOperation{RequestID: "recover-partial", Revision: paused.Revision, FromRunID: run.ID}
	_, err = s.OperateTask(member, task.ID, "continue", in)
	requireCode(t, err, "forbidden")
	continued, err := s.OperateTask(admin, task.ID, "continue", in)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.GetRun(admin, continued.ActiveRunID)
	if err != nil || next.InputCommit != run.ResultCommit || continued.CheckpointCommit != run.ResultCommit || continued.CheckpointRevision != paused.CheckpointRevision+1 {
		t.Fatal("explicit recovery did not seed the next draft from exact captured files", next, err)
	}
	if continued.BaseSharedCommit != paused.BaseSharedCommit || continued.PreviousDraftCommit != paused.PreviousDraftCommit || continued.PublicationRunID != "" || continued.PublishedCommit != "" ||
		continued.CoveredInputRevision != paused.CoveredInputRevision || continued.ProcessedInputRevision != paused.ProcessedInputRevision || continued.InputRevision != paused.InputRevision ||
		continued.TurnsUsed != paused.TurnsUsed || continued.ModelRequestsUsed != paused.ModelRequestsUsed || continued.MaxTurns != paused.MaxTurns || continued.MaxModelRequests != paused.MaxModelRequests {
		t.Fatal("adopting partial files changed publication, input coverage or allowance", continued)
	}
	resource, err := s.GetResource(admin, room.ResourceID)
	if err != nil || resource.CurrentCommit != humanCommit {
		t.Fatal("recovery changed shared Room files", err)
	}
	old, err := s.GetRun(admin, run.ID)
	if err != nil || !reflect.DeepEqual(old, run) || old.Status != "failed" || old.Published {
		t.Fatal("recovery rewrote the failed execution", err)
	}
	if candidate, err = s.TaskRecoveryCandidate(admin, task.ID); err != nil || candidate != nil {
		t.Fatal("active work still offered the old recovery candidate", candidate, err)
	}
	// The accepted command itself makes the selected baseline stale. Its exact
	// request still resolves first after a lost response, without another Run.
	replay, found, err := s.FindTaskOperation(admin, task.ID, "continue", in)
	if err != nil || !found || replay.ActiveRunID != continued.ActiveRunID {
		t.Fatal("manager preflight could not resolve the original recovery receipt", err)
	}
	replay, err = s.OperateTask(admin, task.ID, "continue", in)
	if err != nil || replay.ActiveRunID != continued.ActiveRunID || replay.CheckpointCommit != continued.CheckpointCommit {
		t.Fatal("uncertain recovery replayed work instead of its receipt", err)
	}
	changed := in
	changed.FromRunID = ""
	_, err = s.OperateTask(admin, task.ID, "continue", changed)
	requireCode(t, err, "request_conflict")
}

func TestCapturedFilesRecoveryRejectsOlderFenceAndOtherTask(t *testing.T) {
	s, admin, _, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	first, paused := failedCapturedTaskRound(t, s, admin, task, strings.Repeat("b", 40), true)
	continued, err := s.OperateTask(admin, task.ID, "continue", TaskOperation{RequestID: "original-draft", Revision: paused.Revision})
	if err != nil || continued.CheckpointCommit != task.CheckpointCommit {
		t.Fatal("ordinary continuation silently selected partial files", err)
	}
	second, paused := failedCapturedTaskRound(t, s, admin, continued, strings.Repeat("c", 40), true)
	_, err = s.OperateTask(admin, task.ID, "continue", TaskOperation{RequestID: "old-fence", Revision: paused.Revision, FromRunID: first.ID})
	requireCode(t, err, "invalid_task_recovery")
	candidate, err := s.TaskRecoveryCandidate(admin, task.ID)
	if err != nil || candidate == nil || candidate.RunID != second.ID {
		t.Fatal("candidate selection did not follow the current draft fence", candidate, err)
	}
	_, err = s.OperateTask(admin, task.ID, "continue", TaskOperation{RequestID: "two-baselines", Revision: paused.Revision, FromRunID: second.ID, FromLatest: true})
	requireCode(t, err, "invalid_task_recovery")
	other := startTask(t, s, admin, room, writer, reviewer)
	_, other = failedCapturedTaskRound(t, s, admin, other, strings.Repeat("d", 40), true)
	_, err = s.OperateTask(admin, other.ID, "continue", TaskOperation{RequestID: "wrong-task", Revision: other.Revision, FromRunID: second.ID})
	requireCode(t, err, "invalid_task_recovery")
	_, err = s.OperateTask(admin, task.ID, "continue", TaskOperation{RequestID: "stale-revision", Revision: paused.Revision - 1, FromRunID: second.ID})
	requireCode(t, err, "revision_conflict")
}

func TestCapturedFilesRecoveryRequiresCommittedCleanupAndCurrentAuthority(t *testing.T) {
	t.Run("unconfirmed cleanup", func(t *testing.T) {
		s, admin, _, room, writer, reviewer := taskFixture(t)
		task := startTask(t, s, admin, room, writer, reviewer)
		run, paused := failedCapturedTaskRound(t, s, admin, task, strings.Repeat("b", 40), false)
		if _, err := s.RecoveryRunFiles(admin, paused.ID, run.ID); err == nil {
			t.Fatal("unconfirmed cleanup qualified captured files")
		}
		if candidate, err := s.TaskRecoveryCandidate(admin, paused.ID); err != nil || candidate != nil {
			t.Fatal("unsafe cleanup became a recovery option", candidate, err)
		}
	})
	t.Run("revoked authority with exhausted allowance", func(t *testing.T) {
		s, admin, _, room, writer, reviewer := taskFixture(t)
		task := startTask(t, s, admin, room, writer, reviewer)
		run, paused := failedCapturedTaskRound(t, s, admin, task, strings.Repeat("b", 40), true)
		if _, err := s.db.Exec(`UPDATE collaboration_tasks SET snapshot=json_set(snapshot,'$.maxTurns',?,'$.maxModelRequests',?) WHERE id=?`, paused.TurnsUsed, paused.ModelRequestsUsed, task.ID); err != nil {
			t.Fatal(err)
		}
		writer.Access = "read"
		if _, err := s.SaveAgent(admin, writer); err != nil {
			t.Fatal(err)
		}
		_, err := s.OperateTask(admin, task.ID, "continue", TaskOperation{RequestID: "revoked-recovery", Revision: paused.Revision, FromRunID: run.ID})
		if err == nil {
			t.Fatal("exhausted allowance bypassed current authority during draft adoption")
		}
		current, readErr := s.GetTaskState(admin, task.ID)
		if readErr != nil || current.CheckpointCommit != paused.CheckpointCommit || current.CheckpointRevision != paused.CheckpointRevision {
			t.Fatal("rejected recovery partially adopted files", readErr)
		}
	})
}
