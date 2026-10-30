package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func testCommand() p.Command {
	return p.Command{ID: "command-1", Kind: "run.start", OperationID: "attempt-1", Deadline: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), Spec: &p.ExecutionSpec{AttemptID: "attempt-1", RunID: "run-1", TimeoutSeconds: 30, ModelToken: "opaque-test-capability"}}
}

func TestJournalDurableDeduplicationAndRecovery(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	command := testCommand()
	created, err := j.accept(ctx, command)
	if err != nil || !created {
		t.Fatalf("accept: %v %v", created, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			created, err := j.accept(ctx, command)
			if err != nil || created {
				t.Errorf("duplicate: %v %v", created, err)
			}
		})
	}
	wg.Wait()
	changed := command
	changed.Spec = &p.ExecutionSpec{AttemptID: "changed"}
	if _, err := j.accept(ctx, changed); err == nil {
		t.Fatal("content change accepted")
	}
	if started, err := j.begin(ctx, command.OperationID); err != nil || !started {
		t.Fatal("first start", started, err)
	}
	if started, err := j.begin(ctx, command.OperationID); err != nil || started {
		t.Fatal("duplicate start", started, err)
	}
	if err := j.appendEvent(ctx, command.OperationID, "assistant", "first"); err != nil {
		t.Fatal(err)
	}
	if err := j.appendEvent(ctx, command.OperationID, "assistant", "second"); err != nil {
		t.Fatal(err)
	}
	if err := j.acknowledge(ctx, command.OperationID, 1); err != nil {
		t.Fatal(err)
	}
	if err := j.db.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	ops, err := j.operations(ctx)
	if err != nil || len(ops) != 1 || ops[0].State != "running" {
		t.Fatalf("reopen: %+v %v", ops, err)
	}
	events, err := j.events(ctx)
	if err != nil || len(events) != 1 || events[0].Sequence != 2 {
		t.Fatalf("outbox: %+v %v", events, err)
	}
	result := p.Result{CompletionID: "completion", Status: "interrupted", CleanupConfirmed: true, Checks: []p.CheckResult{}}
	if err := j.finish(ctx, command.OperationID, result, filepath.Join(directory, "result.tar")); err != nil {
		t.Fatal(err)
	}
	if err := j.finish(ctx, command.OperationID, p.Result{Status: "succeeded"}, "wrong"); err != nil {
		t.Fatal(err)
	}
	ops, err = j.operations(ctx)
	if err != nil || ops[0].Result.CompletionID != "completion" {
		t.Fatal("terminal manifest replaced", ops, err)
	}
	if err := j.receipt(ctx, command.OperationID, p.Receipt{Disposition: "committed"}); err != nil {
		t.Fatal(err)
	}
	created, err = j.accept(ctx, command)
	if err != nil || created {
		t.Fatal("terminal command replayed", created, err)
	}
	ops, err = j.operations(ctx)
	if err != nil || len(ops) != 0 {
		t.Fatal("terminal record active", ops, err)
	}
}

func TestLeaseUsesMonotonicRequestTimeAndCapsTTL(t *testing.T) {
	start := time.Now()
	d := &Daemon{grants: map[string]grant{}, active: map[string]*activeOperation{}, grantRequests: map[string]time.Time{"old": start.Add(-10 * time.Second), "new": start}}
	d.setGrant("attempt", "old", 600000, time.Now().Add(time.Hour).Format(time.RFC3339Nano))
	if remaining := time.Until(d.grants["attempt"].until); remaining > 50*time.Second || remaining < 49*time.Second {
		t.Fatalf("grant extended by delivery time: %v", remaining)
	}
	d.setGrant("short", "new", 5000, time.Now().Add(time.Second).Format(time.RFC3339Nano))
	if time.Until(d.grants["short"].until) > time.Second {
		t.Fatal("absolute expiry was extended")
	}
	d.setGrant("unknown", "missing", 60000, "")
	if _, ok := d.grants["unknown"]; ok {
		t.Fatal("a grant without a corresponding request was accepted")
	}
	d.grantRequests["expired"] = start.Add(-3 * time.Minute)
	d.setGrant("expired", "expired", 60000, "")
	if _, ok := d.grants["expired"]; ok {
		t.Fatal("a delayed expired grant was accepted")
	}
	for range 300 {
		d.newGrantRequest()
	}
	if len(d.grantRequests) > 256 {
		t.Fatal("unbounded lease request history")
	}
}

func TestMissingResumeNeverStartsExecutor(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	command := testCommand()
	command.Spec.Resume = true
	command.Spec.SessionID = "missing"
	command.Spec.ConversationID = "conversation"
	if _, err = j.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, err = j.begin(ctx, command.OperationID); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{config: Config{DataDir: directory, Executor: "native"}, journal: j, active: map[string]*activeOperation{}}
	d.execute(ctx, operation{Command: command, State: "running"})
	ops, err := j.operations(ctx)
	if err != nil || len(ops) != 1 || ops[0].Result == nil || ops[0].Result.Status != "interrupted" {
		encoded, _ := json.Marshal(ops)
		t.Fatalf("missing session did not interrupt: %s %v", encoded, err)
	}
}

func TestEndpointRejectsRedirectedCapabilities(t *testing.T) {
	for _, reference := range []string{"https://evil.example/model", "//evil.example/", "https://camp.example:444/model", "https://u:p@camp.example/path"} {
		if _, err := endpoint("https://camp.example", reference); err == nil {
			t.Fatalf("accepted %q", reference)
		}
	}
	if _, err := endpoint("http://camp.example", "/api"); err == nil {
		t.Fatal("nonlocal cleartext accepted")
	}
	if _, err := endpoint("http://127.0.0.1:1234", "/api"); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseExpiryStopsActiveOperationWithoutAnotherGrant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j, err := openJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	stopped := make(chan struct{})
	d := &Daemon{journal: j, grants: map[string]grant{}, active: map[string]*activeOperation{"expired": {deadline: time.Now().Add(-time.Second), cancel: func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}}}}
	done := make(chan struct{})
	go func() { d.schedule(ctx); close(done) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("expired execution was not stopped")
	}
	cancel()
	<-done
}

func TestAcceptedCancellationDoesNotStartFixture(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j, err := openJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	command := testCommand()
	if _, err = j.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	if err = j.cancel(ctx, command.OperationID); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{config: Config{Executor: "fixture", Slots: 1}, journal: j, grants: map[string]grant{}, active: map[string]*activeOperation{}}
	// A durable cancellation is handled before grants and execution. Poll just the
	// scheduler's first transition, then stop before transfer is attempted.
	scheduleCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { d.schedule(scheduleCtx); close(done) }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		ops, err := j.operations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ops[0].Result != nil {
			if ops[0].Result.Status != "cancelled" {
				t.Fatal(ops[0].Result.Status)
			}
			stop()
			<-done
			break
		}
		select {
		case <-ticker.C:
		case <-time.After(time.Second):
			t.Fatal("cancel not recorded")
		}
	}
	events, err := j.events(ctx)
	if err != nil || len(events) != 0 {
		t.Fatal("cancelled operation emitted execution events", events, err)
	}
}

func TestReceiptAndSessionEligibilityCommitTogether(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.db.Close() }()
	command := testCommand()
	command.Spec.ConversationID = "conversation"
	command.Spec.Generation = 3
	command.Spec.SessionID = "session"
	if _, err = j.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	if err = j.appendEvent(ctx, command.OperationID, "assistant", "Transferred progress"); err != nil {
		t.Fatal(err)
	}
	if err = j.finish(ctx, command.OperationID, p.Result{Status: "succeeded", Resumable: true}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = j.db.Exec(`CREATE TRIGGER reject_session BEFORE INSERT ON metadata BEGIN SELECT RAISE(ABORT,'injected session storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	receipt := p.Receipt{Disposition: "committed", Status: "succeeded"}
	if err = j.receipt(ctx, command.OperationID, receipt); err == nil {
		t.Fatal("session storage failure was ignored")
	}
	ops, err := j.operations(ctx)
	if err != nil || len(ops) != 1 || ops[0].State != "uploading" {
		t.Fatal("failed receipt transaction discarded the recoverable result", ops, err)
	}
	if _, err = j.db.Exec(`DROP TRIGGER reject_session`); err != nil {
		t.Fatal(err)
	}
	if err = j.receipt(ctx, command.OperationID, receipt); err != nil {
		t.Fatal(err)
	}
	if err = j.db.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	var session string
	if err = j.db.QueryRow("SELECT value FROM metadata WHERE key=?", "session:"+sessionKey(command.Spec)).Scan(&session); err != nil || session != "session" {
		t.Fatal("completed session eligibility was lost across restart", session, err)
	}
	if err = j.receipt(ctx, command.OperationID, p.Receipt{Disposition: "rejected", Reason: "Late control response"}); err != nil {
		t.Fatal(err)
	}
	var encoded []byte
	if err = j.db.QueryRow("SELECT receipt FROM commands WHERE operation_id=?", command.OperationID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var stored p.Receipt
	if json.Unmarshal(encoded, &stored) != nil || stored.Disposition != "committed" || stored.Status != "succeeded" {
		t.Fatal("late rejection changed the durable completion receipt", string(encoded))
	}
	var retainedBytes, events int
	if err = j.db.QueryRow("SELECT length(body)+COALESCE(length(result),0)+length(archive_path) FROM commands WHERE operation_id=?", command.OperationID).Scan(&retainedBytes); err != nil || retainedBytes != 0 {
		t.Fatal("terminal journal retained copied prompts or artifacts", retainedBytes, err)
	}
	if err = j.db.QueryRow("SELECT count(*) FROM events WHERE operation_id=?", command.OperationID).Scan(&events); err != nil || events != 0 {
		t.Fatal("terminal progress was not compacted", events, err)
	}
	if accepted, err := j.accept(ctx, command); err != nil || accepted {
		t.Fatal("compacted terminal command was executed again", accepted, err)
	}
	command.Deadline = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err = j.accept(ctx, command); err == nil {
		t.Fatal("compaction lost the original command digest")
	}
}

func TestStartupPrunesOnlyAcknowledgedScratch(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	for _, id := range []string{"completed", "uploading", "unknown"} {
		if err = os.MkdirAll(filepath.Join(directory, "operations", id), 0700); err != nil {
			t.Fatal(err)
		}
		if id == "unknown" {
			continue
		}
		command := testCommand()
		command.ID, command.OperationID = id, id
		if _, err = j.accept(ctx, command); err != nil {
			t.Fatal(err)
		}
		if err = j.finish(ctx, id, p.Result{Status: "failed"}, ""); err != nil {
			t.Fatal(err)
		}
		if id == "completed" {
			if err = j.receipt(ctx, id, p.Receipt{Disposition: "committed", Status: "failed"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	d := &Daemon{config: Config{DataDir: directory}, journal: j}
	if err = d.pruneTerminalScratch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(directory, "operations", "completed")); !os.IsNotExist(err) {
		t.Fatal("terminal scratch remained after restart", err)
	}
	for _, id := range []string{"uploading", "unknown"} {
		if _, err = os.Stat(filepath.Join(directory, "operations", id)); err != nil {
			t.Fatal("cleanup removed unacknowledged or unrelated data", id, err)
		}
	}
}

func TestEventPersistenceFailureSuspendsRunner(t *testing.T) {
	directory, source := t.TempDir(), t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := testCommand()
	command.Kind, command.Spec = "source.capture", nil
	command.Source = &p.SourceCapture{SourceID: "project", Revision: sourceRevision(source)}
	if _, err = j.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, err = j.begin(ctx, command.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err = j.db.Exec(`CREATE TRIGGER reject_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'injected event disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{config: Config{DataDir: directory, Executor: "fixture", Sources: map[string]string{"project": source}}, journal: j, output: io.Discard, active: map[string]*activeOperation{command.OperationID: {cancel: cancel}}}
	d.execute(ctx, operation{Command: command, State: "running"})
	if d.capabilities().Ready || ctx.Err() == nil {
		t.Fatal("journal failure did not suspend execution capability and stop active work")
	}
	ops, err := j.operations(context.Background())
	if err != nil || len(ops) != 1 || ops[0].Result == nil || ops[0].Result.Status != "interrupted" {
		t.Fatal("persistence failure was reported as success", ops, err)
	}
}

func TestSourceCannotContainSymlinkedRunnerData(t *testing.T) {
	source := t.TempDir()
	private := filepath.Join(source, "private-runner")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "runner-link")
	if err := os.Symlink(private, alias); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(private, "daemon.json"), []byte(`{"credential":"must-never-be-imported"}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{config: Config{DataDir: alias, Sources: map[string]string{"project": source}}}
	sources := d.sources()
	if len(sources) != 1 || sources[0].Available {
		t.Fatal("a source exposing symlinked private Runner storage was offered", sources)
	}
	command := p.Command{OperationID: "capture", Source: &p.SourceCapture{SourceID: "project", Revision: sourceRevision(source)}}
	result, _, err := d.capture(context.Background(), command, t.TempDir(), func(string, string) error { return nil })
	if err == nil || result.Archive != nil || !strings.Contains(err.Error(), "overlaps private Runner data") {
		t.Fatal("private Runner credentials entered an archive", result, err)
	}
}

func TestAcknowledgementRetainsRecoveryReceiptsWhileRunnerIsSuspended(t *testing.T) {
	directory := t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	ctx := context.Background()
	command := testCommand()
	if _, err := j.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, err := j.begin(ctx, command.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := j.finish(ctx, command.OperationID, p.Result{CompletionID: "unknown", Status: "interrupted", CleanupConfirmed: false}, ""); err != nil {
		t.Fatal(err)
	}
	if err := j.receipt(ctx, command.OperationID, p.Receipt{Disposition: "committed", Status: "interrupted"}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(directory, "operations", command.OperationID, "native-dependency-jobs")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(root, "owner.json")
	if err := os.WriteFile(receipt, []byte("retained process identity"), 0600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{config: Config{DataDir: directory}, journal: j, output: io.Discard, active: map[string]*activeOperation{}, failure: errors.New("cleanup unknown")}
	d.pruneAcknowledged(ctx, command.OperationID)
	if _, err := os.Stat(receipt); err != nil {
		t.Fatal("Server receipt discarded unconfirmed process recovery state", err)
	}
	// This startup-only cleanup is called after native recovery has independently
	// confirmed quiescence; it never changes the already interrupted result.
	if err := d.pruneTerminalScratch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatal("confirmed startup cleanup did not retire acknowledged scratch", err)
	}
}
