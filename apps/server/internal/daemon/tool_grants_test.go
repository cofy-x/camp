package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/execution"
)

func TestChangingToolGrantsRetainsIdentityAndRequiresStoppedRunner(t *testing.T) {
	config := Config{DataDir: t.TempDir(), ServerURL: "https://camp.example", RunnerID: "saved-runner", StorageID: "saved-storage", RegistrationID: "saved-registration", Credential: "saved-credential", Executor: "native"}
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(config.DataDir, "journal.sqlite")
	if err := os.WriteFile(journal, []byte("retain journal"), 0600); err != nil {
		t.Fatal(err)
	}
	tool, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveToolRoots(config, []string{tool}); err == nil {
		t.Fatal("running Runner grants changed")
	}
	_ = lock.Close()
	if err := SaveToolRoots(config, []string{tool}); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadConfig(config.DataDir)
	if err != nil || !reflect.DeepEqual(saved.ToolRoots, []string{tool}) {
		t.Fatal("tool grants were not saved", saved.ToolRoots, err)
	}
	saved.ToolRoots = nil
	if !reflect.DeepEqual(saved, config) {
		t.Fatal("tool grants changed enrollment or private configuration")
	}
	stale := config
	stale.StorageID = "replaced-storage"
	if err := SaveToolRoots(stale, nil); err == nil {
		t.Fatal("a stale setup changed a different identity")
	}
	if err := SaveToolRoots(config, nil); err != nil {
		t.Fatal(err)
	}
	saved, err = LoadConfig(config.DataDir)
	if err != nil || len(saved.ToolRoots) != 0 {
		t.Fatal("revoked grants remained", err)
	}
	if data, err := os.ReadFile(journal); err != nil || string(data) != "retain journal" {
		t.Fatal("grant changes modified the journal", err)
	}
}
