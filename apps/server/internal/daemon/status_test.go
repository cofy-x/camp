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
	value.ObservedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	data, _ := json.Marshal(value)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if value := read(); value.Connection != "not recently observed" {
		t.Fatal("Old observations were presented as live", value)
	}
}
