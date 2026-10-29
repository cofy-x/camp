package runs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func valuePtr[T any](value T) *T { return &value }
func TestQueuedRequestsKeepPrivateConfigurationAndBudgets(t *testing.T) {
	m, _, dir, room := testManager(t)
	firstRevision, _ := m.models.Snapshot()
	started := make(chan storage.Run, 3)
	release := make(chan struct{}, 3)
	resolved := make(chan configuration.Model, 3)
	m.mu.Lock()
	m.unavailable = ""
	m.executeRun = func(ctx context.Context, e *execution) {
		m.mu.Lock()
		run := e.run
		m.mu.Unlock()
		model, err := m.models.Resolve(run.RuntimeKey)
		resolved <- model
		started <- run
		select {
		case <-release:
		case <-ctx.Done():
		}
		m.finish(e, ctx, nil, "", err)
	}
	m.mu.Unlock()
	first := sendTo(t, m, room, "first-model-version").Runs[0]
	awaitStart(t, started)
	queued := sendTo(t, m, room, "queued-model-version").Runs[0]
	view, err := m.models.Update(configuration.Update{APIKey: valuePtr("replacement-private-key"), Model: valuePtr("new-model"), MaxTokens: valuePtr(2048)})
	if err != nil {
		t.Fatal(err)
	}
	future := sendTo(t, m, room, "future-model-version").Runs[0]
	if _, err := m.models.Update(configuration.Update{ClearAPIKey: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(context.Background(), room.ID, storage.SendMessageInput{RequestID: "missing-connection", Content: "New work", RecipientAgentIDs: []string{room.DefaultAgentID}}); err == nil {
		t.Fatal("new work admitted without a current key")
	}

	if first.RuntimeKey != firstRevision || queued.RuntimeKey != firstRevision || future.RuntimeKey != view.Revision || queued.MaxTokens != 1024 || future.MaxTokens != 2048 {
		t.Fatal("queued configuration or budget was changed")
	}
	for i := 0; i < 3; i++ {
		release <- struct{}{}
		if i < 2 {
			awaitStart(t, started)
		}
	}
	waitRun(t, m, future.ID)
	for i := 0; i < 3; i++ {
		model := <-resolved
		if i < 2 && model.APIKey != "camp-test-keyless" {
			t.Fatal("old request used new key")
		}
		if i == 2 && model.APIKey != "replacement-private-key" {
			t.Fatal("new request used old key")
		}
	}
	stored, _ := m.Get(context.Background(), queued.ID)
	encoded, _ := json.Marshal(stored)
	if strings.Contains(string(encoded), "camp-test-keyless") || strings.Contains(string(encoded), "replacement-private-key") {
		t.Fatal("credential leaked into public run")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	models, err := configuration.Open(filepath.Join(dir, "settings"), m.config.modelDefaults(), configuration.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	old, err := models.Resolve(firstRevision)
	if err != nil || old.APIKey != "camp-test-keyless" {
		t.Fatal("private queued version was not restart durable")
	}
}
func TestReadinessExplainsMissingComponentsWithoutModelCalls(t *testing.T) {
	m, _, _, _ := testManager(t)
	nodePath := m.config.NodePath
	report, err := m.CheckReadiness(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.CheckedAt != "" || len(report.Components) < 12 {
		t.Fatalf("incomplete diagnostics: %+v", report)
	}
	statuses := map[string]string{}
	for _, item := range report.Components {
		statuses[item.ID] = item.Status
	}
	if statuses["model"] != "ready" || statuses["connection"] != "unknown" || statuses["storage"] != "unknown" {
		t.Fatalf("misreported components: %+v", statuses)
	}
	before, err := os.ReadFile(filepath.Join(filepath.Dir(m.dir), "settings", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err = m.CheckReadiness(context.Background(), true)
	if err != nil || report.Ready || report.CheckedAt == "" {
		t.Fatalf("recheck invented readiness: %+v %v", report, err)
	}
	for _, item := range report.Components {
		statuses[item.ID] = item.Status
	}
	if statuses["storage"] != "ready" || statuses["layerfs"] != "missing" || statuses["connection"] != "unknown" {
		t.Fatalf("incorrect probe results: %+v", statuses)
	}
	cached, err := m.CheckReadiness(context.Background(), false)
	if err != nil || cached.CheckedAt != report.CheckedAt {
		t.Fatal("reading readiness changed the last check")
	}
	after, _ := os.ReadFile(filepath.Join(filepath.Dir(m.dir), "settings", "models.json"))
	if string(before) != string(after) {
		t.Fatal("readiness changed private configuration")
	}
	if m.config.NodePath != nodePath {
		t.Fatal("failed readiness check discarded the configured Node path")
	}
}

func TestObservedConnectionBelongsToPrivateModelRevision(t *testing.T) {
	m, _, _, _ := testManager(t)
	firstRevision, _ := m.models.Snapshot()
	m.recordModelDiagnostic(firstRevision, &execution{modelRequested: 1, modelResponded: 1}, nil, nil)
	m.recordModelDiagnostic(firstRevision, &execution{modelRequested: 1}, errors.New("stopped request"), context.Canceled)
	report, err := m.CheckReadiness(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range report.Components {
		if item.ID == "connection" && (item.Status != "ready" || item.LastSuccessAt == "") {
			t.Fatal("actual provider response was not recorded")
		}
	}
	if _, err := m.models.Update(configuration.Update{Model: valuePtr("unverified-model")}); err != nil {
		t.Fatal(err)
	}
	report, err = m.CheckReadiness(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range report.Components {
		if item.ID == "connection" && item.Status != "unknown" {
			t.Fatal("new configuration inherited old provider verification")
		}
	}
}

func TestExecutionDiagnosticWaitsForDurableCompletion(t *testing.T) {
	m, _, _, room := testManager(t)
	started := make(chan storage.Run, 1)
	release := make(chan struct{})
	m.mu.Lock()
	m.unavailable = ""
	m.executeRun = func(ctx context.Context, e *execution) {
		err := m.record(e, "output", "A command is still working.\n")
		started <- e.run
		select {
		case <-release:
		case <-ctx.Done():
		}
		m.finish(e, ctx, nil, "", err)
	}
	m.mu.Unlock()
	run := sendTo(t, m, room, "in-progress-readiness").Runs[0]
	awaitStart(t, started)
	if status := m.diagnostics.Get("execution").Status; status != "" {
		t.Fatalf("progress was mistaken for completed execution: %s", status)
	}
	close(release)
	waitRun(t, m, run.ID)
	if status := m.diagnostics.Get("execution").Status; status != "ready" {
		t.Fatalf("durable completion was not observed: %s", status)
	}
}
