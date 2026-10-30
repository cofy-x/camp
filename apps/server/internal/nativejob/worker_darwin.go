//go:build darwin

package nativejob

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/processowner"
	"github.com/cofy-x/camp/apps/server/internal/seatbelt"
)

type outputBudget struct {
	mu        sync.Mutex
	remaining int64
	truncated bool
	wire      *wire
	cancel    context.CancelFunc
}
type streamWriter struct {
	budget *outputBudget
	kind   string
}

func (w streamWriter) Write(data []byte) (int, error) {
	n := len(data)
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	if int64(len(data)) > w.budget.remaining {
		data = data[:w.budget.remaining]
		w.budget.truncated = true
		defer w.budget.cancel()
	}
	w.budget.remaining -= int64(len(data))
	for len(data) > 0 {
		size := min(len(data), 16<<10)
		if err := w.budget.wire.send(frame{Type: w.kind, Data: data[:size]}); err != nil {
			w.budget.cancel()
			return 0, err
		}
		data = data[size:]
	}
	return n, nil
}

// Worker is the trusted root of a distinct launchd resource coalition. The
// sandboxed command cannot signal it, read its receipt or use its transport.
func Worker(ctx context.Context, name string) error {
	spec, err := readSpec(name)
	if err != nil {
		return err
	}
	address, err := net.ResolveTCPAddr("tcp", spec.Endpoint)
	if err != nil || !address.IP.Equal(net.IPv4(127, 0, 0, 1)) || address.Port <= 0 || len(spec.Token) != 64 {
		return errors.New("Invalid private native job channel")
	}
	self, err := processowner.Observe(os.Getpid())
	if err != nil || self.Coalition == spec.Client.Coalition || self.Boot != spec.Client.Boot {
		return errors.New("Native job has no independent process ownership")
	}
	connection, err := net.DialTimeout("tcp", address.String(), 5*time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	w := &wire{connection: connection}
	if err := w.send(frame{Type: "hello", Token: spec.Token, Owner: self}); err != nil {
		return err
	}
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	admit, err := receive(connection)
	if err != nil || admit.Type != "admit" {
		return errors.New("Native job was not admitted")
	}
	_ = connection.SetReadDeadline(time.Time{})
	profile, err := seatbelt.Profile(spec.Policy)
	if err != nil {
		return err
	}
	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutMS)*time.Millisecond)
	defer cancel()
	cmd := exec.Command("/usr/bin/sandbox-exec", append([]string{"-p", profile}, spec.Argv...)...)
	cmd.Dir, cmd.Env = spec.Directory, spec.Env
	cmd.WaitDelay = 250 * time.Millisecond
	budget := &outputBudget{remaining: spec.MaxOutput, wire: w, cancel: cancel}
	cmd.Stdout, cmd.Stderr = streamWriter{budget, "stdout"}, streamWriter{budget, "stderr"}
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	result := Result{Code: 1}
	err = cmd.Start()
	if err == nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		go func() {
			for {
				f, err := receive(connection)
				if err != nil {
					cancel()
					return
				}
				switch f.Type {
				case "stdin":
					if _, err := input.Write(f.Data); err != nil {
						_ = input.Close()
					}
				case "stdin-eof":
					_ = input.Close()
				case "cancel":
					cancel()
					return
				default:
					cancel()
					return
				}
			}
		}()
		watchDone := make(chan struct{})
		defer close(watchDone)
		go func() {
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-watchDone:
					return
				case <-commandCtx.Done():
					return
				case <-ticker.C:
					observed, err := processowner.Observe(spec.Client.PID)
					if err != nil || observed != spec.Client {
						cancel()
						return
					}
				}
			}
		}()
		waited := false
		select {
		case err = <-done:
			waited = true
			result.Code = cmd.ProcessState.ExitCode()
			if err != nil && result.Code < 0 {
				result.Reason = "Native command did not exit normally"
			}
		case <-commandCtx.Done():
			result.Reason = "Native command cancelled or timed out"
		}
		// No command remains admitted after this point. Closing stdin unblocks a
		// client that stopped reading before all input was consumed.
		_ = input.Close()
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		cleanupErr := (processowner.Owner{Root: self}).Terminate(cleanupCtx)
		stop()
		result.Clean = cleanupErr == nil
		if cleanupErr != nil {
			result.Reason = "Native descendant cleanup is unconfirmed"
		}
		if !waited {
			select {
			case <-done:
			case <-time.After(time.Second):
				result.Clean = false
			}
		}
		budget.mu.Lock()
		result.Truncated = budget.truncated
		budget.mu.Unlock()
		if result.Truncated {
			result.Code = 1
			result.Reason = "Native command exceeded its output bound"
		}
	} else {
		_ = input.Close()
		result.Reason = "Native command could not be started"
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		result.Clean = (processowner.Owner{Root: self}).Terminate(cleanupCtx) == nil
		stop()
	}
	if err := save(filepath.Join(filepath.Dir(name), "worker-result.json"), result); err != nil {
		return err
	}
	if err := w.send(frame{Type: "exit", Result: &result}); err != nil {
		return err
	}
	if !result.Clean {
		return errors.New(result.Reason)
	}
	return nil
}

var _ io.Writer = streamWriter{}
