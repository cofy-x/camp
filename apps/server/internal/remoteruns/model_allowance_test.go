package remoteruns

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestTaskModelProxyExhaustionIsPreciseAndUncharged(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()
	manager, store, actor, priorAttempt, _ := proxyFixture(t, upstream.URL)
	prior, err := store.InterruptAttempt(actor, priorAttempt.ID, "Release isolated proxy fixture.")
	if err != nil {
		t.Fatal(err)
	}
	task, err := manager.CreateTask(actor, prior.RoomID, storage.CreateTaskInput{RequestID: "allowance-task", Goal: "Write a note.", ParticipantAgentIDs: []string{prior.AgentID}, FirstAgentID: prior.AgentID})
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
	attempt, err = store.AcceptAttempt(actor, attempt.ID, run.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	var command p.Command
	if err = json.Unmarshal(attempt.Command, &command); err != nil {
		t.Fatal(err)
	}
	// Storage charges earlier requests; only the final allowed request is sent
	// through this synthetic HTTP provider. No real model is configured.
	for range task.MaxModelRequests - 1 {
		if err = store.BeginAttemptModel(actor, attempt.ID, run.RunnerID); err != nil {
			t.Fatal(err)
		}
		if err = store.EndAttemptModel(actor, attempt.ID); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewTLSServer(manager.Handler())
	defer server.Close()
	const body = `{"model":"test-model","max_tokens":20,"stream":true}`
	response := proxyRequest(t, server, attempt, command.Spec.ModelToken, body)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || calls.Load() != 1 {
		t.Fatal("final allowed request was not sent exactly once", response.StatusCode, calls.Load())
	}
	for range 2 {
		response = proxyRequest(t, server, attempt, command.Spec.ModelToken, body)
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		var denial struct {
			Error struct{ Code, Message string }
		}
		if err = json.Unmarshal(data, &denial); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusForbidden || denial.Error.Code != "task_budget_exhausted" || !strings.Contains(denial.Error.Message, "model request allowance is exhausted") || calls.Load() != 1 {
			t.Fatalf("exhaustion not preserved: status=%d error=%+v calls=%d", response.StatusCode, denial.Error, calls.Load())
		}
		charged, readErr := store.GetTaskState(actor, task.ID)
		currentAttempt, attemptErr := store.GetAttempt(actor, attempt.ID)
		if readErr != nil || attemptErr != nil || charged.ModelRequestsUsed != task.MaxModelRequests || currentAttempt.ModelInFlight || charged.ActiveRunID != run.ID || currentAttempt.Phase != "executing" || charged.Error != "" {
			t.Fatal("denied request changed accounting or invented a terminal execution error", readErr, attemptErr)
		}
	}
	// Revocation takes priority even though the same task is still at its cap.
	if _, err = store.SetRunnerAgentGrant(actor, run.RunnerID, run.AgentID, false); err != nil {
		t.Fatal(err)
	}
	response = proxyRequest(t, server, attempt, command.Spec.ModelToken, body)
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || strings.Contains(string(data), "task_budget_exhausted") || !strings.Contains(string(data), "authorization ended") || calls.Load() != 1 {
		t.Fatalf("permission denial misclassified as exhaustion: status=%d body=%s calls=%d", response.StatusCode, data, calls.Load())
	}
}
