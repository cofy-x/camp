package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/execution"
)

func storageRuntimeFixture(t *testing.T) (string, string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Checkpoint storage tests require the pinned Node toolchain: ", err)
	}
	// This is the entire ordinary Mac package's offline inspection dependency
	// closure. There is deliberately no SDK, Docker executable or worker client.
	module := t.TempDir()
	for _, name := range []string{"storage.mjs", "retention.mjs", "references.mjs", "contracts.mjs", "state.mjs", "inventory.mjs", "storage-state.mjs", "storage-budget.mjs", "cleanup.mjs"} {
		data, err := os.ReadFile(filepath.Join("../../../../tools/layerfs-worker", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(module, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "daemon.json"), []byte(`{"runnerId":"controlled-runner","storageId":"controlled-storage","credential":"must-remain-private","workspaceBackend":"native-local"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "journal.sqlite"), []byte("retained-journal"), 0600); err != nil {
		t.Fatal(err)
	}
	return directory, module, node
}

func TestCheckpointStorageRunsWithoutWorkerAndKeepsMachineOutputClean(t *testing.T) {
	directory, module, node := storageRuntimeFixture(t)
	var output bytes.Buffer
	if err := runCheckpointStorage(t.Context(), directory, node, module, "controlled-runner", "controlled-storage", nil, true, &output); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("JSON must contain only the report: %s: %v", output.String(), err)
	}
	if report["complete"] != true || report["workerConfigured"] != false {
		t.Fatal(report)
	}
	if strings.Contains(output.String(), "must-remain-private") {
		t.Fatal("Credential leaked into output")
	}
	observed := bytes.Clone(output.Bytes())
	data, err := os.ReadFile(filepath.Join(directory, "journal.sqlite"))
	if err != nil || string(data) != "retained-journal" {
		t.Fatalf("Journal changed: %s, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "layerfs-worker")); !os.IsNotExist(err) {
		t.Fatal("Offline inspection created worker state")
	}
	output.Reset()
	if err := writeCheckpointStorage(&output, observed, false); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Checkpoint storage: complete", "0 protected, 0 unreferenced", "not observed", "No data is removed"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("Missing %q in %s", text, output.String())
		}
	}
}

func TestCheckpointStoragePartialReportRemainsReadableAndFails(t *testing.T) {
	data := []byte(`{"schema":1,"kind":"checkpoint-storage-preview","complete":false,"totals":{"logicalBytes":1073741824},"retention":{"blocked":1,"deletionSupported":false},"problems":[{"path":"opaque\nname","code":"InvalidMetadata"}]}`)
	for _, machine := range []bool{false, true} {
		var output bytes.Buffer
		if err := writeCheckpointStorage(&output, data, machine); err == nil {
			t.Fatal("Partial inspection must not count as success")
		}
		if machine && !json.Valid(output.Bytes()) {
			t.Fatal(output.String())
		}
		if !machine && (!strings.Contains(output.String(), "1.0 GiB") || !strings.Contains(output.String(), `"opaque\nname"`)) {
			t.Fatal(output.String())
		}
	}
}

func TestStorageMutationRejectsRunningOwnerAndConflictingFlags(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("The checkpoint CLI targets ARM64 Mac; offline helper and reports are tested on this host separately")
	}
	directory, _, _ := storageRuntimeFixture(t)
	lock, err := execution.AcquireLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	var output bytes.Buffer
	err = runRunnerCheckpoint(t.Context(), []string{"budget", "--data-dir", directory, "--retained-budget", "1GiB", "--json"}, &output)
	if err == nil || !strings.Contains(err.Error(), "Stop this Runner") || output.Len() != 0 {
		t.Fatalf("Running owner was not protected: %v %s", err, output.String())
	}
	for _, args := range [][]string{{"storage", "--data-dir", directory, "--operation", "unexpected"}, {"status", "--data-dir", directory, "--json"}} {
		if err := runRunnerCheckpoint(t.Context(), args, &output); err == nil || !strings.Contains(err.Error(), "Storage accepts") {
			t.Fatal(err)
		}
	}
}

func TestLiveStorageCLIAndStoppedBudgetPolicy(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ARM64 Mac operator CLI; helper is portable")
	}
	directory, module, node := storageRuntimeFixture(t)
	t.Setenv("CAMP_BUNDLE_DIR", "")
	t.Setenv("CAMP_DSH_NODE", node)
	t.Setenv("CAMP_LAYERFS_WORKER_RUNTIME", module)
	lock, err := execution.AcquireLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runRunnerCheckpoint(t.Context(), []string{"storage", "--data-dir", directory, "--json"}, &output)
	lock.Close()
	if err != nil {
		t.Fatal(err, output.String())
	}
	var observed struct {
		Quiescent bool `json:"quiescent"`
		Retention struct {
			PreviewAllowed bool `json:"previewAllowed"`
		} `json:"retention"`
	}
	if err = json.Unmarshal(output.Bytes(), &observed); err != nil || observed.Quiescent || observed.Retention.PreviewAllowed {
		t.Fatal("Live overview exposed cleanup candidates", err, output.String())
	}
	output.Reset()
	if err = runRunnerCheckpoint(t.Context(), []string{"budget", "--data-dir", directory, "--retained-budget", "1GiB", "--min-free", "0", "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Retained int64 `json:"retainedBudgetBytes"`
		Free     int64 `json:"minFreeBytes"`
	}
	if err = json.Unmarshal(output.Bytes(), &policy); err != nil || policy.Retained != 1<<30 || policy.Free != 0 {
		t.Fatal(err, output.String())
	}
	output.Reset()
	if err = runRunnerCheckpoint(t.Context(), []string{"budget", "--data-dir", directory, "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(output.Bytes(), &policy); err != nil || policy.Retained != 1<<30 {
		t.Fatal(err, output.String())
	}
}
