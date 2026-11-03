//go:build linux

package documenttext

import (
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

func initializeReader(context.Context, Config) error { return nil }

func runReader(ctx context.Context, config Config, format string, input io.Reader, stdout, stderr io.Writer) error {
	cmd, err := readerCommand(config, format)
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, stdout, stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return failure("unavailable", "The isolated reader could not start. Check Bubblewrap and the document runtime.", true)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		// Killing the Bubblewrap supervisor removes its private PID namespace.
		_ = cmd.Process.Kill()
		err = <-done
	}
	return err
}

func readerCommand(config Config, format string) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, failure("unavailable", "Document preparation requires Linux Bubblewrap (bwrap). The original remains available to download.", true)
	}
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-net", "--die-with-parent", "--new-session", "--cap-drop", "ALL"}
	for _, path := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if _, err := os.Stat(path); err == nil {
			args = append(args, "--ro-bind", path, path)
		}
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--dir", "/home", "--dir", "/home/camp", "--chdir", "/tmp")
	if format == "pdf" {
		node, err := executable(config.NodePath)
		if err != nil {
			return nil, err
		}
		root, err := readerRuntimeDirectory(config)
		if err != nil {
			return nil, err
		}
		args = append(args, "--ro-bind", node, "/node", "--ro-bind", root, "/runtime", "--", "/node", "--max-old-space-size=128", "--max-semi-space-size=8", "/runtime/extract.mjs")
	} else {
		program, err := executable(config.ExecutablePath)
		if err != nil {
			return nil, err
		}
		args = append(args, "--ro-bind", program, "/camp", "--", "/camp", "document-worker")
	}
	cmd := exec.Command(bwrap, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/home/camp", "TMPDIR=/tmp", "LANG=C.UTF-8", "GOMEMLIMIT=96MiB", "GOMAXPROCS=1"}
	return cmd, nil
}
