package remoteruns

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// Install a stopped scheduler before adding test barriers. HTTP/control handlers
// and fixture daemons use the same production code; no live function is swapped.
func preparationHarness(t *testing.T) (*remoteHarness, *Manager) {
	t.Helper()
	h := newRemoteHarness(t, runs.Config{})
	old := h.manager.Load()
	if err := old.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
	m := pausedPreparationManager(h, old)
	h.manager.Store(m)
	return h, m
}

func pausedPreparationManager(h *remoteHarness, old *Manager) *Manager {
	life, cancel := context.WithCancel(context.WithoutCancel(h.ctx))
	return &Manager{store: h.store, files: old.files, models: old.models, diagnostics: old.diagnostics, config: old.config, logger: old.logger, ctx: life, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), peers: map[string]*peer{}, proxyCalls: map[string]map[string]context.CancelFunc{}, prepared: make(chan preparationOutcome, 1), lastGC: time.Now()}
}

func awaitPreparation(t *testing.T, ch <-chan struct{}, reason string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(15 * time.Second):
		t.Fatal(reason)
	}
}

func TestPreparationBarrierPreservesDispatchHeartbeatCancellationAndBackpressure(t *testing.T) {
	h, m := preparationHarness(t)
	a := h.start(t, h.join(t, "prepared-runner", "fixture", nil, nativeexecutor.Config{}))
	b := h.start(t, h.join(t, "blocked-runner", "fixture", nil, nativeexecutor.Config{}))
	roomA, agentA := h.room(t, "Already prepared", a.config.RunnerID)
	roomB, agentB := h.room(t, "Blocked preparation", b.config.RunnerID)
	roomC, agentC := h.room(t, "Waiting admission", a.config.RunnerID)
	ready := h.send(t, roomA, agentA, "[fixture:wait]")
	claimed, attempt, err := h.store.ClaimRemoteRun(h.ctx, ready.ID, a.config.RunnerID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.prepare(h.ctx, claimed, attempt); err != nil {
		t.Fatal(err)
	}
	blocked := h.send(t, roomB, agentB, "Block only input construction.")
	queued := h.send(t, roomC, agentC, "Write after the input builder is free.")
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var building, maximum atomic.Int64
	m.build = func(ctx context.Context, r storage.Run, a storage.Attempt) (PreparedInput, error) {
		n := building.Add(1)
		defer building.Add(-1)
		for previous := maximum.Load(); n > previous && !maximum.CompareAndSwap(previous, n); previous = maximum.Load() {
		}
		input, err := m.buildInput(ctx, r, a)
		if err != nil || r.ID != blocked.ID {
			return input, err
		}
		close(started)
		// Model an uninterruptible filesystem operation. Cancellation intent
		// cannot certify that its final private write has quiesced yet.
		<-release
		if err := os.WriteFile(filepath.Join(m.operationDir(a.ID), "builder-exited"), []byte("private fixture"), 0600); err != nil {
			return input, err
		}
		return input, nil
	}
	go m.schedule()
	awaitPreparation(t, started, "preparation did not reach its barrier")
	working := h.barrier(t, ready.ID) // Must dispatch while the builder remains blocked.
	before, err := h.store.GetAttempt(h.ctx, working.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	remoteEventually(t, 15*time.Second, "heartbeat did not renew while preparation was blocked", func() bool {
		a, err := h.store.GetAttempt(h.ctx, working.AttemptID)
		return err == nil && a.LeaseUntil > before.LeaseUntil
	})
	if _, err = m.Cancel(h.actor, ready.ID); err != nil {
		t.Fatal(err)
	}
	if cancelled := h.finished(t, ready.ID); cancelled.Status != "cancelled" {
		t.Fatal(cancelled)
	}
	waiting, err := h.store.GetRun(h.ctx, queued.ID)
	if err != nil || waiting.Status != "queued" || waiting.AttemptID != "" {
		t.Fatal("queued work reserved an Attempt behind the builder", waiting, err)
	}
	if _, err = h.store.GetResourceLease(h.ctx, queued.ResourceID); errorCode(err) != "not_found" {
		t.Fatal("waiting work reserved a writer lease", err)
	}
	cancelled, err := m.Cancel(h.actor, blocked.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatal("unsent cancellation waited for the builder", cancelled, err)
	}
	retained, err := h.store.GetRunAttempt(h.ctx, blocked.ID)
	if err != nil || len(retained.Command) > 0 || retained.InputDigest != "" || retained.ModelInFlight {
		t.Fatal("blocked builder activated execution", retained, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(h.data, "camp.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(h.ctx, "UPDATE run_attempts SET updated_at=? WHERE id=?", time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339Nano), retained.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.collectTerminalScratch(h.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(m.operationDir(retained.ID)); err != nil {
		t.Fatal("collector removed a live builder's directory", err)
	}
	unblock()
	if done := h.finished(t, queued.ID); done.Status != "succeeded" {
		t.Fatal(done)
	}
	if maximum.Load() != 1 {
		t.Fatal("preparation exceeded one builder", maximum.Load())
	}
	retained, err = h.store.GetAttempt(h.ctx, retained.ID)
	if err != nil || retained.Phase != "finished" || len(retained.Command) > 0 || retained.InputDigest != "" {
		t.Fatal("late success revived cancelled work", retained, err)
	}
	if _, err = os.Stat(filepath.Join(m.operationDir(retained.ID), "builder-exited")); err != nil {
		t.Fatal("builder did not complete its final private write", err)
	}
	if err = m.collectTerminalScratch(h.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(m.operationDir(retained.ID)); !os.IsNotExist(err) {
		t.Fatal("quiesced terminal files were not collected", err)
	}
	h.mu.Lock()
	completions := h.completions[retained.ID]
	h.mu.Unlock()
	if completions != 0 {
		t.Fatal("cancelled unsent Attempt reached a Runner", completions)
	}
}

func TestPreparationShutdownDrainsBuilderBeforeCompletion(t *testing.T) {
	h, m := preparationHarness(t)
	runner := h.start(t, h.join(t, "shutdown-builder", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Shutdown preparation", runner.config.RunnerID)
	run := h.send(t, room, agent, "Build before shutdown.")
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	m.build = func(ctx context.Context, r storage.Run, a storage.Attempt) (PreparedInput, error) {
		input, err := m.buildInput(ctx, r, a)
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return input, err
	}
	go m.schedule()
	awaitPreparation(t, started, "shutdown fixture never started building")
	budget, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err := m.Shutdown(budget)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("shutdown reported completion before builder exit", err)
	}
	awaitPreparation(t, cancelled, "shutdown did not cancel the builder")
	select {
	case <-m.done:
		t.Fatal("scheduler released ownership before builder exit")
	default:
	}
	if err = h.store.Ping(h.ctx); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err = m.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
	a, err := h.store.GetRunAttempt(h.ctx, run.ID)
	if err != nil || len(a.Command) > 0 || a.InputDigest != "" {
		t.Fatal("shutdown activated a late preparation result", a, err)
	}
}

func TestPreparationRestartRebuildsUnsentAttemptWithFrozenSession(t *testing.T) {
	h, m := preparationHarness(t)
	runner := h.start(t, h.join(t, "restart-builder", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Restart preparation", runner.config.RunnerID)
	run := h.send(t, room, agent, "Retain the admitted identity across restart.")
	started := make(chan struct{})
	m.build = func(ctx context.Context, _ storage.Run, _ storage.Attempt) (PreparedInput, error) {
		close(started)
		<-ctx.Done()
		return PreparedInput{}, ctx.Err()
	}
	go m.schedule()
	awaitPreparation(t, started, "restart fixture never started building")
	before, err := h.store.GetRunAttempt(h.ctx, run.ID)
	if err != nil || before.Preparation == nil {
		t.Fatal(before, err)
	}
	if err = m.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
	next := pausedPreparationManager(h, m)
	if err = next.recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.manager.Store(next)
	go next.schedule()
	finished := h.finished(t, run.ID)
	after, err := h.store.GetRunAttempt(h.ctx, run.ID)
	if err != nil || finished.Status != "succeeded" || after.ID != before.ID || after.CommandID != before.CommandID || after.Preparation == nil || *after.Preparation != *before.Preparation || finished.SessionGeneration != before.Preparation.Generation {
		t.Fatal("unsent recovery changed its admitted session/Attempt", finished, before, after, err)
	}
}

func TestPreparationLostActivationReplyRetainsCommandAndDoesNotRebuild(t *testing.T) {
	h, m := preparationHarness(t)
	runner := h.start(t, h.join(t, "activation-reply", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Lost activation reply", runner.config.RunnerID)
	run := h.send(t, room, agent, "Write once using the committed command.")
	var builds, activations atomic.Int64
	committed := make(chan storage.Attempt, 1)
	m.build = func(ctx context.Context, r storage.Run, a storage.Attempt) (PreparedInput, error) {
		builds.Add(1)
		return m.buildInput(ctx, r, a)
	}
	m.activate = func(ctx context.Context, a storage.Attempt, in *PreparedInput) (storage.Attempt, error) {
		activations.Add(1)
		out, err := m.activateInput(ctx, a, in)
		if err == nil {
			committed <- out
			return storage.Attempt{}, errors.New("activation reply was lost")
		}
		return out, err
	}
	go m.schedule()
	finished := h.finished(t, run.ID)
	if finished.Status != "succeeded" || builds.Load() != 1 || activations.Load() != 1 {
		t.Fatal("lost activation reply restarted input or model work", finished, builds.Load(), activations.Load())
	}
	original := <-committed
	retained, err := h.store.GetAttempt(h.ctx, original.ID)
	if err != nil || string(retained.Command) != string(original.Command) || retained.ModelTokenHash != original.ModelTokenHash {
		t.Fatal(retained, err)
	}
	h.mu.Lock()
	calls := h.completions[original.ID]
	h.mu.Unlock()
	if calls != 1 {
		t.Fatal("activation retried execution", calls)
	}
}

func TestPreparedCommandRestartDispatchesOriginalWithoutBuilder(t *testing.T) {
	h, m := preparationHarness(t)
	runner := h.start(t, h.join(t, "prepared-restart", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Prepared restart", runner.config.RunnerID)
	run := h.send(t, room, agent, "Keep the original prepared command.")
	claimed, a, err := h.store.ClaimRemoteRun(h.ctx, run.ID, runner.config.RunnerID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.prepare(h.ctx, claimed, a); err != nil {
		t.Fatal(err)
	}
	before, err := h.store.GetAttempt(h.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Cancel before starting the paused loop so its committed command cannot
	// escape before the replacement scheduler is installed.
	m.cancel()
	go m.schedule()
	// Shutdown before releasing new work; a command may already have reached the
	// fixture, so retain it through the existing journal/receipt protocol.
	if err = m.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
	next := pausedPreparationManager(h, m)
	var builds atomic.Int64
	next.build = func(context.Context, storage.Run, storage.Attempt) (PreparedInput, error) {
		builds.Add(1)
		return PreparedInput{}, errors.New("committed input must not rebuild")
	}
	if err = next.recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.manager.Store(next)
	go next.schedule()
	finished := h.finished(t, run.ID)
	after, err := h.store.GetAttempt(h.ctx, a.ID)
	if err != nil || finished.Status != "succeeded" || builds.Load() != 0 || string(after.Command) != string(before.Command) || after.ModelTokenHash != before.ModelTokenHash {
		t.Fatal("committed restart changed execution identity", finished, builds.Load(), err)
	}
	if strings.Contains(string(after.Command), "private-upstream-key") {
		t.Fatal("server credential escaped")
	}
}

func TestPreparationLateResultCannotActivateAfterRevocationOrExpiry(t *testing.T) {
	for _, reason := range []string{"revocation", "lease_expiry"} {
		t.Run(reason, func(t *testing.T) {
			h, m := preparationHarness(t)
			runner := h.start(t, h.join(t, reason, "fixture", nil, nativeexecutor.Config{}))
			room, agent := h.room(t, "Late result "+reason, runner.config.RunnerID)
			run := h.send(t, room, agent, "Keep this preparation within current authority.")
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			m.build = func(ctx context.Context, r storage.Run, a storage.Attempt) (PreparedInput, error) {
				input, err := m.buildInput(ctx, r, a)
				close(started)
				<-release
				return input, err
			}
			go m.schedule()
			awaitPreparation(t, started, "late-result fixture never started building")
			a, err := h.store.GetRunAttempt(h.ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if reason == "revocation" {
				agent.Enabled = false
				if _, err = h.store.SaveAgent(h.actor, agent); err != nil {
					t.Fatal(err)
				}
			} else {
				db, err := sql.Open("sqlite", filepath.Join(h.data, "camp.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err = db.Exec("UPDATE run_attempts SET lease_expires_at=? WHERE id=?", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), a.ID); err != nil {
					t.Fatal(err)
				}
			}
			finished := h.finished(t, run.ID)
			if finished.Status == "succeeded" || finished.Published {
				t.Fatal("obsolete preparation completed work", finished)
			}
			unblock()
			remoteEventually(t, 5*time.Second, "late builder did not drain", func() bool { return m.currentPreparation() == nil })
			retained, err := h.store.GetAttempt(h.ctx, a.ID)
			if err != nil || len(retained.Command) > 0 || retained.InputDigest != "" || retained.ModelInFlight {
				t.Fatal("late result installed execution authority", retained, err)
			}
		})
	}
}

func TestPreparationRetriesUncommittedActivationWithTheSameCommand(t *testing.T) {
	h, m := preparationHarness(t)
	runner := h.start(t, h.join(t, "activation-retry", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Retry input activation", runner.config.RunnerID)
	run := h.send(t, room, agent, "Dispatch the stable command after persistence recovers.")
	db, err := sql.Open("sqlite", filepath.Join(h.data, "camp.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TRIGGER reject_first_activation BEFORE INSERT ON runner_commands WHEN NEW.kind='run.start' BEGIN SELECT RAISE(FAIL,'temporary activation failure'); END"); err != nil {
		t.Fatal(err)
	}
	var builds, activations atomic.Int64
	candidate := make(chan string, 1)
	m.build = func(ctx context.Context, r storage.Run, a storage.Attempt) (PreparedInput, error) {
		builds.Add(1)
		return m.buildInput(ctx, r, a)
	}
	m.activate = func(ctx context.Context, a storage.Attempt, in *PreparedInput) (storage.Attempt, error) {
		n := activations.Add(1)
		out, err := m.activateInput(ctx, a, in)
		if n == 1 {
			if err == nil {
				return out, errors.New("fixture did not reject its first activation")
			}
			candidate <- string(in.command)
			if _, dropErr := db.Exec("DROP TRIGGER reject_first_activation"); dropErr != nil {
				return out, dropErr
			}
		}
		return out, err
	}
	go m.schedule()
	finished := h.finished(t, run.ID)
	if finished.Status != "succeeded" || builds.Load() != 1 || activations.Load() != 2 {
		t.Fatal("activation retry rebuilt input or failed to recover", finished, builds.Load(), activations.Load())
	}
	retained, err := h.store.GetRunAttempt(h.ctx, run.ID)
	if err != nil || string(retained.Command) != <-candidate {
		t.Fatal("activation retry replaced its command/capability", retained, err)
	}
}

func TestPendingActivationDoesNotBlockUnrelatedCommittedDispatch(t *testing.T) {
	h, m := preparationHarness(t)
	config := h.join(t, "offline-prepared-peer", "fixture", nil, nativeexecutor.Config{})
	runner, err := h.store.GetRunner(h.ctx, config.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities{Executor: "fixture", Ready: true, Slots: 1, Fingerprint: "prepared-dispatch-fixture"}
	capabilities, _ := json.Marshal(caps)
	if _, err = h.store.ConnectRunner(h.ctx, runner.ID, storage.RunnerConnection{StorageID: runner.StorageID, Runtime: "fixture", Slots: 1, Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	room, agent := h.room(t, "Committed before connection", runner.ID)
	queued := h.send(t, room, agent, "Dispatch the retained command independently.")
	run, a, err := h.store.ClaimRemoteRun(h.ctx, queued.ID, runner.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.prepare(h.ctx, run, a); err != nil {
		t.Fatal(err)
	}
	defer h.store.InterruptAttempt(h.ctx, a.ID, "The protocol fixture did not start native execution.")
	other := h.start(t, h.join(t, "pending-activation-peer", "fixture", nil, nativeexecutor.Config{}))
	otherRoom, otherAgent := h.room(t, "Pending activation", other.config.RunnerID)
	pending := h.send(t, otherRoom, otherAgent, "Wait for persistence recovery.")
	failed := make(chan struct{})
	var once sync.Once
	m.activate = func(context.Context, storage.Attempt, *PreparedInput) (storage.Attempt, error) {
		once.Do(func() { close(failed) })
		return storage.Attempt{}, errors.New("temporary activation persistence failure")
	}
	go m.schedule()
	awaitPreparation(t, failed, "preparation never reached activation")
	defer m.Cancel(h.actor, pending.ID)
	connection, _, err := websocket.Dial(h.ctx, strings.Replace(h.server.URL, "https://", "wss://", 1)+"/api/daemon/connect", &websocket.DialOptions{HTTPClient: h.server.Client(), HTTPHeader: http.Header{"Authorization": []string{"Bearer " + config.Credential}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err = wsjson.Write(h.ctx, connection, p.Message{Type: "hello", RequestID: "prepared-dispatch-reconcile", Hello: &p.Hello{Version: p.Version, StorageID: runner.StorageID, BootID: "prepared-dispatch-boot", Capabilities: caps}}); err != nil {
		t.Fatal(err)
	}
	read, stop := context.WithTimeout(h.ctx, 3*time.Second)
	defer stop()
	var welcome p.Message
	if err = wsjson.Read(read, connection, &welcome); err != nil || welcome.Type != "welcome" {
		t.Fatal("prepared peer did not connect", err)
	}
	// This peer sends no heartbeat, so a command here must come from scheduler
	// dispatch despite the unrelated activation continuing to fail.
	var message p.Message
	if err = wsjson.Read(read, connection, &message); err != nil || message.Type != "command" || message.Command == nil || message.Command.ID != a.CommandID {
		t.Fatal("pending activation blocked unrelated committed dispatch", err)
	}
}
