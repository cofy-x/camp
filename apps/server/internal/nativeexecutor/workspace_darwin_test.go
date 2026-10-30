//go:build darwin

package nativeexecutor

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

func nativeProvider(t *testing.T) (*nativeWorkspaceProvider, context.Context, string) {
	t.Helper()
	dsh := os.Getenv("CAMP_TEST_DSH_RUNTIME")
	if dsh == "" {
		t.Skip("set CAMP_TEST_DSH_RUNTIME on an actual Mac")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "camp-sandbox")
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", helper, "../../cmd/camp-sandbox").CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, output)
	}
	config, err := normalizeConfig(Config{DataDir: root, RuntimeDir: dsh, SandboxHelper: helper, NodePath: "node"})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := openNativeWorkspace(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	return provider.(*nativeWorkspaceProvider), ctx, root
}

func TestNativeWorkspaceCloneCaptureAndImmutableOriginal(t *testing.T) {
	provider, ctx, root := nativeProvider(t)
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	binary := bytes.Repeat([]byte{0, 255, 128, 1}, 8192)
	if err := os.WriteFile(filepath.Join(source, "binary.bin"), binary, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "script.sh"), []byte("original"), 0700); err != nil {
		t.Fatal(err)
	}
	active, err := provider.Prepare(ctx, workspace.Spec{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close(context.Background())
	frame, err := active.Call(ctx, sandboxexec.Request{Operation: "command", Command: "printf changed > script.sh; mv binary.bin renamed.bin", TimeoutMS: 10000, MaxOutputBytes: 4096}, nil)
	if err != nil || frame.Code != float64(0) || !frame.Clean {
		t.Fatalf("native mutation: %+v %v", frame, err)
	}
	if data, err := os.ReadFile(filepath.Join(source, "script.sh")); err != nil || string(data) != "original" {
		t.Fatal("the cloned source changed", err)
	}
	if data, err := os.ReadFile(filepath.Join(source, "binary.bin")); err != nil || !bytes.Equal(data, binary) {
		t.Fatal("renaming the clone changed the original", err)
	}
	archive, err := active.Capture(ctx, filepath.Join(root, "result.tar"), "native capture", nil)
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
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		switch header.Name {
		case "script.sh":
			if string(data) != "changed" || header.Mode != 0755 {
				t.Fatal("executable mode/content changed", header, string(data))
			}
		case "renamed.bin":
			if !bytes.Equal(data, binary) {
				t.Fatal("binary bytes changed")
			}
		default:
			t.Fatal("unexpected captured path", header.Name)
		}
		seen[header.Name] = true
	}
	if len(seen) != 2 {
		t.Fatal("capture lost files", seen)
	}
	if _, err := active.Call(ctx, sandboxexec.Request{Operation: "write", Path: "/workspace/late", Data: []byte("late")}, nil); err == nil {
		t.Fatal("capture did not seal tool admission")
	}
}

func TestNativeWorkspaceRejectsUnsafeCaptureAndUnknownCleanup(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "symlink", true: "unknown-job"}[unknown], func(t *testing.T) {
			provider, ctx, root := nativeProvider(t)
			source := filepath.Join(root, "source")
			_ = os.Mkdir(source, 0700)
			active, err := provider.Prepare(ctx, workspace.Spec{Source: source})
			if err != nil {
				t.Fatal(err)
			}
			w := active.(*nativeWorkspace)
			if unknown {
				if err := os.MkdirAll(filepath.Join(w.jobs, "job-unknown"), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink(root, filepath.Join(w.directory, "outside")); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(root, "unsafe.tar")
			if _, err := active.Capture(ctx, target, "refuse", nil); err == nil {
				t.Fatal("unsafe capture was accepted")
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("a failed capture retained a publishable archive", err)
			}
			err = active.Close(ctx)
			if (err != nil) != unknown || (provider.Ready() != nil) != unknown {
				t.Fatal("cleanup uncertainty was lost", err, provider.Ready())
			}
			if unknown {
				if _, err := os.Stat(w.root); err != nil {
					t.Fatal("unknown cleanup deleted its receipts", err)
				}
			}
		})
	}
}

func TestNativeReadOnlyWorkspaceRejectsCommandsAndWrites(t *testing.T) {
	provider, ctx, root := nativeProvider(t)
	source := filepath.Join(root, "source")
	_ = os.Mkdir(source, 0700)
	_ = os.WriteFile(filepath.Join(source, "read.txt"), []byte("read"), 0600)
	active, err := provider.Prepare(ctx, workspace.Spec{Source: source, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close(context.Background())
	frame, err := active.Call(ctx, sandboxexec.Request{Operation: "read", Path: "/workspace/read.txt"}, nil)
	if err != nil || string(frame.Data) != "read" {
		t.Fatal("read-only read failed", frame, err)
	}
	for _, request := range []sandboxexec.Request{{Operation: "command", Command: "true"}, {Operation: "write", Path: "/workspace/read.txt", Data: []byte("changed")}} {
		if _, err := active.Call(ctx, request, nil); err == nil {
			t.Fatal("read-only authority expanded", request.Operation)
		}
	}
}

func TestNativeToolGrantAllowsExecutionWithoutHostCredentialsOrToolWrites(t *testing.T) {
	provider, ctx, root := nativeProvider(t)
	tool, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(tool, "approved-native-tool")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\ntest -z \"${CAMP_TEST_HOST_SECRET+x}\" || exit 10\nif cat \"$1\" >/dev/null 2>&1; then exit 11; fi\nif touch \"$0.changed\" 2>/dev/null; then exit 12; fi\nprintf 'native tool ran\\n' > native-result.txt\n"), 0700); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(root, "private-credential")
	if err := os.WriteFile(credential, []byte("host-only"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMP_TEST_HOST_SECRET", "host-only")
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	active, err := provider.Prepare(ctx, workspace.Spec{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close(context.Background())
	request := sandboxexec.Request{Operation: "command", Argv: []string{executable, credential}, TimeoutMS: 10000, MaxOutputBytes: 4096}
	frame, err := active.Call(ctx, request, nil)
	if err != nil || frame.Code == float64(0) {
		t.Fatal("an unapproved executable was admitted", frame, err)
	}
	provider.config.ToolRoots, err = ResolveToolRoots(root, []string{tool})
	if err != nil {
		t.Fatal(err)
	}
	request.Argv[0] = "approved-native-tool"
	frame, err = active.Call(ctx, request, nil)
	if err != nil || frame.Code != float64(0) || !frame.Clean {
		t.Fatal("approved native tool failed", frame, err)
	}
	data, err := os.ReadFile(filepath.Join(active.Directory(), "native-result.txt"))
	if err != nil || string(data) != "native tool ran\n" {
		t.Fatal("tool did not use this workspace", err)
	}
}

func TestNativeMacHostDoesNotRequireDockerOrFuse(t *testing.T) {
	if os.Getenv("CAMP_TEST_DSH_RUNTIME") == "" {
		t.Skip("requires native Mac qualification")
	}
	report := HostChecks(Config{DockerSocket: "/nonexistent/camp-docker.sock"})
	for _, check := range report.Checks {
		if check.Status != "passed" {
			t.Fatal("native Mac prerequisite failed", check)
		}
		if check.ID == "docker" || check.ID == "fuse" || check.ID == "bubblewrap" || check.ID == "root" {
			t.Fatal("Mac acquired a Linux prerequisite", check)
		}
	}
}

func TestApprovedMacGoToolchainBuildsAndRunsNativeMachO(t *testing.T) {
	provider, ctx, root := nativeProvider(t)
	compiler, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	compiler, err = filepath.EvalSymlinks(compiler)
	if err != nil {
		t.Fatal(err)
	}
	provider.config.ToolRoots, err = ResolveToolRoots(root, []string{filepath.Dir(filepath.Dir(compiler))})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "go-source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\nimport \"runtime\"\nfunc main(){println(runtime.GOOS+\"/\"+runtime.GOARCH)}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	active, err := provider.Prepare(ctx, workspace.Spec{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close(context.Background())
	frame, err := active.Call(ctx, sandboxexec.Request{Operation: "command", Command: "CGO_ENABLED=0 GOTOOLCHAIN=local go build -o native-tool main.go && ./native-tool > native-tool.txt 2>&1", TimeoutMS: 40000, MaxOutputBytes: 4096}, nil)
	if err != nil || frame.Code != float64(0) || !frame.Clean {
		t.Fatal("approved native compilation failed", frame, err)
	}
	data, err := os.ReadFile(filepath.Join(active.Directory(), "native-tool.txt"))
	if err != nil || string(data) != "darwin/arm64\n" {
		t.Fatal("toolchain did not execute a native Apple Silicon binary", string(data), err)
	}
}

// Local acceptance uses fresh test storage, not an enrolled Runner. It retains
// the production command PATH, sandbox, native owner and capture boundary.
func TestMacOperatorToolAcceptance(t *testing.T) {
	command := os.Getenv("CAMP_MAC_ACCEPTANCE_COMMAND")
	if command == "" {
		t.Skip("use scripts/qualify-native-mac.mjs --command for an operator tool")
	}
	provider, ctx, root := nativeProvider(t)
	var roots []string
	if err := json.Unmarshal([]byte(os.Getenv("CAMP_MAC_ACCEPTANCE_TOOL_ROOTS")), &roots); err != nil {
		t.Fatal(err)
	}
	var err error
	provider.config.ToolRoots, err = ResolveToolRoots(root, roots)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "operator-source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	active, err := provider.Prepare(ctx, workspace.Spec{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := active.Close(context.Background()); err != nil {
			t.Error("operator workspace cleanup", err)
		}
	}()
	frame, err := active.Call(ctx, sandboxexec.Request{Operation: "command", Command: command, TimeoutMS: 40000, MaxOutputBytes: 4096}, nil)
	if err != nil || frame.Code != float64(0) || !frame.Clean {
		t.Fatal("operator command failed", frame, err)
	}
	if _, err := active.Capture(ctx, filepath.Join(root, "operator-capture.tar"), "operator acceptance", nil); err != nil {
		t.Fatal("operator capture failed", err)
	}
}
