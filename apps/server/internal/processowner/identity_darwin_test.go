//go:build darwin

package processowner

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOwnedJobHelper(t *testing.T) {
	dir := os.Getenv("CAMP_PROCESS_TEST")
	if dir == "" {
		t.Skip("owned subprocess fixture")
	}
	role := os.Getenv("CAMP_PROCESS_ROLE")
	if role == "root" {
		id, err := Observe(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(id)
		if err := os.WriteFile(filepath.Join(dir, "root.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if role == "root" || role == "intermediate" {
		child := exec.Command(os.Args[0], "-test.run=^TestOwnedJobHelper$")
		next := "intermediate"
		if role == "intermediate" {
			next = "detached"
		}
		child.Env = []string{"PATH=/usr/bin:/bin", "CAMP_PROCESS_TEST=" + dir, "CAMP_PROCESS_ROLE=" + next}
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		if role == "intermediate" {
			return // the detached child is reparented to launchd
		}
		if err := child.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if role == "detached" {
		signal.Ignore(syscall.SIGTERM)
		id, err := Observe(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(id)
		if err := os.WriteFile(filepath.Join(dir, "detached.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		_ = os.Stdin.Close()
		_ = os.Stdout.Close()
		_ = os.Stderr.Close()
	}
	for range 3000 {
		if role == "root" {
			var parent Identity
			data, readErr := os.ReadFile(filepath.Join(dir, "parent.json"))
			if readErr == nil && json.Unmarshal(data, &parent) == nil {
				actual, observeErr := Observe(parent.PID)
				if observeErr != nil || actual.UniqueID != parent.UniqueID {
					self, err := Observe(os.Getpid())
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err = (Owner{Root: self}).Terminate(ctx)
					cancel()
					if err != nil {
						t.Fatal(err)
					}
					return
				}
			}
		}
		_ = os.WriteFile(filepath.Join(dir, role+".heartbeat"), []byte(time.Now().String()), 0600)
		time.Sleep(10 * time.Millisecond)
	}
}

func launchOwnedJob(t *testing.T) (Identity, Identity, string) {
	t.Helper()
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	escape := func(s string) string {
		var out strings.Builder
		if err := xml.EscapeText(&out, []byte(s)); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	label := fmt.Sprintf("dev.cofy.camp.probe.%d.%s", os.Getpid(), filepath.Base(dir))
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if err := exec.Command("/bin/launchctl", "print", domain).Run(); err != nil {
		domain = fmt.Sprintf("user/%d", os.Getuid())
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + escape(label) + `</string><key>ProgramArguments</key><array><string>` + escape(binary) + `</string><string>-test.run=^TestOwnedJobHelper$</string></array><key>EnvironmentVariables</key><dict><key>CAMP_PROCESS_TEST</key><string>` + escape(dir) + `</string><key>CAMP_PROCESS_ROLE</key><string>root</string><key>PATH</key><string>/usr/bin:/bin</string></dict><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>StandardOutPath</key><string>` + escape(filepath.Join(dir, "stdout")) + `</string><key>StandardErrorPath</key><string>` + escape(filepath.Join(dir, "stderr")) + `</string></dict></plist>`
	name := filepath.Join(dir, "job.plist")
	if err := os.WriteFile(name, []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/launchctl", "bootstrap", domain, name).CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("/bin/launchctl", "bootout", domain+"/"+label).Run() })
	var root, child Identity
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rootData, rootErr := os.ReadFile(filepath.Join(dir, "root.json"))
		childData, childErr := os.ReadFile(filepath.Join(dir, "detached.json"))
		if rootErr == nil && childErr == nil && json.Unmarshal(rootData, &root) == nil && json.Unmarshal(childData, &child) == nil {
			return root, child, dir
		}
		time.Sleep(20 * time.Millisecond)
	}
	logs, _ := os.ReadFile(filepath.Join(dir, "stdout"))
	errors, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	t.Fatalf("job did not report identities: %s %s", logs, errors)
	return root, child, dir
}

func TestLaunchdCoalitionOwnsDetachedChildren(t *testing.T) {
	if os.Getenv("CAMP_TEST_MAC_NATIVE") != "1" {
		t.Skip("set CAMP_TEST_MAC_NATIVE=1 on an actual Mac")
	}
	self, err := Observe(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	first, detached, _ := launchOwnedJob(t)
	other, otherDetached, _ := launchOwnedJob(t)
	if first.Coalition == self.Coalition || first.Coalition == other.Coalition || detached.Coalition != first.Coalition || otherDetached.Coalition != other.Coalition {
		t.Fatalf("not independent kernel ownership: self=%+v first=%+v detached=%+v other=%+v other detached=%+v", self, first, detached, other, otherDetached)
	}
	// A stale PID generation must be refused by the kernel, not merely by a
	// prior user-space observation. SIGCONT is harmless even if this proof fails.
	stale := other
	stale.Version++
	if err := Signal(stale, int(unix.SIGCONT)); !errors.Is(err, unix.ESRCH) {
		t.Fatalf("stale PID generation: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := (Owner{Root: first}).Terminate(ctx); err != nil {
		t.Fatal(err)
	}
	if remaining, err := Members(first); err != nil || len(remaining) != 0 {
		t.Fatalf("unconfirmed cleanup: %+v %v", remaining, err)
	}
	for _, expected := range []Identity{self, other, otherDetached} {
		actual, err := Observe(expected.PID)
		if err != nil || actual.UniqueID != expected.UniqueID {
			t.Fatalf("unrelated process changed: %+v %+v %v", expected, actual, err)
		}
	}
	if err := (Owner{Root: other}).Terminate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("independent launchd coalitions contain double-fork/setsid children; generation-bound signals and exact cleanup passed")
}

func TestOwnedJobCleansAfterParentDeath(t *testing.T) {
	if os.Getenv("CAMP_TEST_MAC_NATIVE") != "1" {
		t.Skip("set CAMP_TEST_MAC_NATIVE=1 on an actual Mac")
	}
	parent := exec.Command("/bin/sleep", "30")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	id, err := Observe(parent.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	root, detached, dir := launchOwnedJob(t)
	self, err := Observe(os.Getpid())
	if err != nil || root.Coalition == self.Coalition || detached.Coalition != root.Coalition {
		t.Fatal("the job lacks independent ownership", err)
	}
	data, _ := json.Marshal(id)
	if err := os.WriteFile(filepath.Join(dir, "parent.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		members, err := Members(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(members) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a detached child outlived its watched parent")
}
