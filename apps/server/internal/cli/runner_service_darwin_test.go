//go:build darwin

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMacBackgroundServiceActualLaunchd(t *testing.T) {
	if os.Getenv("CAMP_TEST_MAC_NATIVE") != "1" {
		t.Skip("requires actual Mac launchd qualification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "camp")
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", binary, "./testdata/mac-service-main.go").CombinedOutput(); err != nil {
		t.Fatalf("child build: %s %v", output, err)
	}
	domain := "user/" + strconv.Itoa(os.Geteuid())
	manager := macRunnerService{domain: domain, directory: directory, loginDirectory: t.TempDir(), loaded: func(ctx context.Context, target string) bool {
		return exec.CommandContext(ctx, "/bin/launchctl", "print", target).Run() == nil
	}, command: func(ctx context.Context, args ...string) error {
		output, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
		if err != nil {
			t.Logf("launchctl %v: %s", args, output)
		}
		return err
	}}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = manager.apply(cleanup, "remove", "")
	})
	if err := manager.apply(ctx, "install", binary); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "service-started")); !os.IsNotExist(err) {
		t.Fatal("install started child", err)
	}
	if err := manager.apply(ctx, "start", ""); err != nil {
		t.Fatal(err)
	}
	wait := func(name string) []byte {
		for {
			data, err := os.ReadFile(filepath.Join(directory, name))
			if err == nil && len(data) > 0 {
				return data
			}
			select {
			case <-ctx.Done():
				t.Fatal("missing service receipt", name)
				return nil
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	first := string(wait("service-started"))
	if err := manager.apply(ctx, "start", ""); err != nil {
		t.Fatal(err)
	}
	if string(wait("service-started")) != first {
		t.Fatal("starting an active service replaced its process")
	}
	if err := manager.apply(ctx, "stop", ""); err != nil {
		t.Fatal(err)
	}
	if string(wait("service-stopped")) != "graceful" {
		t.Fatal("stop did not signal graceful shutdown")
	}
	if manager.loaded(ctx, domain+"/"+macServiceLabel(directory)) {
		t.Fatal("stopped service remained loaded")
	}
}
