// Package sandboxexec implements Camp's trusted, short-lived container helper.
// It does not create a sandbox: its caller owns container isolation and must
// destroy the container if the helper cannot confirm descendant cleanup.
package sandboxexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	MaxFileBytes    = 64 << 20
	MaxRequestBytes = 96 << 20
	MaxEntries      = 10000
	MaxMatches      = 1000
)

// Roots is supplied only by the trusted host, never by model tool arguments.
// Paths exposed to tools are rooted at /workspace and /references regardless
// of the LayerFS mount location inside the container.
type Roots struct {
	Workspace  string `json:"workspace"`
	References string `json:"references,omitempty"`
}

type Request struct {
	Operation       string   `json:"operation"`
	Roots           Roots    `json:"roots"`
	Path            string   `json:"path,omitempty"`
	Destination     string   `json:"destination,omitempty"`
	Data            []byte   `json:"data,omitempty"`
	Offset          int64    `json:"offset,omitempty"`
	Length          *int64   `json:"length,omitempty"`
	Recursive       bool     `json:"recursive,omitempty"`
	ExpectedVersion string   `json:"expectedVersion,omitempty"`
	Mode            uint32   `json:"mode,omitempty"`
	Pattern         string   `json:"pattern,omitempty"`
	Argv            []string `json:"argv,omitempty"`
	Command         string   `json:"command,omitempty"`
	Cwd             string   `json:"cwd,omitempty"`
	TimeoutMS       int64    `json:"timeoutMs,omitempty"`
	MaxOutputBytes  int64    `json:"maxOutputBytes,omitempty"`
}

type FileInfo struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Size       int64  `json:"size"`
	Mode       uint32 `json:"mode"`
	ModifiedAt string `json:"modifiedAt"`
	Version    string `json:"version"`
}

type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Frame is one NDJSON record. Data is JSON base64, including command output;
// arbitrary child bytes can never become control records.
type Frame struct {
	Type       string     `json:"type"`
	PID        int        `json:"pid,omitempty"`
	StartTicks string     `json:"startTicks,omitempty"`
	Data       []byte     `json:"data,omitempty"`
	Info       *FileInfo  `json:"info,omitempty"`
	Entries    []FileInfo `json:"entries,omitempty"`
	Matches    []Match    `json:"matches,omitempty"`
	Code       any        `json:"code,omitempty"`
	Message    string     `json:"message,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Clean      bool       `json:"clean,omitempty"`
	Truncated  bool       `json:"truncated,omitempty"`
}

type failure struct{ code, message string }

func (e *failure) Error() string { return e.message }

func problem(code, message string) error { return &failure{code, message} }

// FileCall accepts trusted roots from a workspace provider. All paths remain
// logical capability paths; errors are framed exactly like the short-lived
// helper. Process execution uses a separate platform owner.
func FileCall(ctx context.Context, request Request) (Frame, error) {
	frame, err := files(ctx, request)
	if err == nil {
		return frame, nil
	}
	var known *failure
	if errors.As(err, &known) {
		return Frame{Type: "error", Code: known.code, Message: known.message}, nil
	}
	return Frame{}, err
}

type frameWriter struct {
	mu  sync.Mutex
	out io.Writer
	err error
}

func (w *frameWriter) write(frame Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.err = json.NewEncoder(w.out).Encode(frame)
	return w.err
}

func (w *frameWriter) report(err error) error {
	var known *failure
	frame := Frame{Type: "error", Code: "operation_failed", Message: "The sandbox operation failed."}
	if errors.As(err, &known) {
		frame.Code, frame.Message = known.code, known.message
	}
	return w.write(frame)
}

// Run reads one request and emits bounded NDJSON. A file failure is an error
// frame; a command always needs a final exit frame with clean:true. A non-nil
// return means transport/supervision failed and the container cannot be reused.
func Run(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	w := &frameWriter{out: output}
	if len(args) == 3 && args[0] == "cancel" {
		if err := cancelProcess(args[1], args[2]); err != nil {
			return w.report(err)
		}
		return w.write(Frame{Type: "result"})
	}
	if len(args) < 1 || len(args) > 2 || args[0] != "request" {
		return w.report(problem("invalid_request", "Use request with one JSON input, or cancel with a PID and start time."))
	}
	if len(args) == 2 {
		if len(args[1]) > MaxRequestBytes*4/3+4 {
			return w.report(problem("request_too_large", "The request exceeds the helper limit."))
		}
		input = base64.NewDecoder(base64.StdEncoding, strings.NewReader(args[1]))
	}
	limited := &io.LimitedReader{R: input, N: MaxRequestBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil || limited.N <= 0 {
		return w.report(problem("invalid_request", "Provide one valid request within the helper size limit."))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return w.report(problem("invalid_request", "Only one request is allowed per helper process."))
	}
	if err := validateIdentity(); err != nil {
		return w.report(err)
	}
	if request.Operation == "command" {
		return command(ctx, request, w)
	}
	frame, err := files(ctx, request)
	if err != nil {
		return w.report(err)
	}
	if frame.Type != "result" {
		return fmt.Errorf("invalid internal file response")
	}
	return w.write(frame)
}
