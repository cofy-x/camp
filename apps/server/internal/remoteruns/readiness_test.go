package remoteruns

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestReadinessRefreshPreservesObservationScopeWithoutModelCalls(t *testing.T) {
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer provider.Close()
	h := newRemoteHarness(t, configuration.ModelInput{APIKey: "private-test-key", BaseURL: provider.URL})
	m := h.manager.Load()
	component := func(report configuration.Readiness, id string) configuration.ReadinessComponent {
		t.Helper()
		for _, value := range report.Components {
			if value.ID == id {
				return value
			}
		}
		t.Fatal("Missing component", id)
		return configuration.ReadinessComponent{}
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
		if report.Ready || component(report, "model").Status != "ready" || !reflect.DeepEqual(component(report, "connection").Diagnostic, previous) {
			t.Fatal("Refresh changed observation history or claimed execution readiness", report)
		}
		if component(report, "first-task").Status != "unknown" {
			t.Fatal("Configuration was mistaken for native success")
		}
		if issue := component(report, "execution").Issue; issue != "runner_not_connected" {
			t.Fatal("Missing Runner has the wrong recovery", issue)
		}
	}
	config := h.join(t, "Fixture", "fixture", nil, nativeexecutor.Config{})
	runner := h.start(t, config)
	room, agent := h.room(t, "Fixture task", config.RunnerID)
	finished := h.finished(t, h.send(t, room, agent, "Create a deterministic fixture file.").ID)
	report, err := m.Readiness(h.actor)
	if err != nil || !report.Ready || component(report, "first-task").Status != "unknown" {
		t.Fatal("Fixture work was mistaken for native success", report, err)
	}
	effort := "high"
	if _, err := m.UpdateModelSettings(h.actor, configuration.Update{ReasoningEffort: &effort}); err != nil {
		t.Fatal(err)
	}
	_, err = h.store.ConnectRunner(h.ctx, config.RunnerID, storage.RunnerConnection{
		StorageID: config.StorageID, Runtime: "fixture", Slots: 1,
		Capabilities: []byte(`{"executor":"fixture","ready":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err = m.Readiness(h.actor)
	if err != nil || report.Ready || component(report, "execution").Issue != "runner_upgrade_required" {
		t.Fatal("An incompatible Runner should offer an update", report, err)
	}
	views, err := m.ListRunners(h.actor)
	if err != nil || len(views) != 1 || !views[0].ModelUpgradeRequired {
		t.Fatal("Runner overview missed the same incompatibility", views, err)
	}
	runner.stop(t)
	remoteEventually(t, 5*time.Second, "Runner did not disconnect", func() bool { return !m.IsOnline(config.RunnerID) })
	report, err = m.Readiness(h.actor)
	if err != nil || report.Ready || component(report, "execution").Issue != "runner_unavailable" {
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
	// Emulate a pre-provenance Run in the test database. A later native
	// registration must preserve its evidence without certifying its executor.
	db, err := sql.Open("sqlite", filepath.Join(h.data, "camp.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(h.ctx, `UPDATE runs SET snapshot=json_remove(snapshot,'$.executor') WHERE id=?`, finished.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.ConnectRunner(h.ctx, config.RunnerID, storage.RunnerConnection{
		StorageID: config.StorageID, Runtime: "native", Slots: 1,
		Capabilities: []byte(`{"executor":"native","ready":true,"reasoningEffortVersion":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	views, err = m.ListRunners(h.actor)
	if err != nil || len(views) != 1 || views[0].EarlierTaskRunID != finished.ID || views[0].FirstTaskRunID != "" {
		t.Fatal("Earlier success was not kept as unattributed history", views, err)
	}
	report, err = m.Readiness(h.actor)
	if err != nil || component(report, "first-task").Status != "unknown" {
		t.Fatal("Unattributed history was mistaken for verified native work", report, err)
	}
	if calls.Load() != 0 {
		t.Fatal("A status refresh invoked the model", calls.Load())
	}
}
