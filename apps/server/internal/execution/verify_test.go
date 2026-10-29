//go:build linux

package execution

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestOutputFailureStopsProcess(t *testing.T) {
	expected := errors.New("storage unavailable")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.Command("/bin/sh", "-c", "printf 'event\\n'; exec sleep 30")
	started := time.Now()
	_, err := RunTests(ctx, command, func(string) error { return expected })
	if !errors.Is(err, expected) || time.Since(started) > time.Second {
		t.Fatalf("Output failure did not stop promptly: %v", err)
	}
}

func TestCancellationReapsCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.Command("/bin/sh", "-c", "printf 'ready\\n'; exec sleep 30")
	_, err := RunTests(ctx, command, func(string) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) || command.ProcessState == nil {
		t.Fatalf("Cancellation did not reap process: %v", err)
	}
}

func TestInvalidDependencyMountRejected(t *testing.T) {
	for _, path := range []string{"../node_modules", "/node_modules", "packages/../../node_modules", "packages\\node_modules"} {
		_, err := ProjectCommand(ProjectConfig{NodePath: "/usr/bin/node", ProjectDir: t.TempDir(), Arguments: []string{"/node", "--version"}, Dependencies: []Mount{{Source: t.TempDir(), Target: path}}}, nil)
		if err == nil || !strings.Contains(err.Error(), "mount") {
			t.Fatalf("Invalid mount %q accepted: %v", path, err)
		}
	}
}
