package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func testManager(t *testing.T) (*Manager, *storage.Store, string, storage.Room) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager, err := New(context.Background(), store, dir, Config{APIKey: "camp-test-keyless", MaxTokens: 1024, MaxSteps: 4, Timeout: 30 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	id := strings.Repeat("a", 32)
	resource, err := manager.resources.Create(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	room, err := store.SeedRoom(context.Background(), storage.Resource{ID: id, Name: "Room files", CurrentCommit: resource.Commit, Environment: json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	return manager, store, dir, room
}
func newAgent(t *testing.T, s *storage.Store, room storage.Room, handle, access string) storage.Agent {
	t.Helper()
	a, err := s.SaveAgent(context.Background(), storage.Agent{Name: handle, Handle: handle, Icon: "assistant", Access: access, Instructions: "Help with the request.", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.JoinRoom(context.Background(), room.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	return a
}
func sendTo(t *testing.T, m *Manager, room storage.Room, id string, agents ...string) storage.SendResult {
	t.Helper()
	if len(agents) == 0 {
		agents = []string{room.DefaultAgentID}
	}
	turn, err := m.Send(context.Background(), room.ID, storage.SendMessageInput{RequestID: id, Content: id, RecipientAgentIDs: agents})
	if err != nil {
		t.Fatal(err)
	}
	return turn
}
func waitRun(t *testing.T, m *Manager, id string) storage.Run {
	t.Helper()
	until := time.Now().Add(40 * time.Second)
	for time.Now().Before(until) {
		r, err := m.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !unfinished(r.Status) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("execution did not finish")
	return storage.Run{}
}
func awaitStart(t *testing.T, started <-chan storage.Run) storage.Run {
	t.Helper()
	select {
	case r := <-started:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("execution did not start")
		return storage.Run{}
	}
}
func noStart(t *testing.T, started <-chan storage.Run) {
	t.Helper()
	select {
	case r := <-started:
		t.Fatalf("unexpected concurrent execution: %+v", r)
	case <-time.After(350 * time.Millisecond):
	}
}

func TestSchedulerParallelReadersSerializedWritersAndPerAgentOrder(t *testing.T) {
	m, s, _, room := testManager(t)
	writer := room.DefaultAgentID
	writer2 := newAgent(t, s, room, "writer-two", "write")
	reader := newAgent(t, s, room, "reader", "read")
	started := make(chan storage.Run, 8)
	release := make(chan string, 8)
	m.mu.Lock()
	m.executeRun = func(ctx context.Context, e *execution) {
		m.mu.Lock()
		r := e.run
		m.mu.Unlock()
		dir := filepath.Join(m.dir, r.ID)
		err := m.resources.Prepare(ctx, r.ResourceID, r.BaselineCommit, dir)
		started <- r
		select {
		case name := <-release:
			if r.AgentSnapshot.Access == "write" && err == nil {
				err = os.WriteFile(filepath.Join(dir, "project", name), []byte(r.ID), 0600)
			}
		case <-ctx.Done():
			err = ctx.Err()
		}
		writeTestExport(t, dir)
		files, commit, captureErr := m.capture(context.Background(), r)
		m.finish(e, ctx, files, commit, errors.Join(err, captureErr))
	}
	m.unavailable = ""
	m.mu.Unlock()
	first := sendTo(t, m, room, "first", writer).Runs[0]
	a := awaitStart(t, started)
	if a.ID != first.ID {
		t.Fatal("wrong first run")
	}
	second := sendTo(t, m, room, "second-writer", writer2.ID).Runs[0]
	third := sendTo(t, m, room, "reader", reader.ID).Runs[0]
	fourth := sendTo(t, m, room, "same-agent-next", writer).Runs[0]
	b := awaitStart(t, started)
	if b.ID != third.ID {
		t.Fatalf("blocked writer consumed reader slot: %s", b.ID)
	}
	noStart(t, started)
	// Cancel only the reader; the active writer must remain alive.
	if _, err := m.Cancel(context.Background(), third.ID); err != nil {
		t.Fatal(err)
	}
	if got := waitRun(t, m, third.ID); got.Status != "cancelled" {
		t.Fatalf("reader: %+v", got)
	}
	noStart(t, started)
	release <- "first.txt"
	done := waitRun(t, m, first.ID)
	if !done.Published {
		t.Fatalf("writer was not published: %+v", done)
	}
	c := awaitStart(t, started)
	if c.ID != second.ID || c.BaselineCommit != done.ResultCommit {
		t.Fatalf("writer did not select latest resource: %+v", c)
	}
	noStart(t, started)
	release <- "second.txt"
	waitRun(t, m, second.ID)
	d := awaitStart(t, started)
	if d.ID != fourth.ID {
		t.Fatalf("agent work lost order: %+v", d)
	}
	release <- "third.txt"
	last := waitRun(t, m, fourth.ID)
	files, err := m.resources.ListFiles(context.Background(), room.ResourceID, last.ResultCommit)
	if err != nil || len(files) != 3 {
		t.Fatalf("serialized results lost files: %v %v", files, err)
	}
}

func TestQueuedCancellationAndShutdownPreserveUnstartedWork(t *testing.T) {
	m, s, dir, room := testManager(t)
	turn, err := s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "cancel", Content: "Cancel before start"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	run, err := m.Cancel(context.Background(), turn.Runs[0].ID)
	if err != nil || run.Status != "cancelled" || run.SessionID != "" {
		t.Fatalf("queued cancel: %+v %v", run, err)
	}
	turn, err = s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "restart", Content: "Retain pending work"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, err := New(context.Background(), s, dir, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown(context.Background())
	got, err := again.Get(context.Background(), turn.Runs[0].ID)
	if err != nil || got.Status != "queued" {
		t.Fatalf("restart consumed queued work: %+v %v", got, err)
	}
}

func TestRecoveryRetainsPartialAndDoesNotPublish(t *testing.T) {
	m, s, dir, room := testManager(t)
	turn, err := s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "recover", Content: "Work"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ClaimRun(context.Background(), turn.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(m.dir, run.ID)
	if err = m.resources.Prepare(context.Background(), run.ResourceID, run.BaselineCommit, runDir); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(runDir, "project", "partial.txt"), []byte("recover me"), 0600); err != nil {
		t.Fatal(err)
	}
	writeTestExport(t, runDir)
	run.Output = "Saved before restart\n"
	if _, err = s.UpdateRunWithEvent(context.Background(), run, "output", run.Output); err != nil {
		t.Fatal(err)
	}
	if err = m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, err := New(context.Background(), s, dir, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown(context.Background())
	result, err := again.Get(context.Background(), run.ID)
	if err != nil || result.Status != "interrupted" || len(result.Files) != 1 || !strings.Contains(result.Output, "Saved before restart") || result.Published {
		t.Fatalf("recovery: %+v %v", result, err)
	}
	messages, err := s.ListMessages(context.Background(), room.ID, "", 0)
	if err != nil || len(messages) != 2 {
		t.Fatalf("recovery final missing: %+v %v", messages, err)
	}
}

func TestFinalWriteFailureProjectsTerminalAndSuspendsAdmission(t *testing.T) {
	m, s, dir, room := testManager(t)
	turn, err := s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "failure", Content: "Work"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ClaimRun(context.Background(), turn.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER reject_final BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT, 'forced final persistence failure'); END;`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &execution{run: run, cancel: cancel, done: make(chan struct{})}
	if err := m.resources.Prepare(ctx, run.ResourceID, run.BaselineCommit, filepath.Join(m.dir, run.ID)); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(m.dir, run.ID, "project", "recovery.txt")
	if err := os.WriteFile(scratch, []byte("Preserve after failed terminal write"), 0600); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.active[run.ID] = e
	m.mu.Unlock()
	m.finish(e, ctx, []storage.RunFile{}, "", nil)
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal("failed final write lost recovery inputs", err)
	}
	got, err := m.Get(ctx, run.ID)
	if err != nil || got.Status != "failed" || !strings.Contains(got.Error, "retained in memory") {
		t.Fatalf("false live state: %+v %v", got, err)
	}
	if available, _, _ := m.Availability(); available {
		t.Fatal("admitted after failed final save")
	}
}

func TestLateFinalizationReportsRevocationWithoutPrematureSuccess(t *testing.T) {
	m, s, _, room := testManager(t)
	ctx := context.Background()
	turn, err := s.SendMessage(ctx, room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "revoked-before-finish", Content: "Work"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ClaimRun(ctx, turn.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LeaveRoom(ctx, room.ID, run.AgentID); err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(ctx)
	defer cancel()
	e := &execution{run: run, cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.active[run.ID] = e
	m.mu.Unlock()
	m.finish(e, ctx, nil, run.BaselineCommit, nil)
	final, err := m.Get(ctx, run.ID)
	if err != nil || final.Status != "cancelled" || final.Published || strings.Contains(final.Output, "[Camp] succeeded") {
		t.Fatalf("revocation produced a misleading terminal result: %+v %v", final, err)
	}
}

func TestLargeEventRetainsBoundedProgress(t *testing.T) {
	m, s, _, room := testManager(t)
	turn, err := s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "large", Content: "Work"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ClaimRun(context.Background(), turn.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &execution{run: run, cancel: cancel}
	if err = m.record(e, "tool_result", strings.Repeat("Unicode text: 世界 ", 20000)); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListRunEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if len(last.Text) > storage.MaxRunEventSize || !strings.Contains(last.Text, "truncated") {
		t.Fatal("event limit failed")
	}
}

func TestRestartCancelsQueueAfterMembershipOrEnabledChange(t *testing.T) {
	for _, reason := range []string{"leave", "disable", "leave-rejoin", "disable-reenable"} {
		t.Run(reason, func(t *testing.T) {
			m, s, dir, room := testManager(t)
			turn, err := s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "pending", Content: "Do not strand this request"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(reason, "leave") {
				err = s.LeaveRoom(context.Background(), room.ID, room.DefaultAgentID)
				if err == nil && reason == "leave-rejoin" {
					err = s.JoinRoom(context.Background(), room.ID, room.DefaultAgentID)
				}
			} else {
				agent, loadErr := s.GetAgent(context.Background(), room.DefaultAgentID)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				agent.Enabled = false
				agent, err = s.SaveAgent(context.Background(), agent)
				if err == nil && reason == "disable-reenable" {
					agent.Enabled = true
					_, err = s.SaveAgent(context.Background(), agent)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = m.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			next, err := New(context.Background(), s, dir, Config{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Shutdown(context.Background())
			result, err := next.Get(context.Background(), turn.Runs[0].ID)
			if err != nil || result.Status != "cancelled" || result.SessionID != "" || result.CancellationReason == "" {
				t.Fatalf("stranded request: %+v %v", result, err)
			}
		})
	}
}

func TestMissingQueuedConfigurationFailsBeforeStartingNativeProcess(t *testing.T) {
	m, s, _, room := testManager(t)
	turn, err := s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "old-runtime", Content: "Use the selected model"}, storage.Budget{Model: "previous-model", RuntimeKey: "previous-runtime", MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.unavailable = ""
	m.mu.Unlock()
	m.Wake()
	result := waitRun(t, m, turn.Runs[0].ID)
	if result.Status != "failed" || result.SessionID != "" || !strings.Contains(result.Error, "private model configuration") {
		t.Fatalf("queued runtime changed silently: %+v", result)
	}
}

func TestMissingGitDisablesExecutionWithoutBlockingStartup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := New(context.Background(), store, dir, Config{APIKey: "test-key", MaxTokens: 1024, MaxSteps: 4, Timeout: time.Minute}, nil)
	if err != nil {
		t.Fatalf("missing optional runtime blocked startup: %v", err)
	}
	defer manager.Shutdown(context.Background())
	available, reason, _ := manager.Availability()
	if available || !strings.Contains(reason, "Git could not be found") {
		t.Fatalf("runtime availability: %v %q", available, reason)
	}
}
