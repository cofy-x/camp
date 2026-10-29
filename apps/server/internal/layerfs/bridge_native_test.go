package layerfs

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBridgeNativeLifecycle(t *testing.T) {
	runtimeDir := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME")
	if runtimeDir == "" {
		t.Skip("Set CAMP_TEST_LAYERFS_RUNTIME to the installed LayerFS runtime directory")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	original := bytes.Repeat([]byte{0, 1, 2, 128, 254, 255}, 10000)
	if err := os.WriteFile(filepath.Join(source, "binary.dat"), original, 0600); err != nil {
		t.Fatal(err)
	}
	dependency := filepath.Join(source, "node_modules/.pnpm/example@1.0.0/node_modules/example")
	if err := os.MkdirAll(dependency, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dependency, "index.js"), []byte("module.exports = 42;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".pnpm/example@1.0.0/node_modules/example", filepath.Join(source, "node_modules/example")); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"native-probe","scripts":{"test":"node -e \"if(require('example')!==42) throw new Error('dependency failed')\""}}`
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	bridge, err := OpenBridge(ctx, BridgeConfig{RuntimeDir: runtimeDir, DataRoot: root, Home: filepath.Join(root, "home"), MaxArchiveBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	var projectID string
	var workspaces []Workspace
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		for _, workspace := range workspaces {
			if err := bridge.CloseWorkspace(cleanupCtx, workspace.WorkspaceID, true); err != nil {
				t.Error(err)
			}
		}
		if projectID != "" {
			if err := bridge.RemoveProject(cleanupCtx, projectID); err != nil {
				t.Error(err)
			}
		}
		if err := bridge.Close(); err != nil {
			t.Error(err)
		}
	})
	prepared, err := bridge.Prepare(ctx, PrepareRequest{SourceDir: source, BaselineKey: "bridge-native-binary-v1"})
	if err != nil {
		t.Fatal(err)
	}
	projectID = prepared.ProjectID
	for _, runID := range []string{"writer", "reader"} {
		workspace, err := bridge.Create(ctx, CreateRequest{ProjectID: projectID, RunID: runID})
		if err != nil {
			t.Fatal(err)
		}
		workspaces = append(workspaces, workspace)
	}
	if workspaces[0].ContainerName == workspaces[1].ContainerName || workspaces[0].UID <= 0 || workspaces[0].GID <= 0 {
		t.Fatal("Runs did not receive distinct non-root Sandbox identities")
	}
	docker, err := NewDocker("/var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer docker.Close()
	firstID, err := docker.Resolve(ctx, workspaces[0].ContainerName)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := docker.Resolve(ctx, workspaces[1].ContainerName)
	if err != nil {
		t.Fatal(err)
	}
	user := fmt.Sprintf("%d:%d", workspaces[0].UID, workspaces[0].GID)
	var output bytes.Buffer
	code, err := docker.Exec(ctx, firstID, user, []string{"/bin/sh", "-c", "cd \"$1\" && test \"$(pnpm --version)\" = 12.11.2 && pnpm run test", "camp-native-test", workspaces[0].MountPath}, &output, &output)
	if err != nil || code != 0 {
		t.Fatalf("pnpm or imported relative dependency symlink failed: %d %v %s", code, err, output.String())
	}
	code, err = docker.Exec(ctx, firstID, user, []string{"/bin/sh", "-c", "printf 'A short memo.\\n' > \"$1/memo.md\"", "camp-native-test", workspaces[0].MountPath}, &output, &output)
	if err != nil || code != 0 {
		t.Fatalf("write failed: %d %v %s", code, err, output.String())
	}
	code, err = docker.Exec(ctx, secondID, user, []string{"/bin/test", "!", "-e", workspaces[1].MountPath + "/memo.md"}, &output, &output)
	if err != nil || code != 0 {
		t.Fatalf("workspace write escaped into another Run: %d %v", code, err)
	}
	checkpoint, err := bridge.Checkpoint(ctx, workspaces[0].WorkspaceID, "Save the native bridge memo")
	if err != nil || checkpoint.Kind != "Committed" || checkpoint.Head == "" {
		t.Fatalf("checkpoint failed: %+v %v", checkpoint, err)
	}
	archive, err := bridge.ExportWithPolicy(ctx, workspaces[0].WorkspaceID, filepath.Join(root, "result.tar"), []string{"node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if archive.Bytes != int64(len(data)) || archive.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("archive receipt does not match its bytes")
	}
	reader := tar.NewReader(bytes.NewReader(data))
	entries := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			entries[header.Name], err = io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if !bytes.Equal(entries["./binary.dat"], original) || string(entries["./memo.md"]) != "A short memo.\n" {
		t.Fatal("export changed file bytes")
	}
	if _, ok := entries["./node_modules/.pnpm/example@1.0.0/node_modules/example/index.js"]; ok {
		t.Fatal("environment-managed dependencies were included in the resource export")
	}
	if _, err := os.Stat(filepath.Join(source, "memo.md")); !os.IsNotExist(err) {
		t.Fatal("the source directory was modified")
	}
	if _, err := bridge.Export(ctx, workspaces[0].WorkspaceID, archive.ArchivePath); err == nil {
		t.Fatal("export overwrote an existing archive")
	}
	unchanged, err := os.ReadFile(archive.ArchivePath)
	if err != nil || !bytes.Equal(data, unchanged) {
		t.Fatal("a rejected export removed or changed the existing archive")
	}
	code, err = docker.Exec(ctx, firstID, user, []string{"/bin/sh", "-c", "head -c 262144 /dev/zero > \"$1/large.bin\"", "camp-native-test", workspaces[0].MountPath}, &output, &output)
	if err != nil || code != 0 {
		t.Fatalf("large output fixture failed: %d %v", code, err)
	}
	failedArchive := filepath.Join(root, "over-budget.tar")
	_, err = bridge.ExportWithPolicy(ctx, workspaces[0].WorkspaceID, failedArchive, []string{"node_modules"})
	var limit *BridgeError
	if !errors.As(err, &limit) || limit.Code != "ArchiveLimit" {
		t.Fatalf("oversize archive was not rejected: %v", err)
	}
	if _, err := os.Stat(failedArchive); !os.IsNotExist(err) {
		t.Fatal("a partial archive survived the failed export")
	}
	if err := docker.Stop(ctx, firstID, 0); err != nil {
		t.Fatalf("failed export Sandbox could not be stopped: %v", err)
	}
}

func TestBridgeNativeResetDiscardsCrashStateAndPreservesOtherHome(t *testing.T) {
	runtimeDir := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME")
	if runtimeDir == "" {
		t.Skip("Set CAMP_TEST_LAYERFS_RUNTIME to the installed LayerFS runtime directory")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	configA := BridgeConfig{RuntimeDir: runtimeDir, DataRoot: root, Home: filepath.Join(root, "home-a")}
	configB := BridgeConfig{RuntimeDir: runtimeDir, DataRoot: root, Home: filepath.Join(root, "home-b")}
	a, err := OpenBridge(ctx, configA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenBridge(ctx, configB)
	if err != nil {
		_ = a.Close()
		t.Fatal(err)
	}
	var projectA, projectB string
	var workspaceB Workspace
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if workspaceB.WorkspaceID != "" {
			if err := b.CloseWorkspace(cleanupCtx, workspaceB.WorkspaceID, true); err != nil {
				t.Error(err)
			}
		}
		if projectB != "" {
			if err := b.RemoveProject(cleanupCtx, projectB); err != nil {
				t.Error(err)
			}
		}
		_ = b.Close()
		_ = a.Close()
		// Reopen without Store access and finish cleanup if an assertion failed.
		fresh, err := OpenBridge(cleanupCtx, configA)
		if err != nil {
			t.Error(err)
			return
		}
		if err := fresh.Reset(cleanupCtx); err != nil {
			t.Error(err)
		}
		_ = fresh.Close()
	})
	for index, bridge := range []*Bridge{a, b} {
		prepared, err := bridge.Prepare(ctx, PrepareRequest{SourceDir: source, BaselineKey: "same-baseline-different-home"})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			projectA = prepared.ProjectID
		} else {
			projectB = prepared.ProjectID
		}
	}
	if projectA != projectB {
		t.Fatal("the test must exercise identical project IDs in different private homes")
	}
	first, err := a.Create(ctx, CreateRequest{ProjectID: projectA, RunID: "receipt-lost"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Create(ctx, CreateRequest{ProjectID: projectA, RunID: "daemon-stopped"})
	if err != nil {
		t.Fatal(err)
	}
	workspaceB, err = b.Create(ctx, CreateRequest{ProjectID: projectB, RunID: "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	docker, err := NewDocker("/var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer docker.Close()
	secondID, err := docker.Resolve(ctx, second.ContainerName)
	if err != nil {
		t.Fatal(err)
	}
	if err := docker.Stop(ctx, secondID, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(configA.Home, ".camp", "workspaces", first.WorkspaceID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := a.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	a, err = OpenBridge(ctx, configA)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := docker.Resolve(ctx, first.ContainerName); err == nil {
		t.Fatal("startup reset retained the Sandbox with no Camp receipt")
	}
	if _, err := docker.Resolve(ctx, workspaceB.ContainerName); err != nil {
		t.Fatalf("startup reset affected another private home: %v", err)
	}
	records, err := a.Workspaces(ctx)
	if err != nil || len(records) != 0 {
		t.Fatalf("startup reset retained workspace receipts: %+v %v", records, err)
	}
	prepared, err := a.Prepare(ctx, PrepareRequest{SourceDir: source, BaselineKey: "same-baseline-different-home"})
	if err != nil || prepared.Reused {
		t.Fatalf("startup reset reused the previous Disposable Store: %+v %v", prepared, err)
	}
	if err := a.Reset(ctx); err == nil {
		t.Fatal("reset was allowed after project admission")
	}
}
