//go:build darwin

package seatbelt

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestActualNativeReadWriteAndControlPorts(t *testing.T) {
	if os.Getenv("CAMP_TEST_MAC_NATIVE") != "1" {
		t.Skip("set CAMP_TEST_MAC_NATIVE=1 on an actual Mac")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work, reference, private := filepath.Join(dir, "work[1]"), filepath.Join(dir, "reference"), filepath.Join(dir, "private")
	for _, directory := range []string{work, reference, private} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{filepath.Join(reference, "read"), filepath.Join(private, "secret")} {
		if err := os.WriteFile(name, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(private, "secret"), filepath.Join(work, "outside")); err != nil {
		t.Fatal(err)
	}
	policy := Policy{Read: []string{reference}, Write: []string{work}, Executable: []string{work}}
	profile, err := Profile(policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	script := "set -eu; cat " + strconv.Quote(filepath.Join(reference, "read")) + " >/dev/null; printf native > result; if cat " + strconv.Quote(filepath.Join(private, "secret")) + " >/dev/null 2>&1; then exit 11; fi; if cat outside >/dev/null 2>&1; then exit 12; fi; if printf bad > " + strconv.Quote(filepath.Join(reference, "read")) + " 2>/dev/null; then exit 13; fi"
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile, "/bin/bash", "-c", script)
	cmd.Dir, cmd.Env = work, []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native file boundary: %v %s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	for _, allowed := range []bool{false, true} {
		policy.Ports = nil
		if allowed {
			policy.Ports = []uint16{port}
		}
		profile, err := Profile(policy)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile, "/usr/bin/nc", "-z", "-w", "1", "127.0.0.1", strconv.Itoa(int(port)))
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work}
		output, err := cmd.CombinedOutput()
		if (err == nil) != allowed {
			t.Fatalf("loopback allowed=%v: %v %s", allowed, err, output)
		}
	}
	for _, broad := range []string{"/", "/Users", "/System", "/System/Volumes/Data", "/opt/homebrew"} {
		if _, err := Profile(Policy{Read: []string{broad}}); err == nil {
			t.Fatalf("broad grant admitted: %s", broad)
		}
	}
}
