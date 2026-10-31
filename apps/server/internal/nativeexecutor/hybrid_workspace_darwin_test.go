//go:build darwin

package nativeexecutor

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

func hybridProvider(t *testing.T) (*hybridWorkspaceProvider, context.Context, string, string) {
	t.Helper()
	worker, dsh, socket, helper := os.Getenv("CAMP_TEST_LAYERFS_WORKER_RUNTIME"), os.Getenv("CAMP_TEST_DSH_RUNTIME"), os.Getenv("CAMP_TEST_DOCKER_SOCKET"), os.Getenv("CAMP_TEST_SANDBOX_HELPER")
	if worker == "" || dsh == "" || socket == "" || helper == "" {
		t.Skip("Set private actual-Mac worker, DSH, Docker socket and helper test inputs")
	}
	base := filepath.Join(worker, "..", "..", ".data")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "native-worker-test-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Retained private native worker fixture:", root)
	data, tool := filepath.Join(root, "runner"), filepath.Join(root, "approved-tool")
	if err := os.MkdirAll(filepath.Join(tool, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tool, "bin", "camp-probe"), []byte("#!/bin/sh\n/usr/bin/uname -sm\nexec /bin/sh -c \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	config, err := normalizeConfig(Config{DataDir: data, RuntimeDir: dsh, SandboxHelper: helper, NodePath: "node", DockerSocket: socket, WorkspaceBackend: "layerfs-worker", WorkerRuntimeDir: worker, RunnerID: "controlled-native-runner", StorageID: "controlled-native-storage", ToolRoots: []string{tool}})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := openHybridWorkspace(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	return provider.(*hybridWorkspaceProvider), ctx, root, data
}

func TestHybridNativeToolBoundaryAndCompleteCheckpoint(t *testing.T) {
	t.Setenv("CAMP_PRIVATE_MODEL_TEST", "controlled-never-forward")
	provider, ctx, root, data := hybridProvider(t)
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	if report := provider.SelfTest(ctx); !report.Ready {
		t.Fatalf("native plus actual worker readiness: %+v", report)
	}
	source := filepath.Join(data, "source")
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "HEAD"), []byte("private checkpoint membership"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "memo.txt"), []byte("immutable original"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "sibling-secret")
	if err := os.WriteFile(secret, []byte("controlled private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	active, err := provider.Prepare(ctx, workspace.Spec{Source: source, AttemptID: "native-command", NamespaceKey: digest("controlled-hybrid-workspace")})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close(context.Background())
	control := filepath.Join(data, "layerfs-worker", "control", "worker.json")
	denials := fmt.Sprintf("set -e; test -z \"$CAMP_PRIVATE_MODEL_TEST\"; if cat %q >/dev/null 2>&1; then exit 81; fi; if cat %q >/dev/null 2>&1; then exit 82; fi; node -e %s", secret, control, shellArgument(`const n=require('net');const s=n.connect({path:`+fmt.Sprintf("%q", os.Getenv("CAMP_TEST_DOCKER_SOCKET"))+`});s.on('connect',()=>process.exit(83));s.on('error',()=>process.exit(0));setTimeout(()=>process.exit(84),2000);`))
	var observed strings.Builder
	frame, err := active.Call(ctx, sandboxexec.Request{Operation: "command", Command: "camp-probe " + shellArgument(denials), TimeoutMS: 10_000, MaxOutputBytes: 8192}, func(frame sandboxexec.Frame) error {
		if frame.Type == "stdout" {
			_, _ = observed.Write(frame.Data)
		}
		return nil
	})
	if err != nil || frame.Code != float64(0) || !frame.Clean {
		t.Fatalf("approved native tool and descendant boundary: %+v %v", frame, err)
	}
	if !strings.Contains(observed.String(), "Darwin arm64") {
		t.Fatal("approved toolchain did not actually execute on the ARM64 Mac", observed.String())
	}
	readOnlyToolFile := filepath.Join(root, "approved-tool", "readonly-fixture")
	if err := os.WriteFile(readOnlyToolFile, []byte("read-only approved tool data"), 0600); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("if ln %q external-alias 2>/dev/null; then if printf forbidden > external-alias 2>/dev/null; then exit 85; fi; rm external-alias; fi", readOnlyToolFile)
	frame, err = active.Call(ctx, sandboxexec.Request{Operation: "command", Command: command, TimeoutMS: 10_000, MaxOutputBytes: 8192}, nil)
	if err != nil || frame.Code != float64(0) || !frame.Clean {
		t.Fatalf("read-only grant hardlink alias denial: %+v %v", frame, err)
	}
	if bytes, err := os.ReadFile(readOnlyToolFile); err != nil || string(bytes) != "read-only approved tool data" {
		t.Fatal("a hardlink alias mutated a read-only tool grant", err)
	}
	frame, err = active.Call(ctx, sandboxexec.Request{Operation: "command", Command: "printf changed > memo.txt; ln memo.txt twin.txt; mkdir -p node_modules/pkg empty; printf dependency > node_modules/pkg/index.js; ln -s pkg node_modules/alias", TimeoutMS: 10_000, MaxOutputBytes: 8192}, nil)
	if err != nil || frame.Code != float64(0) || !frame.Clean {
		t.Fatalf("native writes: %+v %v", frame, err)
	}
	archive, err := active.Capture(ctx, filepath.Join(data, "result.tar"), "full tree then publication archive", []string{"node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(archive.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(header.Name, ".git") || strings.HasPrefix(header.Name, "node_modules") {
			t.Fatal("publication archive included private checkpoint-only membership", header.Name)
		}
		seen[header.Name] = true
	}
	if !seen["memo.txt"] || !seen["twin.txt"] {
		t.Fatal("publication archive lost accepted files", seen)
	}
	entries, err := os.ReadDir(filepath.Join(data, "layerfs-worker", "checkpoints", "operations"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(data, "layerfs-worker", "checkpoints", "operations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			State       string
			CaptureRoot string
			Binding     struct{ AttemptID string }
		}
		if err := json.Unmarshal(body, &record); err != nil {
			t.Fatal(err)
		}
		if record.Binding.AttemptID != "native-command" {
			continue
		}
		manifest, err := os.ReadFile(filepath.Join(record.CaptureRoot, "manifest.json"))
		if err != nil || record.State != "confirmed" || !strings.Contains(string(manifest), `".git/HEAD"`) || !strings.Contains(string(manifest), `"node_modules/alias"`) || !strings.Contains(string(manifest), `"type":"hardlink"`) {
			t.Fatal("full working-tree checkpoint was incomplete", string(manifest), err)
		}
		found = true
	}
	if !found {
		t.Fatal("native attempt has no complete-tree receipt")
	}
	if bytes, err := os.ReadFile(filepath.Join(source, "memo.txt")); err != nil || string(bytes) != "immutable original" {
		t.Fatal("the original source changed", err)
	}
	if _, err := active.Call(ctx, sandboxexec.Request{Operation: "write", Path: "/workspace/late", Data: []byte("late")}, nil); err == nil {
		t.Fatal("checkpoint did not seal native tool admission")
	}
}

func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func TestHybridFailedCaptureRetainsNativeDirectoryWithoutPublication(t *testing.T) {
	provider, ctx, _, data := hybridProvider(t)
	defer provider.Close()
	source := filepath.Join(data, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	active, err := provider.Prepare(ctx, workspace.Spec{Source: source, AttemptID: "special-entry", NamespaceKey: digest("special-entry")})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := active.Call(ctx, sandboxexec.Request{Operation: "command", Command: "printf retained > user-work.txt; mkfifo unsupported-fifo", TimeoutMS: 10_000, MaxOutputBytes: 8192}, nil)
	if err != nil || frame.Code != float64(0) || !frame.Clean {
		t.Fatalf("fixture: %+v %v", frame, err)
	}
	if _, err := active.Capture(ctx, filepath.Join(data, "result.tar"), "must retain", nil); err == nil {
		t.Fatal("special file checkpoint was accepted")
	}
	if _, err := os.Stat(filepath.Join(data, "result.tar")); !os.IsNotExist(err) {
		t.Fatal("failed checkpoint produced a publishable archive", err)
	}
	if provider.Ready() == nil {
		t.Fatal("the failed backend still admitted execution")
	}
	if err := active.Close(ctx); err != nil {
		t.Fatal(err)
	}
	retained, err := filepath.Glob(filepath.Join(data, "native-recovery", "checkpoint-*", "workspace-state", "workspace", "user-work.txt"))
	if err != nil || len(retained) != 1 {
		t.Fatal("native work was not retained", retained, err)
	}
	if body, err := os.ReadFile(retained[0]); err != nil || string(body) != "retained" {
		t.Fatal("retained native work changed", err)
	}
}
