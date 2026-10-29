package remoteruns

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (h *remoteHarness) taskState(t *testing.T, id, status string) storage.CollaborationTask {
	t.Helper()
	var task storage.CollaborationTask
	remoteEventually(t, 25*time.Second, "collaboration did not reach "+status, func() bool {
		task, _ = h.store.GetTask(h.ctx, id)
		if task.Status == "needs_attention" && status != "needs_attention" {
			t.Fatalf("Collaboration needs attention: %+v", task)
		}
		return task.Status == status
	})
	return task
}
func (h *remoteHarness) restartTaskServer(t *testing.T) {
	t.Helper()
	old := h.manager.Swap(nil)
	shutdown, stop := context.WithTimeout(h.ctx, 10*time.Second)
	if err := old.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	stop()
	fresh, err := New(h.ctx, h.store, h.config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Store(fresh)
}
func TestRemoteCollaborationHandoffQuestionRestartAndOnePublication(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	a := h.start(t, h.join(t, "task-alpha", "fixture", nil, nativeexecutor.Config{}))
	b := h.start(t, h.join(t, "task-bravo", "fixture", nil, nativeexecutor.Config{}))
	room, writer := h.room(t, "Collaboration", a.config.RunnerID)
	reviewer, err := h.store.CreateRoomAgent(h.actor, room.ID, storage.Agent{Name: "Task reviewer", Handle: "task-reviewer", Access: "write", Enabled: true, PreferredRunnerID: b.config.RunnerID})
	if err != nil {
		t.Fatal(err)
	}
	request := storage.CreateTaskInput{RequestID: "one-collaboration", Goal: "[fixture:handoff-ask] Prepare the guide, ask about its audience, and finish.", ParticipantAgentIDs: []string{writer.ID, reviewer.ID}, FirstAgentID: writer.ID}
	h.lostResponse.Store(true)
	task, err := h.manager.Load().CreateTask(h.actor, room.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.manager.Load().CreateTask(h.actor, room.ID, request)
	if err != nil || duplicate.ID != task.ID {
		t.Fatal("Create receipt did not replay", duplicate, err)
	}
	task = h.taskState(t, task.ID, "waiting_for_person")
	if task.Question == nil || task.ActiveRunID != "" || task.CurrentAgentID != reviewer.ID || task.TurnsUsed != 2 {
		t.Fatalf("Question did not release execution: %+v", task)
	}
	rounds, err := h.store.ListTaskRuns(h.ctx, task.ID)
	if err != nil || len(rounds) != 2 {
		t.Fatal("Handoff repeated or was lost", rounds, err)
	}
	first, second := rounds[0], rounds[1]
	if first.RunnerID != a.config.RunnerID || second.RunnerID != b.config.RunnerID || second.InputCommit != first.ResultCommit || first.Published || second.Published {
		t.Fatal("Draft did not cross frozen Runners", first, second)
	}
	resource, err := h.store.GetResource(h.ctx, room.ResourceID)
	if err != nil || resource.CurrentCommit != task.BaseSharedCommit {
		t.Fatal("Intermediate checkpoint changed shared files", resource, err)
	}
	if publications, err := h.store.ListPublications(h.ctx, room.ResourceID, 0); err != nil || len(publications) != 0 {
		t.Fatal("Question published prematurely", publications, err)
	}

	// No executing prompt is resumed: the durable question survives both owners.
	b.stop(t)
	h.restartTaskServer(t)
	h.start(t, b.config)
	remoteEventually(t, 12*time.Second, "first Runner did not reconnect", func() bool { return h.manager.Load().IsOnline(a.config.RunnerID) })
	restored := h.taskState(t, task.ID, "waiting_for_person")
	if restored.Question.ID != task.Question.ID || restored.CheckpointCommit != task.CheckpointCommit || restored.TurnsUsed != 2 {
		t.Fatal("Waiting state changed across restart", restored)
	}
	h.mu.Lock()
	result := h.results[first.AttemptID]
	h.mu.Unlock()
	raw, _ := json.Marshal(result)
	receipt := h.request(t, a.config, "POST", "/api/daemon/operations/"+first.AttemptID+"/complete", raw, nil)
	if receipt.Disposition != "committed" {
		t.Fatal(receipt)
	}
	answer := storage.TaskOperation{RequestID: "answer-once", Revision: restored.Revision, QuestionID: restored.Question.ID, Content: "Internal team"}
	accepted, err := h.manager.Load().OperateTask(h.actor, task.ID, "answers", answer)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := h.manager.Load().OperateTask(h.actor, task.ID, "answers", answer)
	if err != nil || replay.ActiveRunID != accepted.ActiveRunID {
		t.Fatal("Answer receipt started a second round", replay, err)
	}
	finished := h.taskState(t, task.ID, "completed")
	rounds, err = h.store.ListTaskRuns(h.ctx, task.ID)
	if err != nil || len(rounds) != 3 {
		t.Fatal("Expected exactly three authorized rounds", rounds, err)
	}
	if finished.TurnsUsed != 3 || finished.ProcessedInputRevision != 1 || finished.Question.Answer != "Internal team" || finished.PublishedCommit == "" {
		t.Fatal("Answer or final publication lost", finished)
	}
	if !rounds[2].Published || rounds[2].RunnerID != b.config.RunnerID || rounds[2].InputCommit != second.ResultCommit {
		t.Fatal("Wrong final Runner or draft", rounds[2])
	}
	file, err := h.manager.Load().files.ReadFile(h.ctx, room.ResourceID, finished.PublishedCommit, "result.txt")
	if err != nil || !strings.Contains(string(file), "Runner task-bravo") || !strings.Contains(string(file), "Internal team") {
		t.Fatalf("Final artifact lost task input: %q %v", file, err)
	}
	publications, err := h.store.ListPublications(h.ctx, room.ResourceID, 0)
	if err != nil || len(publications) != 1 {
		t.Fatal("Final publication repeated", publications, err)
	}
	inbox, err := h.store.ListInbox(h.actor)
	if err != nil {
		t.Fatal(err)
	}
	questions, completions := 0, 0
	for _, item := range inbox {
		if item.TaskID != task.ID {
			continue
		}
		if item.Kind == "task_question" {
			questions++
		}
		if item.Kind == "task_finished" {
			completions++
		}
	}
	if questions != 1 || completions != 1 {
		t.Fatalf("Duplicate task notifications: question=%d completed=%d", questions, completions)
	}
	for _, round := range rounds {
		events, err := h.store.ListRunEvents(h.ctx, round.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		starts := 0
		for _, event := range events {
			if strings.Contains(event.Text, "Execution accepted") {
				starts++
			}
		}
		if starts != 1 {
			t.Fatalf("Round %s executed %d times", round.ID, starts)
		}
	}
}

func TestRemoteCollaborationCancellationStopsOnlyItsActiveRound(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	a := h.start(t, h.join(t, "cancel-alpha", "fixture", nil, nativeexecutor.Config{}))
	b := h.start(t, h.join(t, "cancel-bravo", "fixture", nil, nativeexecutor.Config{}))
	room, writer := h.room(t, "Cancelled collaboration", a.config.RunnerID)
	reviewer, err := h.store.CreateRoomAgent(h.actor, room.ID, storage.Agent{Name: "Waiting reviewer", Handle: "waiting-reviewer", Access: "write", Enabled: true, PreferredRunnerID: b.config.RunnerID})
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.manager.Load().CreateTask(h.actor, room.ID, storage.CreateTaskInput{RequestID: "cancellable-task", Goal: "[fixture:handoff-wait] Send the draft to the reviewer.", ParticipantAgentIDs: []string{writer.ID, reviewer.ID}, FirstAgentID: writer.ID})
	if err != nil {
		t.Fatal(err)
	}
	var waiting storage.Run
	remoteEventually(t, 25*time.Second, "Second round did not reach cancellation barrier", func() bool {
		task, _ = h.store.GetTask(h.ctx, task.ID)
		if task.Status == "needs_attention" {
			t.Fatal(task)
		}
		if task.ActiveRunID != "" {
			waiting, _ = h.store.GetRun(h.ctx, task.ActiveRunID)
		}
		return waiting.AgentID == reviewer.ID && strings.Contains(waiting.Output, "Waiting at the cancellation barrier")
	})
	otherRoom, otherAgent := h.room(t, "Independent work", a.config.RunnerID)
	if other := h.finished(t, h.send(t, otherRoom, otherAgent, "Continue independently.").ID); other.Status != "succeeded" {
		t.Fatal(other)
	}
	task, _ = h.store.GetTask(h.ctx, task.ID)
	cancellation := storage.TaskOperation{RequestID: "cancel-once", Revision: task.Revision}
	if _, err = h.manager.Load().OperateTask(h.actor, task.ID, "cancel", cancellation); err != nil {
		t.Fatal(err)
	}
	cancelled := h.taskState(t, task.ID, "cancelled")
	if _, err = h.manager.Load().OperateTask(h.actor, task.ID, "cancel", cancellation); err != nil {
		t.Fatal("Cancel receipt did not replay", err)
	}
	rounds, err := h.store.ListTaskRuns(h.ctx, task.ID)
	if err != nil || len(rounds) != 2 || rounds[1].Status != "cancelled" {
		t.Fatal("Cancellation replayed or lost work", rounds, err)
	}
	h.mu.Lock()
	result := h.results[rounds[0].AttemptID]
	h.mu.Unlock()
	raw, _ := json.Marshal(result)
	receipt := h.request(t, a.config, "POST", "/api/daemon/operations/"+rounds[0].AttemptID+"/complete", raw, nil)
	if receipt.Disposition != "committed" {
		t.Fatal(receipt)
	}
	current, _ := h.store.GetTask(h.ctx, task.ID)
	if current.Status != "cancelled" || current.Revision != cancelled.Revision {
		t.Fatal("Old completion revived a cancelled task", current)
	}
	resource, _ := h.store.GetResource(h.ctx, room.ResourceID)
	if resource.CurrentCommit != task.BaseSharedCommit {
		t.Fatal("Cancelled draft was published")
	}
	if publications, err := h.store.ListPublications(h.ctx, room.ResourceID, 0); err != nil || len(publications) != 0 {
		t.Fatal(publications, err)
	}
}
