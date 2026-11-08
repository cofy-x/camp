package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestBrowserRunnerRetainsInputProtocolCapabilityAndFiltersPrivateFields(t *testing.T) {
	runner := storage.Runner{ID: "runner", Capabilities: json.RawMessage(`{"executor":"native","ready":true,"taskInputsVersion":1,"reasoningEffortVersion":1,"fingerprint":"private-runtime-identity","apiKey":"private-test-key"}`)}
	view := runnerForBrowser(runner, true)
	if view.Capabilities.TaskInputsVersion != 1 {
		t.Fatal("Native input capability was hidden from readiness clients", view)
	}
	if view.Capabilities.ReasoningEffortVersion != 1 {
		t.Fatal("Reasoning capability was hidden from readiness clients", view)
	}
	raw, err := json.Marshal(view)
	if err != nil || strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "apiKey") || strings.Contains(string(raw), "fingerprint") {
		t.Fatal("Private fields escaped the capability view", string(raw), err)
	}
}
