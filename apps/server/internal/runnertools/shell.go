package runnertools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

type shellRequest struct {
	CommandID      string `json:"commandId"`
	Command        string `json:"command"`
	Workdir        string `json:"workdir"`
	TimeoutMS      int64  `json:"timeoutMs"`
	StdoutMaxBytes int64  `json:"stdoutMaxBytes"`
}

type outputText struct {
	all     tailBuffer
	pending []byte
}

type tailBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	n := len(data)
	if len(b.data)+n > b.limit {
		b.truncated = true
	}
	if n >= b.limit {
		b.data = append(b.data[:0], data[n-b.limit:]...)
	} else {
		remove := max(0, len(b.data)+n-b.limit)
		b.data = append(b.data[:0], b.data[remove:]...)
		b.data = append(b.data, data...)
	}
	return n, nil
}

func (b *tailBuffer) String() string { return string(b.data) }

func (b *tailBuffer) text() (string, bool) {
	text := strings.ToValidUTF8(b.String(), "\uFFFD")
	truncated := b.truncated
	if len(text) > b.limit {
		start := len(text) - b.limit
		for start < len(text) && !utf8.RuneStart(text[start]) {
			start++
		}
		text, truncated = text[start:], true
	}
	return text, truncated
}

func (o *outputText) append(data []byte, final bool) string {
	_, _ = o.all.Write(data)
	o.pending = append(o.pending, data...)
	end := 0
	for end < len(o.pending) {
		if !utf8.FullRune(o.pending[end:]) && !final {
			break
		}
		_, size := utf8.DecodeRune(o.pending[end:])
		end += size
	}
	text := string(bytes.ToValidUTF8(o.pending[:end], []byte("\uFFFD")))
	o.pending = append(o.pending[:0], o.pending[end:]...)
	return text
}

func (t *Tools) shell(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if t.readOnly {
		writeToolError(w, http.StatusForbidden, &toolError{"FS_PERMISSION_DENIED", "This agent has read-only file access and cannot execute commands."})
		return
	}
	var input shellRequest
	if err := decodeTool(r, &input); err != nil {
		writeToolError(w, http.StatusBadRequest, err)
		return
	}
	if !validCommandID(input.CommandID) || strings.TrimSpace(input.Command) == "" || len(input.Command) > 128<<10 || strings.ContainsRune(input.Command, '\x00') || input.TimeoutMS < 1 || input.TimeoutMS > 180000 || input.StdoutMaxBytes < 1 || input.StdoutMaxBytes > 1<<20 {
		writeToolError(w, http.StatusBadRequest, &toolError{"FS_IO_ERROR", "Command identity, timeout, output limit, or script is invalid."})
		return
	}
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	t.mu.Lock()
	previous, exists := t.commands[input.CommandID]
	if exists && previous.started {
		t.mu.Unlock()
		writeToolError(w, http.StatusConflict, &toolError{"FS_IO_ERROR", "This command identity has already been used; an uncertain command is never replayed."})
		return
	}
	if len(t.commands) >= 1024 {
		t.mu.Unlock()
		writeToolError(w, http.StatusTooManyRequests, &toolError{"FS_IO_ERROR", "This execution has reached its command limit."})
		return
	}
	command := &toolCommand{cancel: cancel, done: make(chan struct{}), started: true}
	t.commands[input.CommandID] = command
	t.mu.Unlock()
	if exists {
		cancel() // A cancellation arriving before its command is a tombstone.
	}
	defer func() { close(command.done) }()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flush := http.NewResponseController(w)
	emit := func(value any) {
		_ = flush.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if err := json.NewEncoder(w).Encode(value); err != nil {
			cancel()
		}
		_ = flush.Flush()
	}
	stdout := outputText{all: tailBuffer{limit: int(input.StdoutMaxBytes)}}
	stderr := outputText{all: tailBuffer{limit: int(input.StdoutMaxBytes)}}
	var frame sandboxexec.Frame
	err := t.acquire(execCtx)
	if err == nil {
		cwd, cwdErr := t.virtualFilePath(".", input.Workdir)
		if cwdErr != nil {
			t.release()
			emit(map[string]any{"type": "error", "cleanupConfirmed": true, "error": map[string]string{"code": "FS_SANDBOX_DENIED", "message": cwdErr.Error()}})
			t.mu.Lock()
			command.clean = true
			t.mu.Unlock()
			return
		}
		frame, err = t.call(execCtx, sandboxexec.Request{Operation: "command", Command: input.Command, Cwd: cwd, TimeoutMS: input.TimeoutMS, MaxOutputBytes: 8 << 20}, func(event sandboxexec.Frame) error {
			switch event.Type {
			case "stdout", "stderr":
				stream := &stdout
				if event.Type == "stderr" {
					stream = &stderr
				}
				if text := stream.append(event.Data, false); text != "" {
					emit(map[string]any{"type": event.Type, "text": text})
				}
			}
			return nil // Drain the helper even if its HTTP client has disconnected.
		})
		t.release()
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		frame = sandboxexec.Frame{Type: "exit", Code: float64(130), Clean: true, Reason: "cancelled"}
		err = nil
	}
	if err != nil || !frame.Clean {
		var preparation *BackendError
		clean := errors.As(err, &preparation)
		t.mu.Lock()
		command.clean = clean
		t.mu.Unlock()
		message := "The command could not confirm a clean completion. The execution cannot publish files."
		if clean {
			message = preparation.Message
		} else {
			t.abort()
		}
		emit(map[string]any{"type": "error", "cleanupConfirmed": clean, "error": map[string]string{"code": "FS_IO_ERROR", "message": message}})
		return
	}
	for _, stream := range []struct {
		name string
		text *outputText
	}{{"stdout", &stdout}, {"stderr", &stderr}} {
		if text := stream.text.append(nil, true); text != "" {
			emit(map[string]any{"type": stream.name, "text": text})
		}
	}
	t.mu.Lock()
	command.clean = true
	t.mu.Unlock()
	stdoutText, stdoutTruncated := stdout.all.text()
	stderrText, stderrTruncated := stderr.all.text()
	emit(map[string]any{"type": "result", "cleanupConfirmed": true, "result": map[string]any{
		"exitCode": frame.Code, "signal": nil, "timedOut": frame.Reason == "timeout", "aborted": frame.Reason == "cancelled", "timeoutMs": input.TimeoutMS,
		"stdout": map[string]any{"text": stdoutText, "truncated": stdoutTruncated || frame.Truncated},
		"stderr": map[string]any{"text": stderrText, "truncated": stderrTruncated || frame.Truncated},
	}})
}

func (t *Tools) cancelShell(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var input struct {
		CommandID string `json:"commandId"`
	}
	if err := decodeTool(r, &input); err != nil || !validCommandID(input.CommandID) {
		writeToolError(w, http.StatusBadRequest, &toolError{"FS_IO_ERROR", "Provide the command identity to cancel."})
		return
	}
	t.mu.Lock()
	command, exists := t.commands[input.CommandID]
	if !exists {
		if len(t.commands) >= 1024 {
			t.mu.Unlock()
			writeToolError(w, http.StatusTooManyRequests, &toolError{"FS_IO_ERROR", "This execution has reached its command limit."})
			return
		}
		command = &toolCommand{done: make(chan struct{}), clean: true}
		close(command.done)
		t.commands[input.CommandID] = command
	}
	cancel := command.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-command.done:
		t.mu.Lock()
		clean := command.clean
		t.mu.Unlock()
		if !clean {
			writeToolError(w, http.StatusServiceUnavailable, &toolError{"FS_IO_ERROR", "Command cleanup could not be confirmed."})
			return
		}
		writeToolValue(w, map[string]bool{"cleanupConfirmed": true})
	case <-ctx.Done():
		writeToolError(w, http.StatusRequestTimeout, ctx.Err())
	}
}
