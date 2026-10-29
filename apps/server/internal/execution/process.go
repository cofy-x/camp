// Package execution manages host processes, isolation, and process cleanup.
package execution

import (
	"context"
	"os/exec"
	"time"
)

// Only Wait reaps processes; stopping Bubblewrap also removes tool processes in its PID namespace.
func Probe(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return ctx.Err()
	}
}

func Stop(cmd *exec.Cmd, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(6 * time.Second):
		_ = cmd.Process.Kill()
		return <-done
	}
}
