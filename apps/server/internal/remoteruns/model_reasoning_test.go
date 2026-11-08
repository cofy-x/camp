package remoteruns

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestReasoningRequiresAdvertisedRunnerSupportWithoutChangingProviderDefault(t *testing.T) {
	for _, effort := range []string{"", "off"} {
		t.Run("effort="+effort, func(t *testing.T) {
			manager, store, actor, attempt, _ := proxyFixture(t, "http://127.0.0.1:1", effort)
			run, err := store.GetRun(actor, attempt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			runner, err := store.GetRunner(actor, run.RunnerID)
			if err != nil {
				t.Fatal(err)
			}
			// Model the exact report from an older otherwise-ready Runner. The
			// stopped scheduler keeps this test free of execution/model calls.
			if _, err = store.ConnectRunner(actor, runner.ID, storage.RunnerConnection{StorageID: runner.StorageID, Runtime: "native", Slots: 1, Capabilities: json.RawMessage(`{"executor":"native","ready":true}`)}); err != nil {
				t.Fatal(err)
			}
			manager.mu.Lock()
			manager.peers[runner.ID] = &peer{lastSeen: time.Now()}
			manager.mu.Unlock()
			defer func() {
				manager.mu.Lock()
				delete(manager.peers, runner.ID)
				manager.mu.Unlock()
			}()
			report, err := manager.Readiness(actor)
			if err != nil || report.Ready != (effort == "") {
				t.Fatal("Runner readiness ignored explicit reasoning compatibility", report, err)
			}
			_, err = manager.buildInput(actor, run, attempt)
			if effort == "" {
				if err != nil {
					t.Fatal("provider-default work stopped accepting existing Runners", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "Update the assigned Runner") {
				t.Fatal("explicit reasoning reached an incompatible Runner", err)
			}
		})
	}
}

func TestExplicitReasoningRemainsFrozenAndMismatchesAreNotCharged(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil || !matchesReasoning(body, "off") {
			t.Error("explicit off did not reach Messages wire payload")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()
	manager, store, actor, attempt, command := proxyFixture(t, upstream.URL, "off")
	if command.Spec.ReasoningEffort != "off" {
		t.Fatal("frozen execution spec lost the reasoning setting")
	}
	high := "high"
	if _, err := manager.UpdateModelSettings(actor, configuration.Update{ReasoningEffort: &high}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(manager.Handler())
	defer server.Close()
	for _, body := range []string{
		`{"model":"test-model","max_tokens":20}`,
		`{"model":"test-model","max_tokens":20,"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}`,
	} {
		response := proxyRequest(t, server, attempt, command.Spec.ModelToken, body)
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatal("Runner bypassed the original explicit reasoning setting", response.StatusCode)
		}
	}
	state, err := store.GetRun(context.Background(), attempt.RunID)
	latest, readErr := store.GetAttempt(context.Background(), attempt.ID)
	if err != nil || readErr != nil || latest.ModelInFlight || calls.Load() != 0 {
		t.Fatal("rejected reasoning settings consumed a model request", err)
	}
	response := proxyRequest(t, server, attempt, command.Spec.ModelToken, `{"model":"test-model","max_tokens":20,"thinking":{"type":"disabled"}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || calls.Load() != 1 {
		t.Fatal("new settings reinterpreted an admitted request", response.StatusCode)
	}
	room, err := store.GetRoom(actor, state.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := manager.Send(actor, state.RoomID, storage.SendMessageInput{RequestID: "new-model-settings", Content: "Use the newly selected model settings.", RecipientAgentIDs: []string{state.AgentID}, RunnerRevision: &room.RunnerRevision})
	if err != nil || len(next.Runs) != 1 {
		t.Fatal("new explicit request could not be admitted", err)
	}
	model, err := manager.models.Resolve(next.Runs[0].RuntimeKey)
	if err != nil || next.Runs[0].RuntimeKey == state.RuntimeKey || model.ReasoningEffort != "high" {
		t.Fatal("new authorization did not freeze current model settings", err)
	}
}

func TestReasoningWireModes(t *testing.T) {
	for _, test := range []struct {
		effort, body string
		want         bool
	}{
		{"", `{}`, true},
		{"off", `{"thinking":{"type":"disabled"}}`, true},
		{"off", `{"thinking":{"type":"enabled"}}`, false},
		{"off", `{"thinking":{"type":"disabled"},"output_config":{"effort":"max"}}`, false},
		{"low", `{"thinking":{"type":"enabled"},"output_config":{"effort":"low"}}`, true},
		{"high", `{"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}`, true},
		{"max", `{"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}`, true},
		{"low", `{"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}`, false},
		{"max", `{"thinking":{"type":"disabled"},"output_config":{"effort":"max"}}`, false},
		{"off", `{"thinking":"disabled"}`, false},
		{"low", `{"thinking":{"type":"enabled"},"output_config":42}`, false},
	} {
		var body map[string]json.RawMessage
		_ = json.Unmarshal([]byte(test.body), &body)
		if matchesReasoning(body, test.effort) != test.want {
			t.Errorf("reasoning %q body %s: expected %v", test.effort, test.body, test.want)
		}
	}
}
