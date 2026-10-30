//go:build darwin

package nativeexecutor

import (
	"archive/tar"
	"bytes"
	"context"
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
