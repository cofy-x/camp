package remoteruns

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/eventflow"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestScheduledRoomFeedbackDigestPublishesOncePerPeriod(t *testing.T) {
	var assessments atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assessments.Add(1)
		var request struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		var candidate orchestration.Candidate
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 1 ||
			json.Unmarshal([]byte(request.Messages[0].Content), &candidate) != nil ||
			len(candidate.Events) != 1 || candidate.Events[0].Kind != "timer.fired" || candidate.Events[0].WorkItemID == "" {
			t.Error("the digest assessment needs one durable follow-up for its matter")
			http.Error(w, "unexpected assessment", http.StatusBadRequest)
			return
		}
		result, err := json.Marshal(orchestration.Assessment{
			Outcome: "execute", WorkItemID: candidate.Events[0].WorkItemID,
			Reason: "The scheduled feedback period is ready to summarize.",
		})
		if err != nil {
			t.Error(err)
			http.Error(w, "could not encode the fixture decision", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn", "content": []map[string]string{{"type": "text", "text": string(result)}},
		})
	}))
	t.Cleanup(provider.Close)
	h := newRemoteHarness(t, configuration.ModelInput{BaseURL: provider.URL, APIKey: "digest-fixture-key", Model: "digest-fixture"})
	runner := h.start(t, h.join(t, "weekly-feedback", "fixture", nil, nativeexecutor.Config{}))
	room, writer := h.room(t, "Weekly feedback", runner.config.RunnerID)
	owner, _ := storage.UserFromContext(h.actor)
	policy, err := h.store.SaveAutomationPolicy(h.actor, room.ID, "room", orchestration.PolicyInput{
		RequestID: "digest-policy", Enabled: true, Mode: "auto", Rules: "Use the selected feedback when a scheduled digest is due.",
		AllowedActorIDs: []string{owner.ID}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID,
		MaxTurns: 1, MaxModelRequests: 4, DailyEvaluations: 4, DailyTasks: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	channel := &automationTestChannel{posts: map[string]orchestration.Delivery{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var flow *eventflow.Manager
	t.Cleanup(func() {
		if flow != nil {
			if err := flow.Shutdown(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})

	var firstResult *orchestration.WorkResult
	var firstContent []byte
	for index, week := range []string{"2026-W05", "2026-W06"} {
		feedback := week + " feedback: make the preference easier to find."
		source, err := h.manager.Load().Send(h.actor, room.ID, storage.SendMessageInput{
			RequestID: week + "-feedback", Content: feedback, PostOnly: true,
		})
		if err != nil || len(source.Runs) != 0 {
			t.Fatal("recording input must not start work", source, err)
		}
		sourceID := "room:" + source.Message.ID
		request := orchestration.WorkItemInput{
			RequestID: "room-digest-" + week, Goal: "[fixture:finish] Summarize Room feedback for " + week,
			CompletionMode: "automatic", CompletionCriteria: "Publish the summary for " + week,
			PolicyID: policy.ID, PolicyRevision: policy.Revision, EvidenceIDs: []string{sourceID},
			Budget: orchestration.WorkBudget{MaxEvaluations: 1, MaxTasks: 1, MaxModelRequests: 4, MaxActions: 1, MaxFollowUps: 1},
		}
		work, err := h.manager.Load().CreateWorkItem(h.actor, room.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		due := time.Now().Add(time.Second)
		wait := orchestration.WorkItemOperation{
			RequestID: week + "-schedule", Revision: work.Revision, Kind: "wait",
			WaitCondition: "Collect feedback for " + week, ResumeAt: due.UTC().Format(time.RFC3339Nano),
		}
		waiting, err := h.manager.Load().OperateWorkItem(h.actor, work.ID, wait)
		if err != nil || waiting.Status != "waiting" || len(waiting.Timers) != 1 {
			t.Fatal("could not schedule the digest", waiting, err)
		}
		// The worker is offline when this authorized period falls due.
		// Restart uses the retained timer and its exact firing receipt.
		if index > 0 {
			h.restartTaskServer(t)
		}
		time.Sleep(time.Until(due))
		flow, err = eventflow.New(h.ctx, h.store, h.manager.Load().ModelConfiguration(), nil, logger,
			eventflow.Config{Channel: channel, PollInterval: 5 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err = h.store.FireWorkItemTimers(h.ctx); err != nil {
				t.Fatal(err)
			}
		}
		var completed orchestration.WorkItem
		remoteEventually(t, 15*time.Second, "the scheduled digest did not complete", func() bool {
			current, err := h.store.GetWorkItem(h.actor, work.ID)
			if err != nil {
				t.Fatal(err)
			}
			completed = current
			return current.Status == "completed"
		})
		if completed.Usage.FollowUps != 1 || completed.Usage.Evaluations != 1 || completed.Usage.Tasks != 1 ||
			len(completed.Timers) != 1 || completed.Timers[0].ID != waiting.Timers[0].ID ||
			completed.Timers[0].Status != "fired" || completed.Timers[0].EventID != "timer:"+waiting.Timers[0].ID ||
			completed.Result == nil || !completed.Result.Published || completed.Result.ResourceID != room.ResourceID ||
			len(completed.Result.Files) != 1 || completed.Result.Files[0].Path != "result.txt" {
			t.Fatal("the digest needs one firing, bounded task, and published result", completed)
		}
		task, err := h.store.GetTaskState(h.ctx, completed.Result.TaskID)
		if err != nil || task.PublishedCommit != completed.Result.Commit || task.PublishedCommit == "" {
			t.Fatal("Work does not point to the exact Library version", task, err)
		}
		inputs, err := h.store.TaskInputsPage(h.actor, task.ID, 0, nil, 20)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range inputs.Items {
			found = found || item.SourceEventID == sourceID && strings.Contains(item.Content, feedback)
		}
		if !found {
			t.Fatal("the task did not receive its selected feedback evidence", inputs)
		}
		content, err := h.manager.Load().files.ReadFile(h.ctx, room.ResourceID, completed.Result.Commit, "result.txt")
		if err != nil || !bytes.Contains(content, []byte(week)) {
			t.Fatal("the exact Library version does not contain this period", string(content), err)
		}
		if err = flow.Shutdown(h.ctx); err != nil {
			t.Fatal(err)
		}
		flow = nil
		repeated, err := h.manager.Load().CreateWorkItem(h.actor, room.ID, request)
		if err != nil || repeated.ID != work.ID {
			t.Fatal("replaying this period created another matter", repeated, err)
		}
		repeated, err = h.manager.Load().OperateWorkItem(h.actor, work.ID, wait)
		if err != nil || len(repeated.Timers) != 1 || repeated.Timers[0].ID != waiting.Timers[0].ID {
			t.Fatal("replaying the wait created another timer", repeated, err)
		}
		if index == 0 {
			firstResult, firstContent = completed.Result, content
		} else {
			original, err := h.manager.Load().files.ReadFile(h.ctx, room.ResourceID, firstResult.Commit, "result.txt")
			if err != nil || !bytes.Equal(original, firstContent) || firstResult.Commit == completed.Result.Commit {
				t.Fatal("a later digest changed the previous exact version", string(original), err)
			}
		}
	}
	items, err := h.store.WorkItems(h.actor, room.ID, 0, 20)
	if err != nil || len(items.Items) != 2 || assessments.Load() != 2 {
		t.Fatal("there must be one completed matter and one assessment per scheduled period", items, assessments.Load(), err)
	}
	publications, err := h.store.ListPublications(h.ctx, room.ResourceID, 0)
	if err != nil || len(publications) != 2 {
		t.Fatal("the digest published more or less than once per period", publications, err)
	}
	attention, err := h.store.Attention(h.actor, storage.AttentionOptions{RoomID: room.ID})
	if err != nil || attention.Total != 0 {
		t.Fatal("completed digests remained in Needs me", attention, err)
	}
}
