package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/execution"
)

func TestStoppedWorkspaceSelectionPreservesIdentityJournalAndRetainedData(t *testing.T) {
	backend := "layerfs"
	if runtime.GOOS == "darwin" {
		backend = "layerfs-worker"
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no native backend on this test platform")
	}
	root := t.TempDir()
	config := Config{DataDir: root, ServerURL: "https://camp.example", RunnerID: "controlled-runner", StorageID: "controlled-storage", RegistrationID: "controlled-registration", Credential: "controlled-credential", Name: "Controlled identity", Executor: "native", Slots: 2, RuntimeSource: "server", BundleDir: "/fixed/runtime", DockerSocket: "/controlled/docker.sock", DockerBinary: "/controlled/docker", Sources: map[string]string{"project": "/controlled/source"}, ToolRoots: []string{"/controlled/tool"}}
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}
	journal, err := openJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.db.Close(); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(root, "retained-native-capture")
	if err := os.WriteFile(retained, []byte("original complete-tree fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "daemon.json" {
			continue
		}
		bytes, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		before[entry.Name()] = bytes
	}
	if err := SaveWorkspaceBackend(config, backend); err != nil {
		t.Fatal(err)
	}
	updated, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	want := config
	want.WorkspaceBackend = backend
	if !reflect.DeepEqual(updated, want) {
		t.Fatal("workspace selection changed enrollment or installation fields")
	}
	for name, expected := range before {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(body) != string(expected) {
			t.Fatal("workspace selection changed retained data", name, err)
		}
	}
	stale := config
	stale.StorageID = "different-storage"
	if err := SaveWorkspaceBackend(stale, ""); err == nil {
		t.Fatal("a stale identity changed the backend")
	}
	lock, err := execution.AcquireLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveWorkspaceBackend(config, ""); err == nil {
		t.Fatal("an active Runner allowed a backend change")
	}
	_ = lock.Close()
	if err := SaveWorkspaceBackend(config, ""); err != nil {
		t.Fatal(err)
	}
	updated, err = LoadConfig(root)
	if err != nil || !reflect.DeepEqual(updated, config) {
		t.Fatal("returning to the baseline lost identity or configuration", err)
	}
}
