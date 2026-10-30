//go:build darwin

package cli

import (
	"os"
	"os/exec"
	"testing"
)

func TestMacSecretTerminalChild(t *testing.T) {
	if os.Getenv("CAMP_TEST_SECRET_CHILD") != "1" {
		return
	}
	if !runnerTerminal(os.Stdin) {
		t.Fatal("PTY was not recognized as an interactive terminal")
	}
	value, err := readConnectionCode(os.Stdin)
	if err != nil || string(value) != "fake-connection-code\n" {
		t.Fatal("interactive line input failed", err)
	}
}

func TestMacConnectionCodeHiddenAndTerminalRestored(t *testing.T) {
	if os.Getenv("CAMP_TEST_MAC_NATIVE") != "1" {
		t.Skip("requires Mac PTY qualification")
	}
	// Keep the slave open in the parent to inspect the restored terminal state.
	program := `import os, pty, select, subprocess, sys, termios, time
master, slave = pty.openpty()
env = dict(os.environ, CAMP_TEST_SECRET_CHILD="1")
p = subprocess.Popen([sys.argv[1], "-test.run=^TestMacSecretTerminalChild$"], stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
deadline = time.monotonic() + 10
try:
    while termios.tcgetattr(slave)[3] & termios.ECHO:
        assert p.poll() is None and time.monotonic() < deadline, "secret reader did not hide input"
        time.sleep(0.01)
    os.write(master, b"fake-connection-code\n")
    out, err = p.communicate(timeout=10)
    assert p.returncode == 0, (out, err)
    assert termios.tcgetattr(slave)[3] & termios.ECHO, "terminal echo was not restored"
    assert not select.select([master], [], [], 0.1)[0], "connection code was echoed"
finally:
    if p.poll() is None:
        p.kill()
        p.wait()
    os.close(master)
    os.close(slave)
`
	if output, err := exec.Command("/usr/bin/python3", "-c", program, os.Args[0]).CombinedOutput(); err != nil {
		t.Fatalf("PTY check: %v %s", err, output)
	}
}
