//go:build darwin

package nativejob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/processowner"
	"github.com/cofy-x/camp/apps/server/internal/seatbelt"
)

func nativeFixture(t *testing.T) (string, string, Spec) {
	t.Helper()
	if os.Getenv("CAMP_TEST_MAC_NATIVE") != "1" {
		t.Skip("set CAMP_TEST_MAC_NATIVE=1 on an actual Mac")
	}
	helper := os.Getenv("CAMP_MAC_HELPER")
	if !filepath.IsAbs(helper) {
		t.Fatal("CAMP_MAC_HELPER must identify the built trusted helper")
	}
	root := t.TempDir()
	work := filepath.Join(root, "work[1]")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	spec := Spec{Argv: []string{"/bin/sh", "-c", "uname -s; uname -m; printf native > result"}, Directory: work, Env: []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work}, Policy: seatbelt.Policy{Read: []string{work}, Write: []string{work}}, TimeoutMS: 10000}
	return helper, filepath.Join(root, "private-jobs"), spec
}

func assertQuiescent(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := Reconcile(ctx, root); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "owner.json"))
		if err != nil {
			t.Fatal(err)
		}
		var owner processowner.Identity
		if json.Unmarshal(data, &owner) != nil {
			t.Fatal("invalid owner")
		}
		if members, err := processowner.Members(owner); err != nil || len(members) != 0 {
			t.Fatalf("live writers: %+v %v", members, err)
		}
	}
}

func TestNativeCommandReadWriteNetworkAndReceipts(t *testing.T) {
	helper, root, spec := nativeFixture(t)
	private := filepath.Join(filepath.Dir(spec.Directory), "unrelated-secret")
	if err := os.WriteFile(private, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	spec.Argv = []string{"/bin/sh", "-c", `uname -s; uname -m; printf native > result; if cat "$1"; then exit 4; fi; if /usr/bin/nc -G 1 -w 1 127.0.0.1 "$2"; then exit 5; fi`, "camp", private, port}
	var output bytes.Buffer
	result, err := Run(context.Background(), helper, root, spec, strings.NewReader(""), &output, &output)
	if err != nil || !result.Clean || result.Code != 0 || !strings.Contains(output.String(), "Darwin\narm64") || strings.Contains(output.String(), "synthetic") {
		t.Fatalf("native result: %+v %v %s", result, err, output.String())
	}
	data, err := os.ReadFile(filepath.Join(spec.Directory, "result"))
	if err != nil || string(data) != "native" {
		t.Fatal("owned write failed", err)
	}
	assertQuiescent(t, root)
}

func TestNativeTreeFixture(t *testing.T) {
	work := os.Getenv("CAMP_NATIVE_TREE")
	if work == "" {
		t.Skip("owned command fixture")
	}
	role := os.Getenv("CAMP_NATIVE_ROLE")
	if role == "intermediate" || role == "root" {
		next := "detached"
		if role == "root" {
			next = "intermediate"
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestNativeTreeFixture$", "-test.timeout=60s")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "CAMP_NATIVE_TREE=" + work, "CAMP_NATIVE_ROLE=" + next}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if role == "intermediate" {
			return
		}
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(work, "child.pid")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if os.Getenv("CAMP_NATIVE_HOLD") == "" {
			return
		}
	}
	if role == "detached" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
		if err := os.WriteFile(filepath.Join(work, "child.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
		_ = os.Stdin.Close()
		_ = os.Stdout.Close()
		_ = os.Stderr.Close()
	}
	for range 5000 {
		_ = os.WriteFile(filepath.Join(work, "heartbeat"), []byte(time.Now().String()), 0600)
		time.Sleep(10 * time.Millisecond)
	}
}

func treeSpec(t *testing.T, spec Spec, hold bool) Spec {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec.Argv = []string{binary, "-test.run=^TestNativeTreeFixture$", "-test.timeout=60s"}
	spec.Env = append(spec.Env, "CAMP_NATIVE_TREE="+spec.Directory, "CAMP_NATIVE_ROLE=root")
	if hold {
		spec.Env = append(spec.Env, "CAMP_NATIVE_HOLD=1")
	}
	spec.Policy.Read = append(spec.Policy.Read, binary)
	spec.Policy.Executable = []string{binary}
	return spec
}

func TestNativeNormalExitAndTimeoutRemoveDetachedWriters(t *testing.T) {
	for _, hold := range []bool{false, true} {
		t.Run(fmt.Sprint(hold), func(t *testing.T) {
			helper, root, spec := nativeFixture(t)
			spec = treeSpec(t, spec, hold)
			if hold {
				spec.TimeoutMS = 3000
			}
			var output bytes.Buffer
			result, err := Run(context.Background(), helper, root, spec, strings.NewReader(""), &output, &output)
			if err != nil || !result.Clean || (result.Code == 0) == hold {
				t.Fatalf("tree result: %+v %v %s", result, err, output.String())
			}
			if _, err := os.Stat(filepath.Join(spec.Directory, "child.pid")); err != nil {
				t.Fatalf("detached writer never started: %+v %v %s", result, err, output.String())
			}
			assertQuiescent(t, root)
		})
	}
}

func TestNativeClientDeathCleansBeforeAnyRecoveryReceipt(t *testing.T) {
	helper, root, spec := nativeFixture(t)
	spec = treeSpec(t, spec, true)
	cmd, dir, err := Command(helper, root, spec)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(spec.Directory, "child.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(filepath.Join(dir, "owner.json"))
	if err != nil {
		t.Fatal(err)
	}
	var owner processowner.Identity
	if json.Unmarshal(data, &owner) != nil {
		t.Fatal("owner missing")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	deadline = time.Now().Add(8 * time.Second)
	for {
		members, err := processowner.Members(owner)
		if err != nil {
			t.Fatal(err)
		}
		if len(members) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("writers survived client death: %+v %s", members, output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := Reconcile(context.Background(), root); err == nil {
		t.Fatal("lost result became confirmed success")
	}
	// A killed client cannot unregister the nonpersistent launchd job itself.
	data, _ = os.ReadFile(filepath.Join(dir, "launch.json"))
	var launch Spec
	_ = json.Unmarshal(data, &launch)
	_ = exec.Command("/bin/launchctl", "bootout", launch.Domain+"/"+launch.Label).Run()
}

func TestNativeOutputBoundAndCancellation(t *testing.T) {
	helper, root, spec := nativeFixture(t)
	spec.Argv = []string{"/usr/bin/yes", "bounded"}
	spec.MaxOutput = 1024
	var output bytes.Buffer
	result, err := Run(context.Background(), helper, root, spec, strings.NewReader(""), &output, io.Discard)
	if err != nil || !result.Truncated || !result.Clean || output.Len() > 1024 {
		t.Fatalf("output result: %+v %v %d", result, err, output.Len())
	}
	assertQuiescent(t, root)
	spec.Argv = []string{"/bin/sleep", "30"}
	spec.MaxOutput = 1024
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err = Run(ctx, helper, root, spec, strings.NewReader(""), io.Discard, io.Discard)
	if err != nil || !result.Clean || result.Code == 0 {
		t.Fatalf("cancel result: %+v %v", result, err)
	}
	assertQuiescent(t, root)
}
