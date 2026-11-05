package remoteruns

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/eventflow"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type automationTestChannel struct {
	mu         sync.Mutex
	posts      map[string]orchestration.Delivery
	reconciled int
}

func (c *automationTestChannel) Deliver(_ context.Context, d orchestration.Delivery) orchestration.DeliveryResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d.Reconcile {
		c.reconciled++
		if _, ok := c.posts[d.ID]; !ok {
			return orchestration.DeliveryResult{Status: "unknown", Reason: "No retained fixture comment."}
		}
	} else {
		if _, ok := c.posts[d.ID]; ok {
			return orchestration.DeliveryResult{Status: "failed", Reason: "Fixture rejected a repeated comment POST."}
		}
		c.posts[d.ID] = d
		if d.QuestionID != "" {
			return orchestration.DeliveryResult{Status: "unknown", Reason: "The fixture lost the comment response."}
		}
	}
	return orchestration.DeliveryResult{Status: "delivered", CommentID: 100 + int64(len(c.posts)), URL: fmt.Sprintf("https://github.com/owner/repo/issues/7#issuecomment-%d", 100+len(c.posts))}
}

func automationPayload(kind, action, body string, actor, object int64) []byte {
	payload := map[string]any{"action": action, "repository": map[string]any{"id": 10, "full_name": "owner/repo"}, "sender": map[string]any{"id": actor, "login": fmt.Sprintf("person-%d", actor), "type": "User"}, "issue": map[string]any{"id": 70, "number": 7, "title": "Release guide", "body": body}}
	if kind == "issue_comment" {
		payload["comment"] = map[string]any{"id": object, "body": body}
	}
	raw, _ := json.Marshal(payload)
	return raw
}

func TestAutomationObservedAssessmentQuestionExternalAnswerAndOnePublication(t *testing.T) {
	var modelCalls atomic.Int64
	var sourceCalls, followUpCalls atomic.Int64
	var assessments sync.Map
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 1 {
			t.Error("invalid assessment request")
			w.WriteHeader(400)
			return
		}
		var candidate orchestration.Candidate
		if err := json.Unmarshal([]byte(request.Messages[0].Content), &candidate); err != nil || len(candidate.Events) == 0 {
			t.Error("missing frozen assessment candidate")
			w.WriteHeader(400)
			return
		}
		if _, loaded := assessments.LoadOrStore(candidate.ID, true); loaded {
			t.Error("a frozen assessment was sent to the provider twice")
		}
		assessment := orchestration.Assessment{Outcome: "accept", CompletionCriteria: "The requested guide is published and all outgoing comments have confirmed receipts.", Goal: "[fixture:ask-person] Prepare a release guide, ask who should receive it, and finish.", Reason: "A release guide request matches the policy."}
		if strings.Contains(candidate.Events[len(candidate.Events)-1].Content, "Additional context") {
			assessment = orchestration.Assessment{Outcome: "ignore", Reason: "This is context for the existing active guide task."}
		}
		if strings.Contains(candidate.Events[len(candidate.Events)-1].Content, "New request after completion") {
			assessment.Goal = "[fixture:finish] Prepare a separate follow-up guide."
		}
		if target := candidate.Events[0].WorkItemID; target != "" {
			followUpCalls.Add(1)
			assessment = orchestration.Assessment{Outcome: "complete", WorkItemID: target, Reason: "The guide task has finished; close the matter after outgoing delivery is confirmed."}
		} else {
			sourceCalls.Add(1)
		}
		text, _ := json.Marshal(assessment)
		_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": string(text)}}})
	}))
	defer provider.Close()
	h := newRemoteHarness(t, configuration.ModelInput{BaseURL: provider.URL, APIKey: "private-assessment-fixture-key", Model: "fixture-model"})
	runner := h.start(t, h.join(t, "automation-runner", "fixture", nil, nativeexecutor.Config{}))
	room, writer := h.room(t, "Automated guide", runner.config.RunnerID)
	webhookRef := strings.Repeat("a", 48)
	source, err := h.store.CreateGitHubSource(h.actor, room.ID, storage.CreateGitHubSourceInput{RequestID: "create-automation-source", Repository: "owner/repo", Secret: strings.Repeat("s", 40)}, webhookRef)
	if err != nil {
		t.Fatal(err)
	}
	policyInput := orchestration.PolicyInput{RequestID: "observe-automation", Enabled: true, Mode: "observe", Rules: "Prepare requested release guides. Additional context belongs to an existing task.", AllowedActorIDs: []string{"123"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, MaxTurns: 4, MaxModelRequests: 12, DailyEvaluations: 10, DailyTasks: 4, AllowAnswers: true, AllowComments: true}
	policy, err := h.store.SaveAutomationPolicy(h.actor, room.ID, source.ID, policyInput)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := configuration.OpenGitHubCredentials(filepath.Join(h.data, "test-comment-credentials"))
	if err != nil {
		t.Fatal(err)
	}
	credentialRef, err := credentials.Write(strings.Repeat("private-token", 4), 999)
	if err != nil {
		t.Fatal(err)
	}
	credentialInput := storage.AutomationCredentialInput{RequestID: "save-comment-credential", Revision: policy.Revision, Token: strings.Repeat("private-token", 4), ActorID: 999}
	credentialReceipt, err := h.store.SetAutomationCredential(h.actor, source.ID, credentialInput, credentialRef)
	if err != nil {
		t.Fatal(err)
	}
	policy = *credentialReceipt.Policy
	channel := &automationTestChannel{posts: map[string]orchestration.Delivery{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var flow *eventflow.Manager
	startFlow := func() {
		var err error
		flow, err = eventflow.New(h.ctx, h.store, h.manager.Load().ModelConfiguration(), credentials, logger, eventflow.Config{Channel: channel, PollInterval: 5 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
	}
	startFlow()
	t.Cleanup(func() {
		if flow != nil {
			if err := flow.Shutdown(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	deliveryNumber := 0
	receive := func(kind, action, body string, actor, object int64) (storage.GitHubEvent, []byte, string) {
		t.Helper()
		deliveryNumber++
		guid := fmt.Sprintf("00000000-0000-4000-8000-%012d", deliveryNumber)
		payload := automationPayload(kind, action, body, actor, object)
		event, duplicate, err := h.store.ReceiveGitHubDelivery(h.ctx, source.ID, webhookRef, guid, kind, payload)
		if err != nil || duplicate {
			t.Fatal("receive event", event, duplicate, err)
		}
		return event, payload, guid
	}
	awaitEvent := func(id, status string) orchestration.SourceEvent {
		t.Helper()
		var found orchestration.SourceEvent
		remoteEventually(t, 5*time.Second, "event did not reach "+status, func() bool {
			page, err := h.store.AutomationEvents(h.actor, room.ID, 0, nil, 20)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				if item.ID == "github:"+id {
					found = item
					return item.Status == status
				}
			}
			return false
		})
		return found
	}
	observed, _, _ := receive("issues", "opened", "Please prepare the release guide.", 123, 70)
	awaitEvent(observed.ID, "observed")
	if tasks, err := h.store.ListRoomTasks(h.ctx, room.ID); err != nil || len(tasks) != 0 {
		t.Fatal("observation admitted work", tasks, err)
	}
	policyInput.RequestID, policyInput.Revision, policyInput.Mode = "enable-automatic-work", policy.Revision, "auto"
	if _, err = h.store.SaveAutomationPolicy(h.actor, room.ID, source.ID, policyInput); err != nil {
		t.Fatal(err)
	}
	accepted, _, _ := receive("issues", "reopened", "Please prepare the release guide now.", 123, 70)
	admission := awaitEvent(accepted.ID, "accepted")
	if admission.TaskID == "" {
		t.Fatal("accepted event has no task")
	}
	task := h.taskState(t, admission.TaskID, "waiting_for_person")
	if task.SourceEventID != accepted.ID || task.AutomationSourceEventID != "github:"+accepted.ID {
		t.Fatalf("automatic task lost its source resolver or generic provenance: %+v", task)
	}
	if original, err := h.store.GetGitHubEvent(h.actor, task.SourceEventID); err != nil || original.ID != accepted.ID {
		t.Fatal("automatic task sourceEventId cannot resolve its original GitHub event", original, err)
	}
	remoteEventually(t, 5*time.Second, "question comment did not become durable", func() bool {
		deliveries, err := h.store.TaskAutomationDeliveries(h.actor, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(deliveries) == 1 && deliveries[0].Status == "sent" && deliveries[0].QuestionID == task.Question.ID
	})
	if task.Question == nil || task.TurnsUsed != 1 || task.InputRevision != 1 {
		t.Fatalf("unexpected question state: %+v", task)
	}
	channel.mu.Lock()
	postCount, reconcileCount := len(channel.posts), channel.reconciled
	var questionDelivery orchestration.Delivery
	for _, item := range channel.posts {
		questionDelivery = item
	}
	channel.mu.Unlock()
	if postCount != 1 || reconcileCount != 1 {
		t.Fatal("lost comment reply caused duplicate delivery")
	}
	// A source's own comment cannot feed its task or request another assessment.
	echo, _, _ := receive("issue_comment", "created", questionDelivery.Body+"\n\n"+questionDelivery.Marker, 999, 101)
	awaitEvent(echo.ID, "ignored")
	contextEvent, _, _ := receive("issue_comment", "created", "Additional context: use our internal release terminology.", 123, 202)
	awaitEvent(contextEvent.ID, "ignored")
	stillWaiting := h.taskState(t, task.ID, "waiting_for_person")
	if stillWaiting.InputRevision != 2 || stillWaiting.Question.Answer != "" {
		t.Fatal("context resumed or failed to enter the task", stillWaiting)
	}
	if tasks, err := h.store.ListRoomTasks(h.ctx, room.ID); err != nil || len(tasks) != 1 {
		t.Fatal("context created another task", tasks, err)
	}
	// Restart both Server workers while the task is waiting, without another turn.
	if err = flow.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	flow = nil
	h.restartTaskServer(t)
	startFlow()
	remoteEventually(t, 10*time.Second, "Runner did not reconnect", func() bool { return h.manager.Load().IsOnline(runner.config.RunnerID) })
	restored := h.taskState(t, task.ID, "waiting_for_person")
	if restored.Question.ID != task.Question.ID || restored.TurnsUsed != 1 {
		t.Fatal("restart changed the question", restored)
	}
	answer, answerPayload, answerGUID := receive("issue_comment", "created", "/camp answer "+task.Question.ID+" Internal team", 123, 303)
	awaitEvent(answer.ID, "answered")
	if replay, duplicate, err := h.store.ReceiveGitHubDelivery(h.ctx, source.ID, webhookRef, answerGUID, "issue_comment", answerPayload); err != nil || !duplicate || replay.ID != answer.ID {
		t.Fatal("answer replay was not idempotent", replay, duplicate, err)
	}
	finished := h.taskState(t, task.ID, "completed")
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		w, err := h.store.GetWorkItem(h.actor, finished.WorkItemID)
		if err != nil {
			t.Log("matter diagnostics", err)
			return
		}
		t.Logf("matter status=%s revision=%d active=%s question=%q wait=%q usage=%+v", w.Status, w.Revision, w.ActiveTaskID, w.Question, w.WaitCondition, w.Usage)
		for _, d := range w.Decisions {
			t.Logf("decision kind=%s actor=%s/%s reason=%q eval=%s", d.Kind, d.ActorKind, d.ActorID, d.Reason, d.EvaluationID)
		}
		page, err := h.store.AutomationEvents(h.actor, room.ID, 0, nil, 20)
		if err == nil {
			for _, ev := range page.Items {
				t.Logf("event kind=%s status=%s reason=%s eval=%s work=%s", ev.Kind, ev.Status, ev.Reason, ev.EvaluationID, ev.WorkItemID)
			}
		}
	})
	if finished.Question.Answer != "Internal team" || finished.Question.AnsweredByUserID != "" || finished.Question.AnsweredByExternalActorID != "123" || finished.Question.AnswerSourceEventID != "github:"+answer.ID || finished.TurnsUsed != 2 {
		t.Fatalf("external answer attribution or rounds: %+v", finished)
	}
	remoteEventually(t, 5*time.Second, "final comment was not sent once", func() bool {
		deliveries, err := h.store.TaskAutomationDeliveries(h.actor, task.ID)
		return err == nil && len(deliveries) == 2 && deliveries[0].Status == "sent" && deliveries[1].Status == "sent"
	})
	if publications, err := h.store.ListPublications(h.ctx, room.ResourceID, 0); err != nil || len(publications) != 1 {
		t.Fatal("expected one final shared publication", publications, err)
	}
	if rounds, err := h.store.ListTaskRuns(h.ctx, task.ID); err != nil || len(rounds) != 2 {
		t.Fatal("answer or restart repeated a turn", rounds, err)
	}
	inputs, err := h.store.TaskInputsPage(h.actor, task.ID, 0, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs.Items {
		if input.AuthorUserID != "" || input.SourceEventID == "" || input.ExternalActorID != "123" {
			t.Fatalf("external original was attributed to a Camp member: %+v", input)
		}
	}
	remoteEventually(t, 5*time.Second, "the published matter did not complete after confirmed effects", func() bool {
		w, err := h.store.GetWorkItem(h.actor, finished.WorkItemID)
		return err == nil && w.Status == "completed" && w.Usage.Tasks == 1
	})
	if sourceCalls.Load() != 3 || followUpCalls.Load() < 1 || modelCalls.Load() != sourceCalls.Load()+followUpCalls.Load() {
		t.Fatalf("source intake replayed or the matter failed to follow up: source=%d followUp=%d total=%d", sourceCalls.Load(), followUpCalls.Load(), modelCalls.Load())
	}
	terminalNew, _, _ := receive("issue_comment", "created", "New request after completion: prepare a separate follow-up guide.", 123, 404)
	newAdmission := awaitEvent(terminalNew.ID, "accepted")
	if newAdmission.TaskID == task.ID || newAdmission.TaskID == "" {
		t.Fatal("terminal task was revived", newAdmission)
	}
	unchanged, err := h.store.GetTask(h.ctx, task.ID)
	if err != nil || unchanged.Status != "completed" || unchanged.TurnsUsed != 2 || unchanged.InputRevision != finished.InputRevision {
		t.Fatal("new request changed the completed task", unchanged, err)
	}
	h.taskState(t, newAdmission.TaskID, "completed")
}
