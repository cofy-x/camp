package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestSetupNeverReplacesRunningRunnerReadiness(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux execution lock")
	}
	directory := t.TempDir()
	if err := daemon.WriteReadiness(directory, p.ReadinessReport{Ready: true, CheckedAt: "saved-observation"}); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(directory, "readiness.json")
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := execution.AcquireLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = saveRunnerReadiness(context.Background(), directory, nativeexecutor.Config{}, errors.New("Image unavailable")); err == nil {
		t.Fatal("Setup replaced an active Runner's observations")
	}
	after, err := os.ReadFile(filename)
	if err != nil || string(after) != string(before) {
		t.Fatalf("Readiness changed while the Runner held its journal lock: %s %v", after, err)
	}
}
