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
	ipv6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(int(port))))
	if err != nil {
		t.Fatal("IPv6 control-isolation fixture", err)
	}
	defer ipv6.Close()
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
		cmd = exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile, "/usr/bin/nc", "-6", "-z", "-w", "1", "::1", strconv.Itoa(int(port)))
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work}
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("IPv6 listener inherited an IPv4-only grant: %s", output)
		}
	}
	for _, broad := range []string{"/", "/Users", "/System", "/System/Volumes/Data", "/opt/homebrew"} {
		if _, err := Profile(Policy{Read: []string{broad}}); err == nil {
			t.Fatalf("broad grant admitted: %s", broad)
		}
	}
}

func TestNativeUDPChild(t *testing.T) {
	port := os.Getenv("CAMP_SEATBELT_UDP_PORT")
	if port == "" {
		t.Skip("sandboxed UDP fixture")
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: value})
	if err == nil {
		defer connection.Close()
		_, err = connection.Write([]byte("must be denied"))
	}
	if err == nil {
		t.Fatal("a TCP control grant admitted UDP")
	}
}

func TestNativeControlGrantDeniesUDPAndListingDoesNotReadChildren(t *testing.T) {
	if os.Getenv("CAMP_TEST_MAC_NATIVE") != "1" {
		t.Skip("set CAMP_TEST_MAC_NATIVE=1 on an actual Mac")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := Profile(Policy{Read: []string{binary}, Write: []string{work}, List: []string{root}, Executable: []string{binary}, Ports: []uint16{uint16(udp.LocalAddr().(*net.UDPAddr).Port)}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile, binary, "-test.run=^TestNativeUDPChild$")
	cmd.Dir, cmd.Env = work, []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work, "CAMP_SEATBELT_UDP_PORT=" + strconv.Itoa(udp.LocalAddr().(*net.UDPAddr).Port)}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("UDP denial: %v %s", err, output)
	}
	_ = udp.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, _, err := udp.ReadFromUDP(make([]byte, 64)); err == nil {
		t.Fatal("UDP reached the listener")
	}
	cmd = exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile, "/bin/sh", "-c", "ls "+strconv.Quote(root)+" >/dev/null && ! cat "+strconv.Quote(secret)+" >/dev/null 2>&1")
	cmd.Dir, cmd.Env = work, []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("directory listing read child data: %v %s", err, output)
	}
}
