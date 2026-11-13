package remoteruns

import (
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestRemoteAutomaticWorkPublishesAndCompletesAcrossLostReplyAndRestart(t *testing.T) {
	h := newRemoteHarness(t, configuration.ModelInput{})
	runner := h.start(t, h.join(t, "completion-fixture", "fixture", nil, nativeexecutor.Config{}))
	room, writer := h.room(t, "Authorized completion", runner.config.RunnerID)
	for _, mode := range []string{"automatic", "human"} {
		request := orchestration.WorkItemInput{RequestID: "completion-" + mode, Goal: "[fixture:finish] Prepare the saved result", CompletionMode: mode, Start: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
		h.lostResponse.Store(true)
		work, err := h.manager.Load().CreateWorkItem(h.actor, room.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		taskID := work.ActiveTaskID
		task := h.taskState(t, taskID, "completed")
		remoteEventually(t, 10*time.Second, "parent Work did not accept the published result", func() bool {
			current, readErr := h.store.GetWorkItem(h.actor, work.ID)
			return readErr == nil && current.ActiveTaskID == "" && current.Result != nil && current.Result.Published
		})
		current, err := h.store.GetWorkItem(h.actor, work.ID)
		if err != nil || current.Usage.Tasks != 1 || current.Result.Commit != task.PublishedCommit {
			t.Fatal(current, err)
		}
		if mode == "automatic" {
			if current.Status != "completed" || task.WorkItemCompletion == nil || !task.WorkItemCompletion.CriteriaMet {
				t.Fatal("authorized judgment did not cross the Runner protocol", current, task)
			}
		} else if current.Status != "waiting_for_person" || current.QuestionKind != orchestration.WorkQuestionCompletion || task.WorkItemCompletion != nil {
			t.Fatal("human confirmation was bypassed", current, task)
		}
		h.restartTaskServer(t)
		duplicate, err := h.manager.Load().CreateWorkItem(h.actor, room.ID, request)
		if err != nil || duplicate.ID != work.ID || duplicate.ActiveTaskID != taskID {
			t.Fatal("creation receipt changed after restart", duplicate, err)
		}
		tasks, err := h.store.WorkItemHistory(h.actor, work.ID, "tasks", "", 20)
		if err != nil || len(tasks.Items) != 1 {
			t.Fatal("reply loss admitted another task", tasks, err)
		}
	}
}
