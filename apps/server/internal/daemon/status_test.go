package daemon

import (
	"bytes"
	"encoding/json"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusDistinguishesRegistrationConnectionAndReadiness(t *testing.T) {
	dir := t.TempDir()
	config := Config{DataDir: dir, RunnerID: "runner-status", Name: "Studio", ServerURL: "https://camp.example", Executor: "native", Credential: "do-not-display-credential", PendingToken: "do-not-display-code", Sources: map[string]string{"private": "/private/host-source"}}
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	read := func() StatusReport {
		t.Helper()
		var out bytes.Buffer
		if err := Status(dir, &out); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{config.Credential, config.PendingToken, "/private/host-source"} {
			if strings.Contains(out.String(), secret) {
				t.Fatal("Status exposed private information")
			}
		}
		var value StatusReport
		if err := json.Unmarshal(out.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if value := read(); value.Connection != "not observed" || value.Readiness != nil {
		t.Fatal("Registration claimed readiness", value)
	}
	report := p.ReadinessReport{Ready: false, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Checks: []p.ReadinessCheck{{ID: "workspace", Label: "Workspace", Status: "failed", Detail: "Docker unavailable"}}}
	if err := WriteReadiness(dir, report); err != nil {
		t.Fatal(err)
	}
	daemon := Daemon{config: config}
	if err := daemon.recordConnection(true); err != nil {
		t.Fatal(err)
	}
	value := read()
	if value.Connection != "connected" || value.Readiness == nil || value.Readiness.Ready {
		t.Fatal("Connection concealed failed execution", value)
	}
	var record connectionRecord
	if err := readObservation(filepath.Join(dir, "status.json"), &record); err != nil {
		t.Fatal(err)
	}
	record.ObservedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if value := read(); value.Connection != "not recently observed" {
		t.Fatal("Old observations were presented as live", value)
	}
	health := &p.StorageHealth{Schema: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Level: "notice", Warnings: []string{"observation-unavailable"}}
	if err := writeStorageObservation(config, health); err != nil {
		t.Fatal(err)
	}
	if value := read(); value.Storage == nil || value.Readiness == nil || value.Readiness.Ready {
		t.Fatal("Storage advisory changed readiness", value)
	}
	foreign := config
	foreign.StorageID = "different-storage"
	if err := writeStorageObservation(foreign, health); err != nil {
		t.Fatal(err)
	}
	if value := read(); value.Storage != nil {
		t.Fatal("Foreign storage observation was exposed", value)
	}
}

func TestReadinessBindsIdentityConfigurationAndExecution(t *testing.T) {
	dir := t.TempDir()
	config := Config{DataDir: dir, RunnerID: "runner", StorageID: "storage", RegistrationID: "registration", Executor: "native", Slots: 1, BundleDir: "/private/runtime-a", Sources: map[string]string{"notes": "/private/notes"}, ToolRoots: []string{"/private/tool-a"}}
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	report := p.ReadinessReport{Ready: true, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Checks: []p.ReadinessCheck{}}
	if err := WriteBoundReadiness(config, report, "verified-execution-fingerprint"); err != nil {
		t.Fatal(err)
	}
	check := func(state string) {
		t.Helper()
		got, err := ReadStatus(dir)
		if err != nil || got.ReadinessState != state || got.Readiness == nil || !got.Readiness.Ready {
			t.Fatal("Readiness provenance", got, err)
		}
	}
	check("recorded")
	binding := bindObservation(config, "camp-prefix:verified-execution-fingerprint")
	connection := connectionRecord{StatusReport: StatusReport{RunnerID: config.RunnerID, Connection: "connected", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Observed: &ExecutionObservation{State: "current", Capabilities: p.Capabilities{Fingerprint: binding.Fingerprint}}}, Binding: &binding}
	raw, _ := json.Marshal(connection)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	check("current")
	connection.Observed.Capabilities.Fingerprint = "camp-prefix:another-execution"
	raw, _ = json.Marshal(connection)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	check("historical")
	if err := os.Remove(filepath.Join(dir, "status.json")); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.RunnerID = "another-runner" }, func(c *Config) { c.StorageID = "another-storage" }, func(c *Config) { c.RegistrationID = "another-registration" },
		func(c *Config) { c.ToolRoots = []string{"/private/tool-b"} }, func(c *Config) { c.BundleDir = "/private/runtime-b" }, func(c *Config) { c.Sources = map[string]string{"notes": "/private/other"} }, func(c *Config) { c.Slots = 2 },
	} {
		changed := config
		mutate(&changed)
		if err := saveConfig(changed); err != nil {
			t.Fatal(err)
		}
		check("historical")
	}
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(report)
	if err := os.WriteFile(filepath.Join(dir, "readiness.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	check("historical")
	if err := WriteReadiness(dir, report); err != nil {
		t.Fatal(err)
	}
	check("historical")
	if err := WriteBoundReadiness(config, report, "verified-execution-fingerprint"); err != nil {
		t.Fatal(err)
	}
	check("recorded")
}

func TestObservationDoesNotTurnHistoricalExecutionIntoLiveOwnership(t *testing.T) {
	dir := t.TempDir()
	config := Config{DataDir: dir, RunnerID: "runner", StorageID: "storage", RegistrationID: "registration", Executor: "fixture", Slots: 1}
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	d := Daemon{config: config, bootID: "observed-boot"}
	if err := d.recordConnection(true); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatus(dir)
	if err != nil || got.Connection != "connected" || got.Observed == nil || got.Observed.State != "current" || got.Observed.BootID != "observed-boot" {
		t.Fatal(got, err)
	}
	config.StorageID = "replaced-storage"
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	got, err = ReadStatus(dir)
	if err != nil || got.Connection == "connected" || got.Observed.State != "historical" {
		t.Fatal("Foreign observation implied current ownership", got, err)
	}
}

func TestInstallationProvenanceDoesNotChangeResumeFingerprint(t *testing.T) {
	d := Daemon{config: Config{Executor: "fixture", Slots: 1}}
	before := d.capabilities().Fingerprint
	d.config.Installation = &p.Installation{Version: "v0.1.0-alpha.10", SourceCommit: strings.Repeat("a", 40), RuntimeSHA256: strings.Repeat("b", 64)}
	caps := d.capabilities()
	if caps.Installation == nil || caps.Fingerprint != before {
		t.Fatal("Diagnostic provenance changed execution eligibility", caps)
	}
	d.config.Installation.Version = "invalid/private-path"
	if caps = d.capabilities(); caps.Installation != nil || caps.Fingerprint != before {
		t.Fatal("Invalid provenance crossed the report boundary", caps)
	}
}

func TestNativeInitializationCannotReuseAnEarlierReadyObservation(t *testing.T) {
	dir := t.TempDir()
	config := Config{DataDir: dir, RunnerID: "runner", StorageID: "storage", RegistrationID: "registration", Executor: "native", Slots: 1}
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	if err := WriteBoundReadiness(config, p.ReadinessReport{Ready: true, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}, "earlier-runtime"); err != nil {
		t.Fatal(err)
	}
	d := Daemon{config: config, bootID: "new-boot-initializing"}
	if err := d.recordConnection(false); err != nil {
		t.Fatal(err)
	}
	status, err := ReadStatus(dir)
	if err != nil || status.Connection != "disconnected" || status.Observed == nil || status.Observed.BootID != "new-boot-initializing" || status.Observed.Capabilities.Ready || status.ReadinessState != "historical" {
		t.Fatal("Startup reused an earlier ready runtime", status, err)
	}
}
