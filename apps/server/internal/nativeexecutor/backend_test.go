package nativeexecutor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

type refusedWorkspace struct {
	closed, captures int
	closeErr         error
}

func (*refusedWorkspace) Directory() string { return "/workspace" }
func (*refusedWorkspace) Failure() error    { return errors.New("workspace setup failed") }
func (*refusedWorkspace) Call(context.Context, sandboxexec.Request, func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
	return sandboxexec.Frame{}, errors.New("no tool was admitted")
}
func (w *refusedWorkspace) Capture(context.Context, string, string, []string) (workspace.Archive, error) {
	w.captures++
	return workspace.Archive{}, errors.New("a failed workspace must not be captured")
}
func (w *refusedWorkspace) Close(context.Context) error { w.closed++; return w.closeErr }

type refusedProvider struct{ workspace *refusedWorkspace }

func (p *refusedProvider) Prepare(context.Context, workspace.Spec) (workspace.Instance, error) {
	return p.workspace, errors.New("workspace setup failed")
}
func (*refusedProvider) SelfTest(context.Context) p.ReadinessReport { return p.ReadinessReport{} }
func (*refusedProvider) Fingerprint() string                        { return "isolated-test-runtime" }
func (*refusedProvider) Ready() error                               { return nil }
func (*refusedProvider) Close() error                               { return nil }

func TestBackendFailureCannotCaptureOrPublishAndCleanupIsIndependent(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "known-cleanup", true: "unknown-cleanup"}[uncertain], func(t *testing.T) {
			directory := t.TempDir()
			seed := filepath.Join(directory, "project")
			if err := os.Mkdir(seed, 0700); err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(seed, "original")
			if err := os.WriteFile(original, []byte("immutable input"), 0600); err != nil {
				t.Fatal(err)
			}
			backend := &refusedWorkspace{}
			if uncertain {
				backend.closeErr = errors.New("descendant cleanup is unknown")
			}
			executor := &Executor{config: Config{DataDir: directory}, backend: &refusedProvider{workspace: backend}}
			result := executor.Execute(context.Background(), Request{Directory: directory, AttemptID: "test-attempt", SessionID: "test-session", Access: "write", ModelURL: "http://unused", ModelToken: "synthetic-attempt-capability"}, nil)
			if result.Status == "succeeded" || result.ArchivePath != "" || backend.captures != 0 || backend.closed != 1 {
				t.Fatalf("failed workspace escaped lifecycle: %+v %+v", result, backend)
			}
			if result.CleanupConfirmed == uncertain || (executor.Ready() != nil) != uncertain {
				t.Fatalf("cleanup classification: %+v %v", result, executor.Ready())
			}
			data, err := os.ReadFile(original)
			if err != nil || string(data) != "immutable input" {
				t.Fatal("the frozen input changed", err)
			}
		})
	}
}
