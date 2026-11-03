package eventflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

type frozenModels struct {
	model        configuration.Model
	resolveError bool
}

func (m frozenModels) Snapshot() (string, configuration.Model) { return "frozen-model", m.model }
func (m frozenModels) Resolve(revision string) (configuration.Model, error) {
	if revision != "frozen-model" || m.resolveError {
		return configuration.Model{}, errors.New("private configuration unavailable")
	}
	return m.model, nil
}

func assessmentCandidate() orchestration.Candidate {
	return orchestration.Candidate{ID: "evaluation", ModelRevision: "frozen-model", Policy: orchestration.Policy{Rules: "Address release requests.", CredentialRef: "private-credential-reference"}, Events: []orchestration.SourceEvent{{ID: "event", Content: "Please prepare the release notes."}}}
}

func assessmentMessage(w http.ResponseWriter, result string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": result}}})
}

func TestAssessmentUsesFrozenModelAndOneBoundedToolFreeRequest(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "private-model-key" {
			t.Error("unexpected model origin or credential")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if string(body["max_tokens"]) != "1024" || string(body["stream"]) != "false" || body["tools"] != nil {
			t.Fatal("assessment budget/tools were not bounded")
		}
		if string(body["thinking"]) != `{"type":"disabled"}` {
			t.Fatal("bounded assessment did not explicitly disable provider reasoning")
		}
		if strings.Contains(string(body["messages"]), "private-credential-reference") {
			t.Error("outbound credential reference leaked into model context")
		}
		if string(body["model"]) != `"configured-model"` {
			t.Error("frozen model was not used")
		}
		assessmentMessage(w, `{"outcome":"accept","goal":"Prepare release notes.","reason":"Matches the release policy."}`)
	}))
	defer server.Close()
	manager := &Manager{models: frozenModels{model: configuration.Model{BaseURL: server.URL, APIKey: "private-model-key", Model: "configured-model", MaxTokens: 4096, TimeoutSeconds: 180}}}
	result := manager.assess(context.Background(), assessmentCandidate())
	if result.Outcome != "accept" || result.Goal != "Prepare release notes." || calls.Load() != 1 {
		t.Fatalf("assessment: %+v, calls=%d", result, calls.Load())
	}
}

func TestAssessmentFailureNeverRetriesOrExposesProviderDetails(t *testing.T) {
	for _, scenario := range []string{"server_error", "redirect", "invalid_json", "tool", "truncated", "extra_field", "extra_json"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch scenario {
				case "server_error":
					w.WriteHeader(502)
					_, _ = w.Write([]byte("private-provider-key"))
				case "redirect":
					http.Redirect(w, r, "/redirected", 302)
				case "invalid_json":
					_, _ = w.Write([]byte("invalid"))
				case "tool":
					_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "end_turn", "content": []any{map[string]string{"type": "tool_use"}}})
				case "truncated":
					_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "max_tokens", "content": []any{map[string]string{"type": "text", "text": `{"outcome":"ignore","reason":"Unclear"}`}}})
				case "extra_field":
					assessmentMessage(w, `{"outcome":"ignore","reason":"Unclear","tokens":1000000}`)
				case "extra_json":
					assessmentMessage(w, `{"outcome":"ignore","reason":"Unclear"}{}`)
				}
			}))
			defer server.Close()
			manager := &Manager{models: frozenModels{model: configuration.Model{BaseURL: server.URL, APIKey: "private-provider-key", Model: "model", MaxTokens: 512, TimeoutSeconds: 1}}}
			result := manager.assess(context.Background(), assessmentCandidate())
			if result.Outcome != "unavailable" || calls.Load() != 1 || strings.Contains(result.Reason, "private-provider-key") {
				t.Fatalf("failure: %+v, calls=%d", result, calls.Load())
			}
		})
	}
}

func TestAssessmentMissingConfigurationAndOversizedContextDoNotCallProvider(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	models := frozenModels{model: configuration.Model{BaseURL: server.URL, APIKey: "private-key", Model: "model", MaxTokens: 1024, TimeoutSeconds: 1}}
	candidate := assessmentCandidate()
	candidate.Events[0].Content = strings.Repeat("x", 128<<10)
	result := (&Manager{models: models}).assess(context.Background(), candidate)
	if result.Outcome != "unavailable" || calls.Load() != 0 {
		t.Fatal(result)
	}
	models.resolveError = true
	result = (&Manager{models: models}).assess(context.Background(), assessmentCandidate())
	if result.Outcome != "unavailable" || calls.Load() != 0 {
		t.Fatal(result)
	}
}
