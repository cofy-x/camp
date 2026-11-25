package remoteruns

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
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

func TestQueuedAutomaticWorkWithIncompatibleRunnerDoesNotBlockOtherRuns(t *testing.T) {
	h, m := preparationHarness(t)
	defer func() {
		m.cancel()
		m.preparationWG.Wait()
		close(m.done)
	}()
	original := h.start(t, h.join(t, "automatic-work-runner", "fixture", nil, nativeexecutor.Config{}))
	healthy := h.start(t, h.join(t, "healthy-runner", "fixture", nil, nativeexecutor.Config{}))
	room, writer := h.room(t, "Queued automatic Work", original.config.RunnerID)
	otherRoom, otherWriter := h.room(t, "Unrelated run", healthy.config.RunnerID)
	work, err := m.CreateWorkItem(h.actor, room.ID, orchestration.WorkItemInput{
		RequestID: "queued-automatic-work", Goal: "Prepare the saved result", CompletionMode: "automatic", Start: true,
		ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.store.GetTaskState(h.actor, work.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	other := h.send(t, otherRoom, otherWriter, "Run independently.")
	original.stop(t)
	connection, _, err := websocket.Dial(h.ctx, strings.Replace(h.server.URL, "https://", "wss://", 1)+"/api/daemon/connect", &websocket.DialOptions{
		HTTPClient: h.server.Client(), HTTPHeader: http.Header{"Authorization": []string{"Bearer " + original.config.Credential}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err = wsjson.Write(h.ctx, connection, p.Message{Type: "hello", RequestID: "replacement-hello", Hello: &p.Hello{
		Version: p.Version, StorageID: original.config.StorageID, BootID: "older-capabilities",
		Capabilities: p.Capabilities{Executor: "fixture", Ready: true, Slots: 1, TaskInputsVersion: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	read, stop := context.WithTimeout(h.ctx, 3*time.Second)
	defer stop()
	var welcome p.Message
	if err = wsjson.Read(read, connection, &welcome); err != nil || welcome.Type != "welcome" {
		t.Fatal("replacement Runner did not connect", err)
	}
	if err = m.tick(h.ctx); err != nil {
		t.Fatal("an incompatible queued run interrupted the scheduler", err)
	}
	current, err := h.store.GetRun(h.ctx, other.ID)
	if err != nil || current.AttemptID == "" || current.Status != "preparing" {
		t.Fatal("unrelated run was not admitted", current.Status, err)
	}
	queued, err := m.GetRunSummary(h.ctx, task.ActiveRunID)
	if err != nil || queued.Status != "queued" || !strings.Contains(queued.WaitingReason, "automatic Work completion") {
		t.Fatal("queued Work lost its upgrade reason", queued.Status, queued.WaitingReason, err)
	}
}
