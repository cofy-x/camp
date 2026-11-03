package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/execution"
)

func TestStoppedSourceChangesPreserveOtherSettingsAndRejectStaleIdentity(t *testing.T) {
	config := Config{DataDir: t.TempDir(), RunnerID: "runner", StorageID: "storage", RegistrationID: "registration", Executor: "fixture", Credential: "private-credential", PendingToken: "private-token", BundleDir: "/private/runtime", Slots: 2, ToolRoots: []string{"/private/tool"}, Sources: map[string]string{"old": "/private/old"}}
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(config.DataDir, "journal-marker")
	if err := os.WriteFile(journal, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveSources(config, map[string]string{"notes": source}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	expected := config
	expected.Sources = map[string]string{"notes": canonical}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("Source update changed another setting")
	}
	stale := config
	stale.StorageID = "foreign"
	if err = SaveSources(stale, nil); err == nil {
		t.Fatal("Stale identity changed sources")
	}
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveSources(got, nil); err == nil {
		lock.Close()
		t.Fatal("Running Runner sources were changed")
	}
	lock.Close()
	for _, invalid := range []map[string]string{{"notes": "relative"}, {"invalid alias": source}, {"private": config.DataDir}, {"ancestor": filepath.Dir(config.DataDir)}} {
		if err = SaveSources(got, invalid); err == nil {
			t.Fatal("Invalid source was accepted", invalid)
		}
	}
	if err = SaveSources(got, nil); err != nil {
		t.Fatal(err)
	}
	got, err = LoadConfig(config.DataDir)
	if err != nil || len(got.Sources) != 0 {
		t.Fatal(got, err)
	}
	bytes, err := os.ReadFile(journal)
	if err != nil || string(bytes) != "retained" {
		t.Fatal("Source update changed journal", err)
	}
}
