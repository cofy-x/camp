package remoteruns

import (
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestCapturedFilesRecoveryVerifiesImmutableFilesAfterOriginalReceipt(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained", true: "missing"}[missing], func(t *testing.T) {
			manager, store, actor, priorAttempt, _ := proxyFixture(t, "http://127.0.0.1:1")
			prior, err := store.InterruptAttempt(actor, priorAttempt.ID, "Release the isolated proxy fixture.")
			if err != nil {
				t.Fatal(err)
			}
			task, err := manager.CreateTask(actor, prior.RoomID, storage.CreateTaskInput{RequestID: "capture-task", Goal: "Build a notes application.", ParticipantAgentIDs: []string{prior.AgentID}, FirstAgentID: prior.AgentID})
			if err != nil {
				t.Fatal(err)
			}
			run, attempt, err := store.ClaimRemoteRun(actor, task.ActiveRunID, prior.RunnerID, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err = manager.prepare(actor, run, attempt); err != nil {
				t.Fatal(err)
			}
			if _, err = store.AcceptAttempt(actor, attempt.ID, run.RunnerID); err != nil {
				t.Fatal(err)
			}
			commit, err := manager.files.WriteFile(actor, run.ResourceID, run.BaselineCommit, "notes.html", []byte("<main>Partial notes application</main>"))
			if err != nil {
				t.Fatal(err)
			}
			if missing {
				commit = strings.Repeat("f", 40)
			}
			run.Status, run.Error, run.ResultCommit = "failed", "The model reached its observed step limit.", commit
			run.Files = []storage.RunFile{{Path: "notes.html", Status: "added", After: "partial implementation"}}
			if _, err = store.FinishRemoteRun(actor, attempt.ID, run.RunnerID, "capture-complete", strings.Repeat("e", 64), run, "Partial files captured.", false, true); err != nil {
				t.Fatal(err)
			}
			paused, err := store.GetTaskState(actor, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			in := storage.TaskOperation{RequestID: "recover-capture", Revision: paused.Revision, FromRunID: run.ID}
			continued, err := manager.OperateTask(actor, task.ID, "continue", in)
			if missing {
				if errorCode(err) != "invalid_task_recovery" {
					t.Fatal("missing immutable files were authorized as a draft", err)
				}
				current, readErr := store.GetTaskState(actor, task.ID)
				if readErr != nil || current.CheckpointCommit != paused.CheckpointCommit || current.ActiveRunID != "" {
					t.Fatal("failed preflight partially adopted a draft", readErr)
				}
				return
			}
			if err != nil || continued.CheckpointCommit != commit {
				t.Fatal("retained captured files could not be selected explicitly", err)
			}
			// The task baseline has changed, the execution is queued, and the
			// local repository is now unavailable. A lost reply still recovers
			// the original accepted command before any new file preflight.
			manager.files = resources.New(t.TempDir())
			replay, err := manager.OperateTask(actor, task.ID, "continue", in)
			if err != nil || replay.ActiveRunID != continued.ActiveRunID || replay.CheckpointCommit != commit {
				t.Fatal("receipt replay was blocked by later file availability", err)
			}
		})
	}
}
