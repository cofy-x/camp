package storage

import (
	"encoding/json"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"testing"
)

func TestStorageAdvisoryPreservesReadinessAndFencesConnections(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	runner, join := enrollTestRunner(t, s, owner, "disk")
	n := int64(123)
	health := &p.StorageHealth{Schema: 1, ObservedAt: "2026-11-01T07:00:00Z", Level: "warning", Warnings: []string{"retained-budget-exceeded"}, LogicalBytes: &n, FreeBytes: &n, Complete: true}
	if err := s.SetRunnerStorageHealth(ctx, runner.ID, runner.ConnectionGeneration, health); err != nil {
		t.Fatal(err)
	}
	observed, err := s.GetRunner(ctx, runner.ID)
	if err != nil {
		t.Fatal(err)
	}
	var caps p.Capabilities
	if err = json.Unmarshal(observed.Capabilities, &caps); err != nil {
		t.Fatal(err)
	}
	if !caps.Ready || caps.Fingerprint != "v1" || caps.Storage == nil || caps.Storage.Level != "warning" {
		t.Fatal(string(observed.Capabilities))
	}
	old := *health
	old.ObservedAt = "2026-11-01T06:00:00Z"
	old.Level = "notice"
	old.Warnings = []string{"observation-incomplete"}
	if err = s.SetRunnerStorageHealth(ctx, runner.ID, runner.ConnectionGeneration, &old); err != nil {
		t.Fatal(err)
	}
	observed, _ = s.GetRunner(ctx, runner.ID)
	json.Unmarshal(observed.Capabilities, &caps)
	if caps.Storage.ObservedAt != health.ObservedAt {
		t.Fatal("Older report displaced current observation")
	}
	newer, err := s.ConnectRunner(ctx, runner.ID, RunnerConnection{StorageID: join.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.SetRunnerStorageHealth(ctx, runner.ID, runner.ConnectionGeneration, health), "stale_connection")
	health.Warnings = []string{"invalid"}
	requireCode(t, s.SetRunnerStorageHealth(ctx, runner.ID, newer.ConnectionGeneration, health), "invalid_runner")
}
