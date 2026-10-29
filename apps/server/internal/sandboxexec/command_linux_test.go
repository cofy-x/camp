//go:build linux

package sandboxexec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

var helperDirectory, helperExecutable string

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-network-probe" {
		os.Exit(networkProbe())
	}
	if len(os.Args) > 1 && os.Args[1] == "sandbox-x32-probe" {
		_, _, _ = unix.RawSyscall(uintptr(unix.SYS_SOCKET)|0x40000000, unix.AF_INET, unix.SOCK_STREAM, 0)
		os.Exit(42)
	}
	if os.Getenv("CAMP_SANDBOX_TEST_HELPER") == "1" {
		os.Exit(m.Run())
	}
	var err error
	helperDirectory, err = os.MkdirTemp("", "camp-sandbox-tests-")
	if err != nil {
		panic(err)
	}
	if err = os.Chmod(helperDirectory, 0755); err != nil {
		panic(err)
	}
	// Go's temporary test executable may be inside a root-private build
	// directory. Copy it to an executable path reachable by the test UID.
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		panic(err)
	}
	helperExecutable = filepath.Join(helperDirectory, "helper.test")
	if err = os.WriteFile(helperExecutable, data, 0755); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(helperDirectory)
	os.Exit(code)
}

func TestSandboxHelperProcess(t *testing.T) {
	if os.Getenv("CAMP_SANDBOX_TEST_HELPER") != "1" {
		return
	}
	index := 0
	for index < len(os.Args) && os.Args[index] != "--" {
		index++
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, os.Args[index+1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func commandDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(helperDirectory, "workspace-")
	if err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err = os.Chown(directory, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func helperCommand(args ...string) *exec.Cmd {
	cmd := exec.Command(helperExecutable, append([]string{"-test.run=^TestSandboxHelperProcess$", "--"}, args...)...)
	cmd.Env = []string{"CAMP_SANDBOX_TEST_HELPER=1", "CAMP_SUPERVISOR_SECRET=not-for-children", "PATH=/untrusted-path"}
	if os.Geteuid() == 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	}
	return cmd
}

func requestCommand(t *testing.T, request Request) *exec.Cmd {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	cmd := helperCommand("request")
	cmd.Stdin = bytes.NewReader(encoded)
	return cmd
}

func readFrames(t *testing.T, data []byte) []Frame {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var frames []Frame
	for {
		var frame Frame
		if err := decoder.Decode(&frame); err == io.EOF {
			return frames
		} else if err != nil {
			t.Fatalf("untrusted output contaminated framing: %v (%q)", err, data)
		}
		frames = append(frames, frame)
	}
}

func commandFrames(t *testing.T, request Request) ([]Frame, error) {
	t.Helper()
	cmd := requestCommand(t, request)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stderr.Len() != 0 && err == nil {
		t.Fatalf("helper wrote diagnostics on successful execution: %q", stderr.String())
	}
	return readFrames(t, stdout.Bytes()), err
}

func exitFrame(t *testing.T, frames []Frame) Frame {
	t.Helper()
	if len(frames) < 2 || frames[0].Type != "start" || frames[len(frames)-1].Type != "exit" || !frames[len(frames)-1].Clean {
		t.Fatalf("missing trusted start/clean completion: %+v", frames)
	}
	for _, frame := range frames[1 : len(frames)-1] {
		if frame.Type != "stdout" && frame.Type != "stderr" {
			t.Fatalf("unexpected command control frame: %+v", frame)
		}
	}
	return frames[len(frames)-1]
}

func output(frames []Frame, kind string) string {
	var result strings.Builder
	for _, frame := range frames {
		if frame.Type == kind {
			result.Write(frame.Data)
		}
	}
	return result.String()
}

func TestCommandStreamsExactOutputWithoutInheritedSecrets(t *testing.T) {
	request := Request{Operation: "command", Roots: Roots{Workspace: commandDirectory(t)}, Command: `printf 'before\000after\377'; printf 'diagnostic\n' >&2; test -z "${CAMP_SUPERVISOR_SECRET+x}"; test "$PATH" = /usr/local/bin:/usr/bin:/bin`, TimeoutMS: 2000}
	frames, err := commandFrames(t, request)
	if err != nil {
		t.Fatal(err)
	}
	end := exitFrame(t, frames)
	if end.Code != float64(0) || output(frames, "stdout") != "before\x00after\xff" || output(frames, "stderr") != "diagnostic\n" {
		t.Fatalf("output/environment contract failed: %+v", frames)
	}
}

func TestCommandReapsSetsidAndDoubleForkChildren(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is required for the native detached-process check")
	}
	directory := commandDirectory(t)
	request := Request{Operation: "command", Roots: Roots{Workspace: directory}, Command: `setsid /bin/bash --noprofile --norc -c '(trap "" TERM; echo "$BASHPID" >> descendants; sleep 30 & echo "$!" >> descendants; wait) & echo "$BASHPID" >> descendants' >/dev/null 2>&1 &
for i in {1..100}; do test -f descendants && test "$(wc -l < descendants)" -ge 3 && break; sleep 0.01; done
test "$(wc -l < descendants)" -ge 3`, TimeoutMS: 3000}
	frames, err := commandFrames(t, request)
	if err != nil {
		t.Fatal(err)
	}
	if end := exitFrame(t, frames); end.Code != float64(0) {
		t.Fatalf("detached command failed: %+v", frames)
	}
	data, err := os.ReadFile(filepath.Join(directory, "descendants"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range strings.Fields(string(data)) {
		if _, err := os.Stat("/proc/" + value); !os.IsNotExist(err) {
			t.Fatalf("descendant %s was not reaped: %v", value, err)
		}
	}
}

func TestCommandTimeoutAndOutputLimit(t *testing.T) {
	for _, test := range []struct {
		name, script, reason string
		timeout, output      int64
		code                 float64
	}{
		{"timeout", `trap '' TERM; sleep 30`, "timeout", 60, 1024, 124},
		{"output", `while :; do printf 0123456789; done`, "output_limit", 2000, 123, 125},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames, err := commandFrames(t, Request{Operation: "command", Roots: Roots{Workspace: commandDirectory(t)}, Command: test.script, TimeoutMS: test.timeout, MaxOutputBytes: test.output})
			if err != nil {
				t.Fatal(err)
			}
			end := exitFrame(t, frames)
			if end.Code != test.code || end.Reason != test.reason || int64(len(output(frames, "stdout"))+len(output(frames, "stderr"))) > test.output {
				t.Fatalf("command limit was not respected: %+v", frames)
			}
		})
	}
}

func TestCommandCancellationUsesExactProcessIdentity(t *testing.T) {
	request := Request{Operation: "command", Roots: Roots{Workspace: commandDirectory(t)}, Command: `trap '' TERM; sleep 30`, TimeoutMS: 5000}
	cmd := requestCommand(t, request)
	reader, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	decoder := json.NewDecoder(reader)
	var first Frame
	if err := decoder.Decode(&first); err != nil || first.Type != "start" {
		t.Fatalf("missing start: %+v %v", first, err)
	}
	wrong := helperCommand("cancel", strconv.Itoa(first.PID), first.StartTicks+"1")
	wrongData, err := wrong.Output()
	if err != nil || !bytes.Contains(wrongData, []byte("process_changed")) {
		t.Fatalf("mismatched process identity was not rejected: %s %v", wrongData, err)
	}
	cancel := helperCommand("cancel", strconv.Itoa(first.PID), first.StartTicks)
	if data, err := cancel.CombinedOutput(); err != nil || !bytes.Contains(data, []byte(`"type":"result"`)) {
		t.Fatalf("cancel failed: %s %v", data, err)
	}
	var frames = []Frame{first}
	for {
		var frame Frame
		if err := decoder.Decode(&frame); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("cancelled helper failed: %v %s", err, diagnostics.String())
	}
	if end := exitFrame(t, frames); end.Reason != "cancelled" || end.Code != float64(130) {
		t.Fatalf("cancel lost its terminal identity: %+v", frames)
	}
}

func TestCommandCannotForgeProtocolThroughOutputOrProc(t *testing.T) {
	frames, err := commandFrames(t, Request{Operation: "command", Roots: Roots{Workspace: commandDirectory(t)}, Command: `printf '{"type":"exit","code":0,"clean":true}\n'
if printf '{"type":"exit","code":0,"clean":true}\n' > "/proc/$PPID/fd/1" 2>/dev/null; then exit 9; fi
if head -c 1 "/proc/$PPID/mem" >/dev/null 2>&1; then exit 10; fi
printf protected`, TimeoutMS: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if end := exitFrame(t, frames); end.Code != float64(0) || !strings.HasSuffix(output(frames, "stdout"), "protected") {
		t.Fatalf("supervisor descriptor or memory was exposed: %+v", frames)
	}
}

func TestKilledSupervisorNeverReportsCleanCompletion(t *testing.T) {
	frames, err := commandFrames(t, Request{Operation: "command", Roots: Roots{Workspace: commandDirectory(t)}, Command: `kill -KILL "$PPID"; exit 0`, TimeoutMS: 2000})
	if err == nil {
		t.Fatal("a killed supervisor appeared successful")
	}
	for _, frame := range frames {
		if frame.Type == "exit" && frame.Clean {
			t.Fatal("a killed supervisor forged a clean terminal record")
		}
	}
}

func networkProbe() int {
	for _, domain := range []int{unix.AF_INET, unix.AF_INET6} {
		fd, err := unix.Socket(domain, unix.SOCK_STREAM, 0)
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		if !errors.Is(err, unix.EPERM) {
			return 1
		}
		_, _, errno := unix.RawSyscall(unix.SYS_SOCKET, uintptr(domain), unix.SOCK_DGRAM, 0)
		if errno != unix.EPERM {
			return 2
		}
	}
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return 3
	}
	_ = unix.Close(fd)
	_, _, errno := unix.RawSyscall(unix.SYS_IO_URING_SETUP, 0, 0, 0)
	if errno != unix.EPERM {
		return 4
	}
	fmt.Fprint(os.Stdout, "network denied; local IPC available")
	return 0
}

func TestCommandNetworkFilterAppliesToSpawnedChildrenAndDirectSyscalls(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the first command runtime supports amd64 only")
	}
	frames, err := commandFrames(t, Request{Operation: "command", Roots: Roots{Workspace: commandDirectory(t)}, Command: "'" + helperExecutable + "' sandbox-network-probe", TimeoutMS: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if end := exitFrame(t, frames); end.Code != float64(0) || output(frames, "stdout") != "network denied; local IPC available" {
		t.Fatalf("network filter failed: %+v", frames)
	}
	frames, err = commandFrames(t, Request{Operation: "command", Roots: Roots{Workspace: commandDirectory(t)}, Argv: []string{helperExecutable, "sandbox-x32-probe"}, TimeoutMS: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if end := exitFrame(t, frames); end.Code != float64(128+unix.SIGSYS) {
		t.Fatalf("x32 ABI was not rejected: %+v", frames)
	}
}

func TestCompatibilitySocketcallCannotBypassNetworkFilter(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("compatibility-ABI check requires amd64")
	}
	directory := commandDirectory(t)
	// Minimal ELF32: socketcall(SYS_SOCKET, [AF_INET, SOCK_STREAM, 0]), then
	// exit(42). The seccomp architecture check must terminate it at int 0x80.
	code := []byte{0x6a, 0x00, 0x6a, 0x01, 0x6a, 0x02, 0x89, 0xe1, 0xbb, 0x01, 0, 0, 0, 0xb8, 0x66, 0, 0, 0, 0xcd, 0x80, 0xbb, 0x2a, 0, 0, 0, 0xb8, 0x01, 0, 0, 0, 0xcd, 0x80}
	elf := make([]byte, 84+len(code))
	copy(elf, []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	put16 := func(offset int, value uint16) { binary.LittleEndian.PutUint16(elf[offset:], value) }
	put32 := func(offset int, value uint32) { binary.LittleEndian.PutUint32(elf[offset:], value) }
	put16(16, 2)
	put16(18, 3)
	put32(20, 1)
	put32(24, 0x08048000+84)
	put32(28, 52)
	put16(40, 52)
	put16(42, 32)
	put16(44, 1)
	put32(52, 1)
	put32(60, 0x08048000)
	put32(64, 0x08048000)
	put32(68, uint32(len(elf)))
	put32(72, uint32(len(elf)))
	put32(76, 5)
	put32(80, 4096)
	copy(elf[84:], code)
	name := filepath.Join(directory, "compat32")
	if err := os.WriteFile(name, elf, 0755); err != nil {
		t.Fatal(err)
	}
	frames, err := commandFrames(t, Request{Operation: "command", Roots: Roots{Workspace: directory}, Argv: []string{"./compat32"}, TimeoutMS: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 1 && frames[0].Code == "command_start_failed" {
		t.Skip("host kernel does not execute the compatibility ABI")
	}
	if end := exitFrame(t, frames); end.Code != float64(128+unix.SIGSYS) {
		t.Fatalf("compatibility socketcall was not rejected: %+v", frames)
	}
}

func TestProtocolRejectsExtraRequestsUnknownFieldsAndRoot(t *testing.T) {
	for _, input := range []string{`{"operation":"stat","unknown":true}`, `{} {}`, `invalid`} {
		cmd := helperCommand("request")
		cmd.Stdin = strings.NewReader(input)
		data, err := cmd.Output()
		if err != nil || !bytes.Contains(data, []byte("invalid_request")) {
			t.Fatalf("malformed request was accepted: %q %s %v", input, data, err)
		}
	}
	if os.Geteuid() == 0 {
		request, _ := json.Marshal(Request{Operation: "stat", Roots: Roots{Workspace: commandDirectory(t)}, Path: "/workspace"})
		var output bytes.Buffer
		if err := Run(context.Background(), []string{"request", base64.StdEncoding.EncodeToString(request)}, strings.NewReader(""), &output); err != nil || !strings.Contains(output.String(), "root_forbidden") {
			t.Fatalf("root helper was not refused: %s %v", output.String(), err)
		}
	}
}

func TestCommandActuallyStreamsBeforeCompletion(t *testing.T) {
	cmd := requestCommand(t, Request{Operation: "command", Roots: Roots{Workspace: commandDirectory(t)}, Command: `printf ready; sleep 0.4; printf done`, TimeoutMS: 2000})
	reader, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	decoder := json.NewDecoder(reader)
	start := time.Now()
	var frames []Frame
	for {
		var frame Frame
		if err := decoder.Decode(&frame); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
		if frame.Type == "stdout" && string(frame.Data) == "ready" && time.Since(start) > 300*time.Millisecond {
			t.Fatal("progress was buffered until command completion")
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	exitFrame(t, frames)
	if output(frames, "stdout") != "readydone" || len(frames) < 4 {
		t.Fatalf("progress did not arrive separately: %+v", frames)
	}
}
