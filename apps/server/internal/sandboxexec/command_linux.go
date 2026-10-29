//go:build linux

package sandboxexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func validateIdentity() error {
	if os.Geteuid() == 0 || os.Getuid() == 0 {
		return problem("root_forbidden", "The sandbox helper must run as an unprivileged user.")
	}
	// The supervised command shares the UID but must not reopen the helper's
	// stdout or memory through /proc and forge trusted framing. The caller must
	// also drop container capabilities, prohibit privilege gain, and protect the
	// helper executable. A same-UID child can still kill us; missing clean exit
	// therefore always requires container destruction by the host.
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return problem("supervision_unavailable", "Could not protect the supervisor process.")
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return problem("supervision_unavailable", "Could not disable privilege gain.")
	}
	return nil
}

// procIdentity only reads kernel identity fields, never child-controlled cmdline
// text. A process name may contain spaces and parentheses, so split after its
// final closing parenthesis rather than treating stat as ordinary whitespace.
func procIdentity(pid int) (parent int, started string, err error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, "", err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return 0, "", errors.New("invalid process identity")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return 0, "", errors.New("incomplete process identity")
	}
	parent, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", err
	}
	return parent, fields[19], nil
}

func cancelProcess(pidText, startTicks string) error {
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 1 || startTicks == "" {
		return problem("invalid_process", "A supervisor PID and its start time are required.")
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return problem("process_unavailable", "The supervisor process is no longer available.")
	}
	defer unix.Close(fd)
	_, actual, err := procIdentity(pid)
	if err != nil || actual != startTicks {
		return problem("process_changed", "The process identity changed; no signal was sent.")
	}
	if err = unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0); err != nil {
		return problem("process_unavailable", "The supervisor process could not be signalled.")
	}
	return nil
}

type outputBudget struct {
	mu        sync.Mutex
	remaining int64
	frames    int
	limited   bool
	err       error
	cancel    context.CancelFunc
	writer    *frameWriter
}

func (b *outputBudget) consume(kind string, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limited || b.err != nil {
		return
	}
	if b.frames >= 4096 {
		b.limited = true
		b.cancel()
		return
	}
	if int64(len(data)) > b.remaining {
		data = data[:b.remaining]
		b.limited = true
		b.cancel()
	}
	if len(data) > 0 {
		b.frames++
		b.remaining -= int64(len(data))
		b.err = b.writer.write(Frame{Type: kind, Data: data})
		if b.err != nil {
			b.cancel()
		}
	}
}

func (b *outputBudget) copy(kind string, source *os.File) {
	buffer := make([]byte, 16<<10)
	for {
		n, err := source.Read(buffer)
		if n > 0 {
			b.consume(kind, buffer[:n])
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				b.mu.Lock()
				b.err = err
				b.cancel()
				b.mu.Unlock()
			}
			return
		}
	}
}

func command(ctx context.Context, request Request, writer *frameWriter) error {
	if (len(request.Argv) == 0) == (request.Command == "") {
		return writer.report(problem("invalid_command", "Provide either an argument array or a shell command."))
	}
	if len(request.Argv) > 256 || len(request.Command) > 256<<10 {
		return writer.report(problem("invalid_command", "The command exceeds the helper limit."))
	}
	argv := request.Argv
	if request.Command != "" {
		argv = []string{"/bin/bash", "--noprofile", "--norc", "-c", request.Command}
	}
	for _, argument := range argv {
		if strings.IndexByte(argument, 0) >= 0 || len(argument) > 256<<10 {
			return writer.report(problem("invalid_command", "A command argument is invalid or too large."))
		}
	}
	if request.TimeoutMS == 0 {
		request.TimeoutMS = 180000
	}
	if request.TimeoutMS < 1 || request.TimeoutMS > 900000 {
		return writer.report(problem("invalid_timeout", "Command timeouts must be between 1 and 900000 milliseconds."))
	}
	if request.MaxOutputBytes == 0 {
		request.MaxOutputBytes = 1 << 20
	}
	if request.MaxOutputBytes < 1 || request.MaxOutputBytes > 16<<20 {
		return writer.report(problem("invalid_output_limit", "Command output limits must be between 1 byte and 16 MiB."))
	}
	if request.Cwd == "" {
		request.Cwd = "/workspace"
	}
	root, err := checkedPath(request.Roots, request.Cwd, false)
	if err != nil {
		return writer.report(err)
	}
	defer root.close()
	cwd, err := openRelative(int(root.root.Fd()), root.rel, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return writer.report(err)
	}
	defer cwd.Close()
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return writer.report(problem("supervision_unavailable", "Could not enable descendant supervision."))
	}
	if err := restrictCommandNetwork(); err != nil {
		return writer.report(err)
	}
	_, startTicks, err := procIdentity(os.Getpid())
	if err != nil {
		return writer.report(problem("supervision_unavailable", "Could not establish the supervisor process identity."))
	}
	stdout, stdoutWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdout.Close()
	defer stdoutWrite.Close()
	stderr, stderrWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stderr.Close()
	defer stderrWrite.Close()
	// Resolve PATH explicitly. exec.Command otherwise searches the supervisor's
	// inherited environment before the child environment is assigned.
	executable, err := commandExecutable(argv[0])
	if err != nil {
		return writer.report(problem("command_not_found", "The requested command is not installed in the sandbox."))
	}
	cmd := exec.Command(executable, argv[1:]...)
	cmd.Dir = fmt.Sprintf("/proc/self/fd/%d", cwd.Fd())
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "TMPDIR=/tmp", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "PNPM_CONFIG_VERIFY_DEPS_BEFORE_RUN=false", "PNPM_CONFIG_MANAGE_PACKAGE_MANAGER_VERSIONS=false"}
	cmd.Stdout, cmd.Stderr = stdoutWrite, stderrWrite
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := ctx.Err(); err != nil {
		return writer.write(Frame{Type: "exit", Code: 130, Reason: "cancelled", Clean: true})
	}
	if err = cmd.Start(); err != nil {
		return writer.report(problem("command_start_failed", "The command could not be started in the sandbox."))
	}
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	timed, stopTimeout := context.WithTimeout(ctx, time.Duration(request.TimeoutMS)*time.Millisecond)
	defer stopTimeout()
	work, cancel := context.WithCancel(timed)
	defer cancel()
	budget := &outputBudget{remaining: request.MaxOutputBytes, writer: writer, cancel: cancel}
	// Control frames originate only here. A command never inherits the protocol
	// output descriptor; it receives the two private pipes above instead.
	if err := writer.write(Frame{Type: "start", PID: os.Getpid(), StartTicks: startTicks}); err != nil {
		cancel()
	}
	var pumps sync.WaitGroup
	pumps.Add(2)
	go func() { defer pumps.Done(); budget.copy("stdout", stdout) }()
	go func() { defer pumps.Done(); budget.copy("stderr", stderr) }()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var waitErr error
	finished := false
	reason := ""
	select {
	case waitErr = <-waited:
		finished = true
	case <-work.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			reason = "cancelled"
		} else if errors.Is(timed.Err(), context.DeadlineExceeded) {
			reason = "timeout"
		} else {
			reason = "output_limit"
		}
	}
	// Natural shell exit is not permission to keep background work alive. Reap
	// the complete descendant tree before emitting a trusted completion.
	clean, cleanupErr := cleanupDescendants(cmd.Process.Pid, waited, &finished, &waitErr)
	if !clean {
		_ = stdout.Close()
		_ = stderr.Close()
	}
	pumpDone := make(chan struct{})
	go func() { pumps.Wait(); close(pumpDone) }()
	select {
	case <-pumpDone:
	case <-time.After(time.Second):
		_ = stdout.Close()
		_ = stderr.Close()
		<-pumpDone
		clean = false
		cleanupErr = errors.New("command output remained open after cleanup")
	}
	budget.mu.Lock()
	limited, outputErr := budget.limited, budget.err
	budget.mu.Unlock()
	if outputErr != nil {
		return errors.Join(outputErr, cleanupErr)
	}
	if limited {
		reason = "output_limit"
	}
	code := 0
	if waitErr != nil {
		code = 1
		var exited *exec.ExitError
		if errors.As(waitErr, &exited) {
			code = exited.ExitCode()
			if code < 0 {
				code = 128 + int(exited.Sys().(syscall.WaitStatus).Signal())
			}
		}
	}
	switch reason {
	case "timeout":
		code = 124
	case "cancelled":
		code = 130
	case "output_limit":
		code = 125
	}
	if !clean {
		code, reason = 125, "cleanup_failed"
	}
	if err := writer.write(Frame{Type: "exit", Code: code, Reason: reason, Clean: clean, Truncated: limited}); err != nil {
		return errors.Join(err, cleanupErr)
	}
	return cleanupErr
}

func commandExecutable(name string) (string, error) {
	if strings.Contains(name, "/") {
		// Relative command paths are resolved by exec after it changes directory.
		return name, nil
	}
	for _, directory := range []string{"/usr/local/bin", "/usr/bin", "/bin"} {
		candidate := directory + "/" + name
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func descendants(parent int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	parents := make(map[int]int)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == parent {
			continue
		}
		ppid, _, err := procIdentity(pid)
		if err == nil {
			parents[pid] = ppid
		} else if !errors.Is(err, os.ErrNotExist) {
			// Other UIDs may be hidden by procfs policy. Own descendants cannot be
			// hidden without changing their credentials, which no-new-privs and
			// dropped container capabilities prevent.
			continue
		}
	}
	owned := map[int]bool{parent: true}
	for changed := true; changed; {
		changed = false
		for pid, ppid := range parents {
			if !owned[pid] && owned[ppid] {
				owned[pid], changed = true, true
			}
		}
	}
	out := make([]int, 0, len(owned)-1)
	for pid := range owned {
		if pid != parent {
			out = append(out, pid)
		}
	}
	return out, nil
}

func cleanupDescendants(group int, waited <-chan error, finished *bool, waitErr *error) (bool, error) {
	deadline := time.Now().Add(3 * time.Second)
	grace := time.Now().Add(250 * time.Millisecond)
	_ = unix.Kill(-group, unix.SIGTERM)
	for time.Now().Before(deadline) {
		children, err := descendants(os.Getpid())
		if err != nil {
			return false, errors.New("could not inspect supervisor descendants")
		}
		signal := unix.SIGTERM
		if time.Now().After(grace) {
			signal = unix.SIGKILL
			// Stop the current tree before killing it. Repeated scans catch
			// double forks adopted by the subreaper during this cleanup.
			for _, child := range children {
				_ = unix.Kill(child, unix.SIGSTOP)
			}
		}
		for _, child := range children {
			_ = unix.Kill(child, signal)
		}
		if !*finished {
			select {
			case *waitErr = <-waited:
				*finished = true
			default:
			}
		}
		if *finished {
			// Wait only after exec.Cmd has reaped its own child; otherwise a
			// wait4(-1) here could steal that child's exit status.
			for {
				var status unix.WaitStatus
				pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
				if errors.Is(err, unix.ECHILD) {
					return true, nil
				}
				if err != nil && !errors.Is(err, unix.EINTR) {
					return false, errors.New("could not reap supervisor descendants")
				}
				if pid == 0 {
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false, errors.New("supervisor descendants did not exit within the cleanup deadline")
}
