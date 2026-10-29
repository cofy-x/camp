package remoteruns

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type remoteTestDaemon struct {
	config daemon.Config
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

func (d *remoteTestDaemon) stop(t *testing.T) {
	t.Helper()
	d.once.Do(func() {
		d.cancel()
		select {
		case err := <-d.done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Error("daemon shutdown did not finish")
		}
	})
}

type remoteHarness struct {
	ctx          context.Context
	actor        context.Context
	cancel       context.CancelFunc
	store        *storage.Store
	manager      atomic.Pointer[Manager]
	server       *httptest.Server
	data, ca     string
	config       Config
	runners      []*remoteTestDaemon
	lostResponse atomic.Bool
	mu           sync.Mutex
	results      map[string]p.Result
	completions  map[string]int
}

func newRemoteHarness(t *testing.T, model runs.Config) *remoteHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	h := &remoteHarness{ctx: ctx, cancel: cancel, data: t.TempDir(), results: map[string]p.Result{}, completions: map[string]int{}}
	var err error
	h.store, err = storage.Open(filepath.Join(h.data, "camp.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err = h.store.EnsureSetupToken(ctx, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	owner, err := h.store.SetupAdmin(ctx, strings.Repeat("a", 64), "owner", "Owner", strings.Repeat("h", 64), "owner-session", time.Now().Add(time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	h.actor = storage.WithUser(ctx, owner)
	h.config = Config{DataDir: h.data, AllowFixture: true, Concurrency: 2, Models: model, LeaseDuration: time.Minute}
	m, err := New(ctx, h.store, h.config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Store(m)
	h.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manager := h.manager.Load()
		if manager == nil {
			http.Error(w, "restarting", 503)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/complete") {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "body", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
			var result p.Result
			_ = json.Unmarshal(data, &result)
			parts := strings.Split(r.URL.Path, "/")
			id := parts[len(parts)-2]
			h.mu.Lock()
			h.results[id] = result
			h.completions[id]++
			h.mu.Unlock()
			recorder := httptest.NewRecorder()
			manager.Handler().ServeHTTP(recorder, r)
			var receipt p.Receipt
			_ = json.Unmarshal(recorder.Body.Bytes(), &receipt)
			if receipt.Disposition == "committed" && h.lostResponse.CompareAndSwap(true, false) {
				panic(http.ErrAbortHandler)
			}
			for key, values := range recorder.Header() {
				w.Header()[key] = values
			}
			w.WriteHeader(recorder.Code)
			_, _ = w.Write(recorder.Body.Bytes())
			return
		}
		manager.Handler().ServeHTTP(w, r)
	}))
	h.ca = filepath.Join(h.data, "test-ca.pem")
	if err = os.WriteFile(h.ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: h.server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, runner := range h.runners {
			runner.stop(t)
		}
		if m := h.manager.Load(); m != nil {
			shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
			if err := m.Shutdown(shutdown); err != nil {
				t.Error(err)
			}
			stop()
		}
		h.server.Close()
		cancel()
		if err := h.store.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}
func remoteEventually(t *testing.T, timeout time.Duration, reason string, check func() bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal(reason)
		}
	}
}
func (h *remoteHarness) join(t *testing.T, name string, executor string, sources map[string]string, native nativeexecutor.Config) daemon.Config {
	t.Helper()
	_, token, err := h.store.CreateRunnerEnrollment(h.actor, name, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	config, err := daemon.Join(h.ctx, daemon.Config{DataDir: t.TempDir(), ServerURL: h.server.URL, Name: name, CAFile: h.ca, Executor: executor, Slots: 1, Sources: sources}, token)
	if err != nil {
		t.Fatal(err)
	}
	config.Native = native
	return config
}
func (h *remoteHarness) start(t *testing.T, config daemon.Config) *remoteTestDaemon {
	t.Helper()
	ctx, cancel := context.WithCancel(h.ctx)
	d := &remoteTestDaemon{config: config, cancel: cancel, done: make(chan error, 1)}
	h.runners = append(h.runners, d)
	go func() { d.done <- daemon.Run(ctx, config, io.Discard) }()
	remoteEventually(t, 90*time.Second, "runner did not connect", func() bool {
		select {
		case err := <-d.done:
			d.done <- err
			t.Fatalf("daemon stopped before connecting: %v", err)
		default:
		}
		return h.manager.Load().IsOnline(config.RunnerID)
	})
	registered, err := h.store.GetRunner(h.ctx, config.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	var caps p.Capabilities
	_ = json.Unmarshal(registered.Capabilities, &caps)
	if !caps.Ready {
		t.Fatalf("runner not ready: %s", caps.Reason)
	}
	return d
}
func (h *remoteHarness) room(t *testing.T, name, runnerID string) (storage.Room, storage.Agent) {
	t.Helper()
	m := h.manager.Load()
	resourceID := newID()
	files, err := m.files.Create(h.ctx, resourceID)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := h.store.SaveAgent(h.actor, storage.Agent{PreferredRunnerID: runnerID, Name: name + " writer", Handle: "writer-" + newID(), Access: "write", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	room, err := h.store.CreateRoomWithResource(h.actor, storage.Room{Name: name, DefaultAgentID: agent.ID}, storage.Resource{ID: resourceID, Name: name, CurrentCommit: files.Commit})
	if err != nil {
		t.Fatal(err)
	}
	room, err = h.store.SetRoomRunner(h.actor, room.ID, runnerID, room.RunnerRevision)
	if err != nil {
		t.Fatal(err)
	}
	return room, agent
}
func (h *remoteHarness) send(t *testing.T, room storage.Room, agent storage.Agent, prompt string) storage.Run {
	t.Helper()
	result, err := h.manager.Load().Send(h.actor, room.ID, storage.SendMessageInput{RequestID: newID(), Content: prompt, RecipientAgentIDs: []string{agent.ID}, RunnerRevision: &room.RunnerRevision})
	if err != nil || len(result.Runs) != 1 {
		t.Fatalf("send: %+v %v", result, err)
	}
	return result.Runs[0]
}
func (h *remoteHarness) finished(t *testing.T, id string) storage.Run {
	t.Helper()
	var run storage.Run
	remoteEventually(t, 25*time.Second, "run did not finish: "+id, func() bool { run, _ = h.store.GetRun(h.ctx, id); return terminal(run.Status) })
	return run
}
func (h *remoteHarness) barrier(t *testing.T, id string) storage.Run {
	t.Helper()
	var run storage.Run
	remoteEventually(t, 15*time.Second, "fixture did not reach its execution barrier", func() bool {
		run, _ = h.store.GetRun(h.ctx, id)
		return strings.Contains(run.Output, "Waiting at the cancellation barrier")
	})
	return run
}
func (h *remoteHarness) request(t *testing.T, config daemon.Config, method, path string, body []byte, headers map[string]string) p.Receipt {
	t.Helper()
	request, err := http.NewRequestWithContext(h.ctx, method, h.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+config.Credential)
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := h.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("request failed: %d %s %v", response.StatusCode, data, err)
	}
	var receipt p.Receipt
	if err = json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestRemoteTLSRunnersPublishOnceAndContinueReview(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	a := h.start(t, h.join(t, "alpha", "fixture", nil, nativeexecutor.Config{}))
	b := h.start(t, h.join(t, "bravo", "fixture", nil, nativeexecutor.Config{}))
	roomA, agentA := h.room(t, "Alpha work", a.config.RunnerID)
	roomB, agentB := h.room(t, "Bravo work", b.config.RunnerID)
	h.lostResponse.Store(true)
	first := h.send(t, roomA, agentA, "Draft the release note.")
	second := h.send(t, roomB, agentB, "Draft the review note.")
	first = h.finished(t, first.ID)
	second = h.finished(t, second.ID)
	for _, run := range []storage.Run{first, second} {
		if run.Status != "succeeded" || !run.Published {
			t.Fatalf("remote publication: %+v", run)
		}
	}
	for _, test := range []struct {
		run  storage.Run
		name string
	}{{first, "alpha"}, {second, "bravo"}} {
		content, err := h.manager.Load().files.ReadFile(h.ctx, test.run.ResourceID, test.run.ResultCommit, "result.txt")
		if err != nil || !strings.Contains(string(content), "Runner "+test.name) {
			t.Fatalf("incorrect runner result %q %v", content, err)
		}
		events, err := h.store.ListRunEvents(h.ctx, test.run.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		starts := 0
		for _, event := range events {
			if event.Type == "fixture" {
				starts++
			}
		}
		if starts != 1 {
			t.Fatalf("duplicate execution: %d", starts)
		}
	}
	h.mu.Lock()
	result := h.results[first.AttemptID]
	h.mu.Unlock()
	raw, _ := json.Marshal(result)
	receipt := h.request(t, a.config, "POST", "/api/daemon/operations/"+first.AttemptID+"/complete", raw, nil)
	if receipt.Disposition != "committed" || receipt.Status != "succeeded" {
		t.Fatal(receipt)
	}
	publications, err := h.store.ListPublications(h.ctx, first.ResourceID, 0)
	if err != nil || len(publications) != 1 {
		t.Fatalf("duplicate publication %v %v", publications, err)
	}
	messages, err := h.store.ListMessages(h.ctx, roomA.ID, "", 0)
	if err != nil || len(messages) != 2 {
		t.Fatalf("duplicate completion messages %d %v", len(messages), err)
	}
	owner, _ := storage.UserFromContext(h.actor)
	review, err := h.store.CreateReview(h.actor, roomA.ID, storage.CreateReviewInput{RequestID: "review", Title: "Release note", ResourceID: first.ResourceID, Path: "result.txt", Commit: first.ResultCommit, ReviewerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	review, err = h.store.ActOnReview(h.actor, review.Review.ID, storage.ReviewActionInput{RequestID: "changes", Revision: review.Review.Revision, Action: "request_changes", Comment: "Add the acceptance criteria."})
	if err != nil {
		t.Fatal(err)
	}
	revised := h.finished(t, h.send(t, roomA, agentA, "Add acceptance criteria to the release note.").ID)
	if revised.Status != "succeeded" {
		t.Fatal(revised)
	}
	review, err = h.store.ActOnReview(h.actor, review.Review.ID, storage.ReviewActionInput{RequestID: "resubmit", Revision: review.Review.Revision, Action: "resubmit", Commit: revised.ResultCommit})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"approve", "deliver"} {
		review, err = h.store.ActOnReview(h.actor, review.Review.ID, storage.ReviewActionInput{RequestID: action, Revision: review.Review.Revision, Action: action})
		if err != nil {
			t.Fatal(err)
		}
	}
	delivered := review.Review.DeliveredCommit
	if next := h.finished(t, h.send(t, roomA, agentA, "Start a separate later revision.").ID); next.Status != "succeeded" {
		t.Fatal(next)
	}
	retained, err := h.store.GetReview(h.ctx, review.Review.ID)
	if err != nil || retained.Review.DeliveredCommit != delivered {
		t.Fatal("delivery changed", retained, err)
	}
}

func TestRemoteCompletionReceiptSurvivesTransferCollection(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	a := h.start(t, h.join(t, "alpha", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Retained delivery", a.config.RunnerID)
	run := h.finished(t, h.send(t, room, agent, "Create a retained result.").ID)
	if run.Status != "succeeded" || !run.Published {
		t.Fatal(run)
	}
	a.stop(t)
	h.mu.Lock()
	result := h.results[run.AttemptID]
	h.mu.Unlock()
	if result.Archive == nil {
		t.Fatal("completed writer did not submit an archive")
	}
	manager := h.manager.Load()
	if _, err := os.Stat(manager.operationDir(run.AttemptID)); err != nil {
		t.Fatal("transfer evidence missing before collection", err)
	}
	// Advance only this terminal operation's retention clock. Its receipt and
	// immutable resource remain real records produced through the daemon API.
	db, err := sql.Open("sqlite", filepath.Join(h.data, "camp.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(h.ctx, `UPDATE run_attempts SET updated_at=? WHERE id=?`, time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339Nano), run.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err = manager.collectTerminalScratch(h.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(manager.operationDir(run.AttemptID)); !os.IsNotExist(err) {
		t.Fatal("terminal transfer files were not collected", err)
	}
	raw, _ := json.Marshal(result)
	for _, retry := range []struct {
		method, suffix string
		body           []byte
		headers        map[string]string
	}{
		{"POST", "/complete", raw, nil},
		{"PUT", "/artifact", []byte("a retried body is unnecessary after durable completion"), map[string]string{"X-Camp-SHA256": result.Archive.SHA256}},
	} {
		receipt := h.request(t, a.config, retry.method, "/api/daemon/operations/"+run.AttemptID+retry.suffix, retry.body, retry.headers)
		if receipt.Disposition != "committed" || receipt.Status != "succeeded" {
			t.Fatal("receipt replay required collected transfer bytes", receipt)
		}
	}
	content, err := manager.files.ReadFile(h.ctx, run.ResourceID, run.ResultCommit, "result.txt")
	if err != nil || !strings.Contains(string(content), "Runner alpha") {
		t.Fatal("transfer collection removed immutable delivery", string(content), err)
	}
	publications, err := h.store.ListPublications(h.ctx, run.ResourceID, 0)
	if err != nil || len(publications) != 1 {
		t.Fatal("receipt replay republished a result", publications, err)
	}
	if _, err = os.Stat(manager.operationDir(run.AttemptID)); !os.IsNotExist(err) {
		t.Fatal("receipt replay recreated collected transfer files", err)
	}
}

func TestRemoteReconnectAndServerRestartKeepAttemptAndCancellation(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	a := h.start(t, h.join(t, "alpha", "fixture", nil, nativeexecutor.Config{}))
	b := h.start(t, h.join(t, "bravo", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Waiting", a.config.RunnerID)
	otherRoom, otherAgent := h.room(t, "Other", b.config.RunnerID)
	waiting := h.barrier(t, h.send(t, room, agent, "[fixture:wait]").ID)
	before, err := h.store.GetRunner(h.ctx, a.config.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Load().StopRunner(a.config.RunnerID)
	remoteEventually(t, 12*time.Second, "runner did not reconnect", func() bool {
		r, _ := h.store.GetRunner(h.ctx, a.config.RunnerID)
		return r.ConnectionGeneration > before.ConnectionGeneration && h.manager.Load().IsOnline(r.ID)
	})
	old := h.manager.Swap(nil)
	shutdown, stop := context.WithTimeout(h.ctx, 10*time.Second)
	if err = old.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	stop()
	fresh, err := New(h.ctx, h.store, h.config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Store(fresh)
	remoteEventually(t, 12*time.Second, "runner did not reconnect to restarted server", func() bool { return fresh.IsOnline(a.config.RunnerID) && fresh.IsOnline(b.config.RunnerID) })
	current, err := h.store.GetRun(h.ctx, waiting.ID)
	if err != nil || current.AttemptID != waiting.AttemptID || terminal(current.Status) {
		t.Fatal("server restart replayed or lost task", current, err)
	}
	other := h.finished(t, h.send(t, otherRoom, otherAgent, "Independent work.").ID)
	if other.Status != "succeeded" {
		t.Fatal(other)
	}
	if _, err = fresh.Cancel(h.actor, waiting.ID); err != nil {
		t.Fatal(err)
	}
	if finished := h.finished(t, waiting.ID); finished.Status != "cancelled" || finished.Published {
		t.Fatal(finished)
	}
	events, err := h.store.ListRunEvents(h.ctx, waiting.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, event := range events {
		if strings.Contains(event.Text, "Execution accepted") {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("reconnected execution started %d times", accepted)
	}
}

func TestRemoteDaemonRestartDoesNotReplayExecution(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	config := h.join(t, "alpha", "fixture", nil, nativeexecutor.Config{})
	runner := h.start(t, config)
	room, agent := h.room(t, "Restart", config.RunnerID)
	run := h.barrier(t, h.send(t, room, agent, "[fixture:wait]").ID)
	runner.stop(t)
	h.start(t, config)
	finished := h.finished(t, run.ID)
	if finished.Status != "interrupted" || finished.Published || finished.AttemptID != run.AttemptID {
		t.Fatal(finished)
	}
	events, err := h.store.ListRunEvents(h.ctx, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, event := range events {
		if strings.Contains(event.Text, "Execution accepted") {
			starts++
		}
	}
	if starts != 1 {
		t.Fatal("daemon replayed a running request", starts)
	}
}

func TestRemoteRejectsCorruptChecksAndUnsafeArchives(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	runner := h.start(t, h.join(t, "alpha", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Checked", runner.config.RunnerID)
	resource, err := h.store.GetResource(h.ctx, room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	resource.Environment, _ = json.Marshal(projectenv.Profile{Kind: "command", Checks: []projectenv.Check{{Name: "verify", Arguments: []string{"true"}}}})
	if _, err = h.store.UpdateResource(h.actor, resource); err != nil {
		t.Fatal(err)
	}
	badCheck := h.finished(t, h.send(t, room, agent, "[fixture:corrupt-check]").ID)
	if badCheck.Status != "interrupted" || badCheck.Published {
		t.Fatal(badCheck)
	}
	resource.Environment = json.RawMessage(`{}`)
	if _, err = h.store.UpdateResource(h.actor, resource); err != nil {
		t.Fatal(err)
	}
	badArchive := h.barrier(t, h.send(t, room, agent, "[fixture:wait]").ID)
	operation := "/api/daemon/operations/" + badArchive.AttemptID
	if receipt := h.request(t, runner.config, "POST", operation+"/finalizing", []byte(`{}`), nil); receipt.Disposition != "accepted" {
		t.Fatal(receipt)
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err = writer.WriteHeader(&tar.Header{Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0644, Size: 4}); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte("bad\n"))
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	hash := digest(archive.Bytes())
	if receipt := h.request(t, runner.config, "PUT", operation+"/artifact", archive.Bytes(), map[string]string{"X-Camp-SHA256": hash, "Content-Type": "application/x-tar"}); receipt.Disposition != "accepted" {
		t.Fatal(receipt)
	}
	result := p.Result{CompletionID: "unsafe-result", Status: "succeeded", Checks: []p.CheckResult{}, CleanupConfirmed: true, Archive: &p.Artifact{SHA256: hash, Bytes: int64(archive.Len())}}
	raw, _ := json.Marshal(result)
	if receipt := h.request(t, runner.config, "POST", operation+"/complete", raw, nil); receipt.Disposition != "rejected" {
		t.Fatal(receipt)
	}
	finished := h.finished(t, badArchive.ID)
	if finished.Status != "interrupted" || finished.Published {
		t.Fatal(finished)
	}
	current, err := h.store.GetResource(h.ctx, room.ResourceID)
	if err != nil || current.CurrentCommit != resource.CurrentCommit {
		t.Fatal("untrusted output replaced files", current, err)
	}
}

func TestRemoteSourceImportExecutesOnAnotherRunner(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	source := t.TempDir()
	original := []byte("Original local evidence.\n")
	if err := os.WriteFile(filepath.Join(source, "brief.txt"), original, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".env"), []byte("PRIVATE_TOKEN=local-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := h.start(t, h.join(t, "alpha", "fixture", map[string]string{"project": source}, nativeexecutor.Config{}))
	b := h.start(t, h.join(t, "bravo", "fixture", nil, nativeexecutor.Config{}))
	runner, err := h.store.GetRunner(h.ctx, a.config.RunnerID)
	if err != nil || len(runner.Sources) != 1 {
		t.Fatal(runner, err)
	}
	item, err := h.store.CreateSourceImport(h.actor, storage.SourceImportInput{RequestID: "source", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: runner.Sources[0].Revision, RoomName: "Imported work"})
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Load().Wake()
	remoteEventually(t, 20*time.Second, "source capture did not finish", func() bool { item, _ = h.store.GetSourceImport(h.ctx, item.ID); return terminal(item.Status) })
	if item.Status != "succeeded" {
		t.Fatal(item)
	}
	room, err := h.store.GetRoom(h.ctx, item.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	room, err = h.store.SetRoomRunner(h.actor, room.ID, b.config.RunnerID, room.RunnerRevision)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := h.store.CreateRoomAgent(h.actor, room.ID, storage.Agent{PreferredRunnerID: b.config.RunnerID, Name: "Imported writer", Handle: "imported-writer", Access: "write", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	run := h.finished(t, h.send(t, room, agent, "Read the imported evidence and draft a result.").ID)
	if run.Status != "succeeded" || run.RunnerID != b.config.RunnerID {
		t.Fatal(run)
	}
	content, err := h.manager.Load().files.ReadFile(h.ctx, item.ResourceID, run.ResultCommit, "brief.txt")
	if err != nil || !bytes.Equal(content, original) {
		t.Fatal(string(content), err)
	}
	if _, err = h.manager.Load().files.ReadFile(h.ctx, item.ResourceID, run.ResultCommit, ".env"); err == nil {
		t.Fatal("private source file was imported")
	}
	local, err := os.ReadFile(filepath.Join(source, "brief.txt"))
	if err != nil || !bytes.Equal(local, original) {
		t.Fatal("source changed", err)
	}
	if _, err = os.Stat(filepath.Join(source, "result.txt")); !os.IsNotExist(err) {
		t.Fatal("execution wrote into the original source", err)
	}
}

func TestRemoteMachineBoundaryRejectsBrowserCredentials(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	config := h.join(t, "alpha", "fixture", nil, nativeexecutor.Config{})
	for _, header := range []string{"Origin", "Cookie"} {
		request, err := http.NewRequestWithContext(h.ctx, "GET", h.server.URL+"/api/daemon/connect", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+config.Credential)
		request.Header.Set(header, "untrusted")
		response, err := h.server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatal(fmt.Sprintf("%s status %d", header, response.StatusCode))
		}
	}
}
