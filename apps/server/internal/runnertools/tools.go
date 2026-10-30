package runnertools

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

type toolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *toolError) Error() string { return e.Message }

type toolCommand struct {
	cancel  context.CancelFunc
	done    chan struct{}
	clean   bool
	started bool
}

// Tools exposes one Run's narrow capabilities to its private DSH process. The
// token is not exposed to model tools, container processes, or dashboard APIs.
type Tools struct {
	sandbox  Backend
	readOnly bool
	token    string
	lease    chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	call     func(context.Context, sandboxexec.Request, func(sandboxexec.Frame) error) (sandboxexec.Frame, error)
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	commands map[string]*toolCommand
	closed   bool
	sealed   bool
	failed   chan struct{}
	failOnce sync.Once
}

func NewTools(ctx context.Context, sandbox Backend, readOnly bool) (*Tools, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	t := &Tools{sandbox: sandbox, readOnly: readOnly, token: hex.EncodeToString(secret[:]), lease: make(chan struct{}, 1), ctx: ctx, cancel: cancel, call: sandbox.Call, listener: listener, commands: map[string]*toolCommand{}, failed: make(chan struct{})}
	t.server = &http.Server{Handler: http.HandlerFunc(t.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8 << 10}
	go func() { _ = t.server.Serve(listener) }()
	return t, nil
}

// Seal rejects new model operations after a durable coordination decision.
// Cancellation remains available while existing operations settle.
func (t *Tools) Seal(ctx context.Context) error {
	t.mu.Lock()
	t.sealed = true
	t.mu.Unlock()
	select {
	case t.lease <- struct{}{}:
		t.release()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.ctx.Done():
		return t.ctx.Err()
	}
}

func (t *Tools) Endpoint() string { return "http://" + t.listener.Addr().String() }
func (t *Tools) Token() string    { return t.token }

// Failed closes on an infrastructure failure; the Run owner must cancel DSH.
func (t *Tools) Failed() <-chan struct{} { return t.failed }

func (t *Tools) abort() {
	t.failOnce.Do(func() { close(t.failed) })
	t.cancel()
}

func (t *Tools) acquire(ctx context.Context) error {
	select {
	case t.lease <- struct{}{}:
		t.mu.Lock()
		sealed := t.sealed
		t.mu.Unlock()
		if sealed {
			t.release()
			return errors.New("Model tools ended after a coordination decision")
		}
		if err := ctx.Err(); err != nil {
			t.release()
			return err
		}
		if err := t.ctx.Err(); err != nil {
			t.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.ctx.Done():
		return t.ctx.Err()
	}
}

func (t *Tools) release() { <-t.lease }

func (t *Tools) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.cancel()
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	err := t.server.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, t.server.Close())
	}
	return err
}

func (t *Tools) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost || r.Header.Get("Origin") != "" || r.URL.RawQuery != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+t.token)) != 1 {
		writeToolError(w, http.StatusForbidden, &toolError{"FS_PERMISSION_DENIED", "This execution capability is not available."})
		return
	}
	t.mu.Lock()
	sealed := t.sealed
	t.mu.Unlock()
	if t.ctx.Err() != nil || sealed && r.URL.Path != "/shell/cancel" {
		writeToolError(w, http.StatusGone, &toolError{"FS_ABORTED", "This execution has ended."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, sandboxexec.MaxRequestBytes)
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	defer cancel()
	switch r.URL.Path {
	case "/fs":
		var input json.RawMessage
		if err := decodeTool(r, &input); err != nil {
			writeToolError(w, http.StatusBadRequest, err)
			return
		}
		if err := t.acquire(ctx); err != nil {
			writeToolError(w, http.StatusGone, err)
			return
		}
		defer t.release()
		result, err := t.fileOperation(ctx, input)
		if err != nil {
			if t.sandbox.Failure() != nil {
				t.abort()
			}
			writeToolError(w, http.StatusBadRequest, err)
			return
		}
		writeToolValue(w, result)
	case "/shell":
		t.shell(ctx, w, r)
	case "/shell/cancel":
		t.cancelShell(ctx, w, r)
	default:
		writeToolError(w, http.StatusNotFound, &toolError{"FS_NOT_FOUND", "This tool operation is not supported."})
	}
}

func decodeTool(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return &toolError{"FS_IO_ERROR", "Provide a valid tool request within the size limit."}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return &toolError{"FS_IO_ERROR", "Only one tool request is allowed."}
	}
	return nil
}

func writeToolValue(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
}

func writeToolError(w http.ResponseWriter, status int, err error) {
	var known *toolError
	if !errors.As(err, &known) {
		known = &toolError{"FS_IO_ERROR", "The execution operation failed. Inspect the retained activity for details."}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			known = &toolError{"FS_ABORTED", "The execution operation was cancelled."}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": known})
}

func validCommandID(id string) bool {
	return len(id) >= 16 && len(id) <= 80 && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-')
	}) < 0
}
