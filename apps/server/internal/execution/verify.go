package execution

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
)

func RunTests(ctx context.Context, cmd *exec.Cmd, output func(string) error) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return -1, err
	}
	done := make(chan error, 1)
	go func() { err := cmd.Wait(); _ = writer.Close(); done <- err }()
	readDone := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(reader)
		s.Buffer(make([]byte, 4096), 256<<10)
		for s.Scan() {
			if err := output(s.Text() + "\n"); err != nil {
				_ = reader.Close()
				readDone <- err
				return
			}
		}
		readDone <- s.Err()
		_ = reader.Close()
	}()
	var waitErr error
	var readErr error
	select {
	case waitErr = <-done:
		readErr = <-readDone
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		waitErr = errors.Join(ctx.Err(), <-done)
		readErr = <-readDone
	case readErr = <-readDone:
		if readErr != nil {
			// A failed durable output callback must stop execution immediately;
			// otherwise a quiet child could outlive its failed event stream.
			_ = cmd.Process.Kill()
		}
		waitErr = <-done
	}
	return cmd.ProcessState.ExitCode(), errors.Join(waitErr, readErr)
}
