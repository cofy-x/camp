package nativeexecutor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

// The private coordinator acknowledges only a journaled decision. It exposes no
// Camp API or Runner credentials and accepts exactly one decision per execution.
type coordination struct {
	ctx      context.Context
	server   *http.Server
	listener net.Listener
	token    string
	task     p.Collaboration
	persist  func(context.Context, p.CoordinationIntent) error
	seal     func(context.Context) error
	mu       sync.Mutex
	intent   *p.CoordinationIntent
}

func newCoordination(ctx context.Context, task p.Collaboration, persist func(context.Context, p.CoordinationIntent) error, seal func(context.Context) error) (*coordination, error) {
	if task.ID == "" || task.AgentID == "" || len(task.Participants) == 0 || persist == nil {
		return nil, errors.New("Collaboration requires a frozen scope and durable intent journal")
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c := &coordination{ctx: ctx, listener: listener, token: hex.EncodeToString(key[:]), task: task, persist: persist, seal: seal}
	c.server = &http.Server{Handler: http.HandlerFunc(c.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8 << 10}
	go func() { _ = c.server.Serve(listener) }()
	return c, nil
}
func (c *coordination) endpoint() string { return "http://" + c.listener.Addr().String() }
func (c *coordination) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := c.server.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, c.server.Close())
	}
	return err
}
func (c *coordination) result() *p.CoordinationIntent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.intent == nil {
		return nil
	}
	out := *c.intent
	return &out
}
func (c *coordination) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	reject := func(code int, message string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "FS_PERMISSION_DENIED", "message": message}})
	}
	if r.Method != http.MethodPost || r.URL.Path != "/intent" || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.token)) != 1 {
		reject(http.StatusForbidden, "This collaboration capability is not available.")
		return
	}
	if c.ctx.Err() != nil {
		reject(http.StatusGone, "This execution has ended.")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<10))
	decoder.DisallowUnknownFields()
	var intent p.CoordinationIntent
	if err := decoder.Decode(&intent); err != nil {
		reject(http.StatusBadRequest, "Provide a valid coordination decision.")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		reject(http.StatusBadRequest, "Only one decision is allowed.")
		return
	}
	if err := validateCoordination(c.task, intent); err != nil {
		reject(http.StatusBadRequest, err.Error())
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.intent != nil {
		before, _ := json.Marshal(c.intent)
		after, _ := json.Marshal(intent)
		if !bytes.Equal(before, after) {
			reject(http.StatusConflict, "This execution already recorded a different coordination decision.")
			return
		}
	} else {
		// Persist is bounded independently of the client disconnect: a lost response
		// cannot turn an acknowledged decision into a second scheduling action.
		durable, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		var err error
		if c.seal != nil {
			err = c.seal(durable)
		}
		if err == nil {
			err = c.persist(durable, intent)
		}
		cancel()
		if err != nil {
			reject(http.StatusServiceUnavailable, "The coordination decision could not be saved. Execution cannot continue.")
			return
		}
		c.intent = &intent
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"value": map[string]bool{"accepted": true}})
}
func validateCoordination(task p.Collaboration, i p.CoordinationIntent) error {
	text := func(v string, n int) bool {
		return strings.TrimSpace(v) != "" && len(v) <= n && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
	}
	if !text(i.CallID, 128) || len(i.ArtifactPaths) > 128 {
		return errors.New("A coordination decision requires a bounded call identity and artifact list")
	}
	for _, v := range i.ArtifactPaths {
		if !text(v, 4096) || v == "." || path.Clean(v) != v || path.IsAbs(v) || strings.ContainsAny(v, "\\\x00") || v == ".." || strings.HasPrefix(v, "../") {
			return errors.New("Artifacts must be relative workspace file paths")
		}
	}
	switch i.Kind {
	case "handoff":
		found := false
		for _, person := range task.Participants {
			found = found || person.ID == i.TargetAgentID
		}
		if !found || !text(i.Instruction, 8192) {
			return errors.New("Choose an approved participant and provide a handoff instruction")
		}
	case "ask_person":
		if !text(i.Question, 8192) || len(i.Options) > 8 {
			return errors.New("Provide a question and at most eight options")
		}
		for _, option := range i.Options {
			if !text(option, 512) {
				return errors.New("Question options must be short nonempty text")
			}
		}
	case "finish":
		if !text(i.Summary, 32<<10) {
			return errors.New("Provide a result summary")
		}
	default:
		return errors.New("Unknown coordination decision")
	}
	return nil
}
