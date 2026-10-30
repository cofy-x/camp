package layerfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/runnertools"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

const helperPath = "/usr/local/bin/camp-sandbox"

// Sandbox is a single Run's frozen container identity. It never discovers or
// operates on another Run's container, and never passes model configuration.
type Sandbox struct {
	Docker      *Docker
	ContainerID string
	User        string
	Roots       sandboxexec.Roots
	mu          sync.Mutex
	failure     error
}

func (s *Sandbox) Failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failure
}

func (s *Sandbox) fail(cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	stopErr := s.Docker.Stop(ctx, s.ContainerID, 2*time.Second)
	s.failure = errors.Join(cause, stopErr)
	return s.failure
}

type helperError = runnertools.BackendError

type frameReader struct {
	pending []byte
	limit   int
	onFrame func(sandboxexec.Frame) error
}

func (r *frameReader) Write(data []byte) (int, error) {
	original := len(data)
	for len(data) > 0 {
		index := bytes.IndexByte(data, '\n')
		part := data
		if index >= 0 {
			part = data[:index]
		}
		if len(r.pending)+len(part) > r.limit {
			return 0, errors.New("Sandbox helper frame exceeded its limit")
		}
		r.pending = append(r.pending, part...)
		if index < 0 {
			break
		}
		var frame sandboxexec.Frame
		decoder := json.NewDecoder(bytes.NewReader(r.pending))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&frame); err != nil {
			return 0, fmt.Errorf("Invalid sandbox helper frame: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return 0, errors.New("Sandbox helper returned trailing frame content")
		}
		if err := r.onFrame(frame); err != nil {
			return 0, err
		}
		r.pending = r.pending[:0]
		data = data[index+1:]
	}
	return original, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if remaining := b.limit - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(remaining, len(p))])
	}
	return original, nil
}

// Call executes one trusted helper request. The caller serializes file and
// command operations, so guarded file edits cannot race a model command.
func (s *Sandbox) Call(ctx context.Context, request sandboxexec.Request, output func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
	if err := ctx.Err(); err != nil {
		return sandboxexec.Frame{}, err
	}
	if err := s.Failure(); err != nil {
		return sandboxexec.Frame{}, err
	}
	request.Roots = s.Roots
	data, err := json.Marshal(request)
	if err != nil {
		return sandboxexec.Frame{}, err
	}
	command := request.Operation == "command"
	budget := 45 * time.Second
	if command {
		budget = time.Duration(request.TimeoutMS)*time.Millisecond + 15*time.Second
	}
	transportCtx, cancelTransport := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancelTransport()
	started := make(chan sandboxexec.Frame, 1)
	finished := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-finished:
			return
		case <-ctx.Done():
		}
		if !command {
			// A killed file writer has an uncertain outcome. Never reuse this Run.
			_ = s.fail(errors.New("File operation was interrupted"))
			cancelTransport()
			return
		}
		select {
		case frame := <-started:
			cancelCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			var out, stderr limitedBuffer
			out.limit, stderr.limit = 4096, 4096
			code, cancelErr := s.Docker.Exec(cancelCtx, s.ContainerID, s.User, []string{helperPath, "cancel", strconv.Itoa(frame.PID), frame.StartTicks}, &out, &stderr)
			cancel()
			if cancelErr != nil || code != 0 {
				_ = s.fail(errors.New("Sandbox command cancellation could not be delivered"))
				cancelTransport()
				return
			}
		case <-finished:
			return
		case <-time.After(3 * time.Second):
			_ = s.fail(errors.New("Sandbox command did not confirm its process identity"))
			cancelTransport()
			return
		}
		select {
		case <-finished:
		case <-time.After(4 * time.Second):
			_ = s.fail(errors.New("Sandbox command did not confirm descendant cleanup"))
			cancelTransport()
		}
	}()
	var result sandboxexec.Frame
	var operationErr error
	gotStart, gotTerminal := false, false
	reader := &frameReader{limit: sandboxexec.MaxRequestBytes, onFrame: func(frame sandboxexec.Frame) error {
		if gotTerminal {
			return errors.New("Sandbox helper emitted output after completion")
		}
		switch frame.Type {
		case "start":
			if !command || gotStart || frame.PID < 1 || frame.StartTicks == "" {
				return errors.New("Invalid sandbox command identity")
			}
			gotStart = true
			started <- frame
		case "stdout", "stderr":
			if !command || !gotStart || len(frame.Data) > 1<<20 {
				return errors.New("Invalid sandbox command output")
			}
		case "exit":
			if !command || !gotStart || !frame.Clean {
				return errors.New("Sandbox command did not confirm cleanup")
			}
			if code, ok := frame.Code.(float64); !ok || code != float64(int(code)) || code < 0 || code > 255 {
				return errors.New("Invalid sandbox command exit code")
			}
			gotTerminal, result = true, frame
		case "result":
			if command {
				return errors.New("Sandbox command returned a file response")
			}
			gotTerminal, result = true, frame
		case "error":
			if gotStart {
				return errors.New("Sandbox command failed without confirming cleanup")
			}
			gotTerminal = true
			operationErr = &helperError{Code: fmt.Sprint(frame.Code), Message: frame.Message}
		default:
			return errors.New("Unknown sandbox helper frame")
		}
		if output != nil {
			return output(frame)
		}
		return nil
	}}
	stderr := &limitedBuffer{limit: 4096}
	code, execErr := s.Docker.ExecInput(transportCtx, s.ContainerID, s.User, []string{helperPath, "request"}, bytes.NewReader(data), reader, stderr)
	close(finished)
	<-watchDone
	if execErr != nil || code != 0 || len(reader.pending) != 0 || !gotTerminal {
		return sandboxexec.Frame{}, s.fail(errors.Join(execErr, errors.New("Sandbox helper exited without a valid completion")))
	}
	if err := s.Failure(); err != nil {
		return sandboxexec.Frame{}, err
	}
	return result, operationErr
}

// Setup installs the trusted helper and a stable shell path before model tools
// are admitted. The workspace target comes from LayerFS public mount metadata.
func (s *Sandbox) Setup(ctx context.Context, helper []byte) error {
	ids := strings.Split(s.User, ":")
	uid, gid := 0, 0
	if len(ids) == 2 {
		uid, _ = strconv.Atoi(ids[0])
		gid, _ = strconv.Atoi(ids[1])
	}
	if uid <= 0 || gid <= 0 || !strings.HasPrefix(s.Roots.Workspace, "/workspaces/") || path.Clean(s.Roots.Workspace) != s.Roots.Workspace || (s.Roots.References != "" && s.Roots.References != "/references") {
		return errors.New("Sandbox setup requires a non-root user and LayerFS workspace path")
	}
	if err := s.Docker.Limit(ctx, s.ContainerID); err != nil {
		return err
	}
	if err := s.Docker.InstallFile(ctx, s.ContainerID, helperPath, helper, 0555); err != nil {
		return err
	}
	for _, argv := range [][]string{{"/bin/ln", "-s", "--", s.Roots.Workspace, "/workspace"}, {"/bin/mkdir", "-p", "--", "/references"}, {"/bin/chmod", "0555", "/references"}} {
		var stderr limitedBuffer
		stderr.limit = 4096
		code, err := s.Docker.Exec(ctx, s.ContainerID, "0:0", argv, io.Discard, &stderr)
		if err != nil || code != 0 {
			return errors.Join(err, fmt.Errorf("Sandbox initialization failed (exit %d)", code))
		}
	}
	return nil
}
