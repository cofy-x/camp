package remoteruns

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func proxyFixture(t *testing.T, upstream string) (*Manager, *storage.Store, context.Context, storage.Attempt, p.Command) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.OpenContext(ctx, filepath.Join(dir, "camp.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	setup := strings.Repeat("a", 64)
	if err = store.EnsureSetupToken(ctx, setup); err != nil {
		t.Fatal(err)
	}
	user, err := store.SetupAdmin(ctx, setup, "owner", "Owner", strings.Repeat("h", 64), "test-session", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	actor := storage.WithUser(ctx, user)
	_, token, err := store.CreateRunnerEnrollment(actor, "Proxy runner", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := store.JoinRunner(ctx, storage.JoinRunnerInput{Token: token, RegistrationID: "proxy-test", Credential: strings.Repeat("c", 64), StorageID: "proxy-storage"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ConnectRunner(ctx, runner.ID, storage.RunnerConnection{StorageID: runner.StorageID, Runtime: "native", Slots: 1, Capabilities: json.RawMessage(`{"executor":"native","ready":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	files := resources.New(filepath.Join(dir, "resources"))
	initial, err := files.Create(ctx, strings.Repeat("d", 32))
	if err != nil {
		t.Fatal(err)
	}
	room, err := store.SeedRoom(actor, storage.Resource{ID: strings.Repeat("d", 32), Name: "Files", CurrentCommit: initial.Commit})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetRunnerAgentGrant(actor, runner.ID, room.DefaultAgentID, true); err != nil {
		t.Fatal(err)
	}
	room, err = store.SetRoomRunner(actor, room.ID, runner.ID, room.RunnerRevision)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(ctx, store, Config{DataDir: dir, Models: runs.Config{BaseURL: upstream, APIKey: "private-upstream-key", Model: "test-model"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Shutdown(context.Background()) })
	// This focused proxy fixture claims manually; the integration suite exercises
	// the live scheduler and daemon lifecycle instead.
	manager.cancel()
	<-manager.done
	manager.ctx, manager.cancel = context.WithCancel(ctx)
	send, err := manager.Send(actor, room.ID, storage.SendMessageInput{RequestID: "model-test", Content: "Write a note.", RecipientAgentIDs: []string{room.DefaultAgentID}, RunnerRevision: &room.RunnerRevision})
	if err != nil {
		t.Fatal(err)
	}
	run, attempt, err := store.ClaimRemoteRun(ctx, send.Runs[0].ID, runner.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.prepare(ctx, run, attempt); err != nil {
		t.Fatal(err)
	}
	attempt, err = store.AcceptAttempt(ctx, attempt.ID, runner.ID)
	if err != nil {
		t.Fatal(err)
	}
	var command p.Command
	if err = json.Unmarshal(attempt.Command, &command); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(attempt.Command), "private-upstream-key") {
		t.Fatal("private upstream key escaped into a runner command")
	}
	return manager, store, actor, attempt, command
}
func proxyRequest(t *testing.T, server *httptest.Server, a storage.Attempt, token, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest("POST", server.URL+"/api/daemon/model/"+a.ID+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Api-Key", token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func TestModelProxyFrozenAuthorityBudgetAndFinalization(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "private-upstream-key" {
			t.Error("upstream authority differs from frozen server configuration")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()
	manager, store, actor, a, command := proxyFixture(t, upstream.URL)
	server := httptest.NewTLSServer(manager.Handler())
	defer server.Close()
	nextKey := "new-private-key"
	nextURL := "http://127.0.0.1:1"
	if _, err := manager.UpdateModelSettings(actor, configuration.Update{APIKey: &nextKey, BaseURL: &nextURL}); err != nil {
		t.Fatal(err)
	}
	good := `{"model":"test-model","max_tokens":20,"stream":true,"messages":[{"role":"user","content":"Hello"}]}`
	for _, input := range []struct {
		token, body string
		status      int
	}{{"wrong", good, 403}, {command.Spec.ModelToken, `{"model":"other","max_tokens":20}`, 400}, {command.Spec.ModelToken, `{"model":"test-model","max_tokens":8192}`, 400}, {command.Spec.ModelToken, `{"model":"test-model","max_tokens":20,"base_url":"http://example.com"}`, 400}, {command.Spec.ModelToken, good, 200}} {
		response := proxyRequest(t, server, a, input.token, input.body)
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != input.status {
			latest, _ := store.GetAttempt(context.Background(), a.ID)
			state, _ := store.GetRun(context.Background(), a.RunID)
			t.Fatalf("status=%d expected=%d body=%s phase=%s run=%s reason=%s", response.StatusCode, input.status, data, latest.Phase, state.Status, state.Error)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid requests reached the model", calls.Load())
	}
	if _, err := store.BeginFinalizing(context.Background(), a.ID, a.RunnerID); err != nil {
		t.Fatal(err)
	}
	response := proxyRequest(t, server, a, command.Spec.ModelToken, good)
	response.Body.Close()
	if response.StatusCode != 403 || calls.Load() != 1 {
		t.Fatal("model remained available during upload")
	}
}
func TestModelProxyNeverRetriesProviderFailure(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		io.WriteString(w, "private-upstream-key sensitive provider failure")
	}))
	defer upstream.Close()
	manager, _, _, a, command := proxyFixture(t, upstream.URL)
	server := httptest.NewTLSServer(manager.Handler())
	defer server.Close()
	response := proxyRequest(t, server, a, command.Spec.ModelToken, `{"model":"test-model","max_tokens":20}`)
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || calls.Load() != 1 || strings.Contains(string(data), "private-upstream-key") {
		t.Fatalf("provider error handling status=%d calls=%d", response.StatusCode, calls.Load())
	}
}

func TestModelProxyCompletesAtMessageStopWithoutWaitingForSocketEOF(t *testing.T) {
	const terminal = "event: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n"
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, terminal)
		w.(http.Flusher).Flush()
		// A valid Messages response is complete at message_stop. Providers can
		// leave the transport open while the SDK starts tools or its next turn.
		<-r.Context().Done()
	}))
	defer upstream.Close()
	manager, store, _, a, command := proxyFixture(t, upstream.URL)
	finished := make(chan struct{}, 3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { finished <- struct{}{} }()
		manager.Handler().ServeHTTP(w, r)
	}))
	defer server.Close()
	server.Client().Timeout = 3 * time.Second
	for step := range 2 {
		response := proxyRequest(t, server, a, command.Spec.ModelToken, `{"model":"test-model","max_tokens":20,"stream":true}`)
		data := make([]byte, len(terminal))
		_, readErr := io.ReadFull(response.Body, data)
		// Match the native SDK: stop consuming once message_stop is dispatched.
		response.Body.Close()
		if response.StatusCode != 200 || readErr != nil || string(data) != terminal {
			t.Fatalf("step %d: status=%d data=%q error=%v", step, response.StatusCode, data, readErr)
		}
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Fatal("completed model response retained its HTTP handler")
		}
		latest, err := store.GetAttempt(context.Background(), a.ID)
		if err != nil || latest.Phase != "executing" || latest.ModelInFlight {
			t.Fatalf("completed stream interrupted the attempt: phase=%s inFlight=%v error=%v", latest.Phase, latest.ModelInFlight, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("model calls=%d; each explicit step must run once", calls.Load())
	}
}

func TestModelProxyRejectsIncompleteSSEWithoutRetry(t *testing.T) {
	for _, body := range []string{
		"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"\n\n",
		"event: message_stop\ndata: {\"type\":\"message_delta\"}\n\n",
	} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, body)
			}))
			defer upstream.Close()
			manager, store, _, a, command := proxyFixture(t, upstream.URL)
			server := httptest.NewTLSServer(manager.Handler())
			defer server.Close()
			response := proxyRequest(t, server, a, command.Spec.ModelToken, `{"model":"test-model","max_tokens":20,"stream":true}`)
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			latest, err := store.GetAttempt(context.Background(), a.ID)
			if err != nil || latest.Phase != "finished" || latest.ModelInFlight || calls.Load() != 1 {
				t.Fatalf("incomplete response accepted: phase=%s inFlight=%v calls=%d error=%v", latest.Phase, latest.ModelInFlight, calls.Load(), err)
			}
		})
	}
}

type modelTerminalWriter struct {
	*httptest.ResponseRecorder
	once  sync.Once
	check func()
}

func (w *modelTerminalWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), "message_stop") {
		w.once.Do(w.check)
	}
	return w.ResponseRecorder.Write(data)
}

func TestModelProxyClearsDurableModelSlotBeforeDeliveringTerminalFrame(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()
	manager, store, _, a, command := proxyFixture(t, upstream.URL)
	request := httptest.NewRequest("POST", "/api/daemon/model/"+a.ID+"/v1/messages", strings.NewReader(`{"model":"test-model","max_tokens":20,"stream":true}`))
	request.SetPathValue("id", a.ID)
	request.Header.Set("X-Api-Key", command.Spec.ModelToken)
	checked := false
	writer := &modelTerminalWriter{ResponseRecorder: httptest.NewRecorder(), check: func() {
		checked = true
		latest, err := store.GetAttempt(context.Background(), a.ID)
		manager.mu.Lock()
		active := len(manager.proxyCalls[a.ID])
		manager.mu.Unlock()
		if err != nil || latest.ModelInFlight || active != 0 {
			t.Errorf("terminal delivery raced the next model request: active=%d inFlight=%v error=%v", active, latest.ModelInFlight, err)
		}
	}}
	manager.modelProxy(writer, request)
	if !checked || writer.Code != 200 {
		t.Fatalf("terminal frame was not delivered: checked=%v status=%d", checked, writer.Code)
	}
}

func TestModelStreamCompletionAcrossChunks(t *testing.T) {
	prefix := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"text\":\"" + strings.Repeat("a", 70<<10) + "\"}\n\n"
	for _, newline := range []string{"\n", "\r", "\r\n"} {
		terminal := strings.Join([]string{": heartbeat", "event: message_stop", "data: {", "data: \"type\": \"message_stop\"}", "", ""}, newline)
		for _, width := range []int{1, 2, 7, 16 << 10, len(prefix) + len(terminal)} {
			stream := modelStreamCompletion{}
			input := prefix + terminal + "ignored trailing bytes"
			complete := false
			for start := 0; start < len(input); start += width {
				end := min(start+width, len(input))
				if offset, ok := stream.feed([]byte(input[start:end])); ok {
					expected := len(prefix) + len(terminal)
					if start+offset != expected && !(newline == "\r\n" && start+offset == expected-1) {
						t.Fatalf("newline %q chunk width %d: terminal offset=%d", newline, width, start+offset)
					}
					complete = true
					break
				}
			}
			if !complete {
				t.Fatalf("newline %q chunk width %d: complete terminal event was lost", newline, width)
			}
		}
	}
}

type completedModelDisconnectWriter struct{ *httptest.ResponseRecorder }

func (w *completedModelDisconnectWriter) FlushError() error { return context.Canceled }

func TestModelProxyTerminalFlushDisconnectDoesNotInterruptNextStep(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()
	manager, store, _, a, command := proxyFixture(t, upstream.URL)
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"model":"test-model","max_tokens":20,"stream":true}`))
	request.SetPathValue("id", a.ID)
	request.Header.Set("X-Api-Key", command.Spec.ModelToken)
	manager.modelProxy(&completedModelDisconnectWriter{httptest.NewRecorder()}, request)
	latest, err := store.GetAttempt(context.Background(), a.ID)
	if err != nil || latest.Phase != "executing" || latest.ModelInFlight {
		t.Fatalf("terminal flush disconnect interrupted execution: phase=%s inFlight=%v error=%v", latest.Phase, latest.ModelInFlight, err)
	}
}

func TestModelFailureDiagnosticsDoNotIncludeProviderDetails(t *testing.T) {
	for _, input := range []struct {
		err  error
		want string
	}{{context.Canceled, "request_cancelled"}, {context.DeadlineExceeded, "deadline_exceeded"}, {io.ErrUnexpectedEOF, "truncated_transport"}, {errors.New("secret-key https://private-provider.example request body"), "transport_error"}} {
		if cause := modelFailureCause(input.err); cause != input.want {
			t.Fatalf("diagnostic classification=%q expected=%q", cause, input.want)
		}
	}
}
func TestResultChecksRejectForgedOrMissingEvidence(t *testing.T) {
	run := storage.Run{AgentSnapshot: storage.Agent{Access: "write"}, Environment: json.RawMessage(`{"kind":"command","checks":[{"name":"test","arguments":["node","--test"]}]}`)}
	result := p.Result{CompletionID: "done", Status: "succeeded", CleanupConfirmed: true, Archive: &p.Artifact{SHA256: strings.Repeat("a", 64), Bytes: 1}}
	if validateResult(run, result) == nil {
		t.Fatal("missing configured check was accepted")
	}
	zero := 0
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	result.Checks = []p.CheckResult{{Name: "test", Command: "node --test", Status: "passed", ExitCode: &zero, StartedAt: stamp, FinishedAt: stamp}}
	if err := validateResult(run, result); err != nil {
		t.Fatal(err)
	}
	result.Checks[0].Command = "pretend"
	if validateResult(run, result) == nil {
		t.Fatal("forged check plan was accepted")
	}
}
