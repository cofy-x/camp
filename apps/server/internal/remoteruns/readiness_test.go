package remoteruns

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/runs"
)

func TestReadinessRefreshPreservesObservationScopeWithoutModelCalls(t *testing.T) {
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer provider.Close()
	h := newRemoteHarness(t, runs.Config{APIKey: "private-test-key", BaseURL: provider.URL})
	m := h.manager.Load()
	component := func(report runs.Readiness, id string) configuration.Diagnostic {
		t.Helper()
		for _, value := range report.Components {
			if value.ID == id {
				return value.Diagnostic
			}
		}
		t.Fatal("Missing component", id)
		return configuration.Diagnostic{}
	}
	revision, _ := m.models.Snapshot()
	key := "connection:" + revision
	if err := m.diagnostics.Record(key, "ready", "A provider response was observed."); err != nil {
		t.Fatal(err)
	}
	if err := m.diagnostics.Record(key, "error", "The last provider request failed."); err != nil {
		t.Fatal(err)
	}
	previous := m.diagnostics.Get(key)
	for range 2 {
		report, err := m.Readiness(h.actor)
		if err != nil {
			t.Fatal(err)
		}
		if report.Ready || component(report, "model").Status != "ready" || !reflect.DeepEqual(component(report, "connection"), previous) {
			t.Fatal("Refresh changed observation history or claimed execution readiness", report)
		}
		if component(report, "first-task").Status != "unknown" {
			t.Fatal("Configuration was mistaken for native success")
		}
	}
	config := h.join(t, "Fixture", "fixture", nil, nativeexecutor.Config{})
	runner := h.start(t, config)
	room, agent := h.room(t, "Fixture task", config.RunnerID)
	h.finished(t, h.send(t, room, agent, "Create a deterministic fixture file.").ID)
	report, err := m.Readiness(h.actor)
	if err != nil || !report.Ready || component(report, "first-task").Status != "unknown" {
		t.Fatal("Fixture work was mistaken for native success", report, err)
	}
	runner.stop(t)
	remoteEventually(t, 5*time.Second, "Runner did not disconnect", func() bool { return !m.IsOnline(config.RunnerID) })
	report, err = m.Readiness(h.actor)
	if err != nil || report.Ready {
		t.Fatal("Disconnected Runner remained available", err)
	}
	model := "new-model-revision"
	if _, err := m.UpdateModelSettings(h.actor, configuration.Update{Model: &model}); err != nil {
		t.Fatal(err)
	}
	report, err = m.Readiness(h.actor)
	if err != nil || component(report, "connection").Status != "unknown" || !reflect.DeepEqual(m.diagnostics.Get(key), previous) {
		t.Fatal("New model inherited an old connection observation", err)
	}
	if calls.Load() != 0 {
		t.Fatal("A status refresh invoked the model", calls.Load())
	}
}
