package remoteruns

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/runs"
)

func nativeRecoveryConfig(t *testing.T) nativeexecutor.Config {
	t.Helper()
	layerRuntime, dshRuntime := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME"), os.Getenv("CAMP_TEST_DSH_RUNTIME")
	if layerRuntime == "" || dshRuntime == "" {
		t.Skip("Set CAMP_TEST_LAYERFS_RUNTIME and CAMP_TEST_DSH_RUNTIME for Linux native recovery")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	helper := os.Getenv("CAMP_TEST_SANDBOX_HELPER")
	if helper == "" {
		helper = filepath.Join(t.TempDir(), "camp-sandbox")
		command := exec.Command("go", "build", "-o", helper, "../../cmd/camp-sandbox")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build sandbox: %v %s", err, output)
		}
	}
	return nativeexecutor.Config{RuntimeDir: dshRuntime, LayerFSRuntimeDir: layerRuntime, SandboxHelper: helper, NodePath: node}
}

type holdingProvider struct {
	server             *httptest.Server
	started, ended     chan struct{}
	calls              atomic.Int32
	startOnce, endOnce sync.Once
}

func holdNativeModel(t *testing.T) *holdingProvider {
	t.Helper()
	p := &holdingProvider{started: make(chan struct{}), ended: make(chan struct{})}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		p.calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"held-native-model\",\"model\":\"deepseek-v4-pro\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		w.(http.Flusher).Flush()
		p.startOnce.Do(func() { close(p.started) })
		<-r.Context().Done()
		p.endOnce.Do(func() { close(p.ended) })
	}))
	t.Cleanup(p.server.Close)
	return p
}
func waitNativeBarrier(t *testing.T, barrier <-chan struct{}, reason string) {
	t.Helper()
	select {
	case <-barrier:
	case <-time.After(60 * time.Second):
		t.Fatal(reason)
	}
}

func TestRemoteNativeControlReconnectAndCancellation(t *testing.T) {
	native := nativeRecoveryConfig(t)
	provider := holdNativeModel(t)
	h := newRemoteHarness(t, runs.Config{APIKey: "local-provider-test-key", BaseURL: provider.server.URL, Timeout: 90 * time.Second})
	runner := h.start(t, h.join(t, "native-cancel", "native", nil, native))
	room, agent := h.room(t, "Native cancellation", runner.config.RunnerID)
	run := h.send(t, room, agent, "Wait for a result.")
	waitNativeBarrier(t, provider.started, "native model request did not start")
	run, err := h.store.GetRun(h.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := h.store.GetAttempt(h.ctx, run.AttemptID)
	if err != nil || !attempt.ModelInFlight {
		t.Fatal("model uncertainty not durable", attempt, err)
	}
	before, err := h.store.GetRunner(h.ctx, runner.config.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Load().StopRunner(runner.config.RunnerID)
	remoteEventually(t, 15*time.Second, "native control connection did not recover", func() bool {
		current, _ := h.store.GetRunner(h.ctx, runner.config.RunnerID)
		return current.ConnectionGeneration > before.ConnectionGeneration && h.manager.Load().IsOnline(current.ID)
	})
	select {
	case <-provider.ended:
		t.Fatal("control disconnect terminated an independent model stream")
	default:
	}
	if _, err = h.manager.Load().Cancel(h.actor, run.ID); err != nil {
		t.Fatal(err)
	}
	finished := h.finished(t, run.ID)
	if finished.Status != "cancelled" || finished.Published {
		t.Fatalf("native cancellation did not confirm cleanup: %+v", finished)
	}
	waitNativeBarrier(t, provider.ended, "cancellation did not stop the upstream request")
	if provider.calls.Load() != 1 {
		t.Fatalf("cancelled model request replayed %d times", provider.calls.Load())
	}
	registered, err := h.store.GetRunner(h.ctx, runner.config.RunnerID)
	if err != nil || !registered.Enabled {
		t.Fatal("clean cancellation disabled the runner", registered, err)
	}
	attempt, err = h.store.GetAttempt(h.ctx, run.AttemptID)
	if err != nil || attempt.ModelInFlight || attempt.Phase != "finished" {
		t.Fatal(attempt, err)
	}
}

func TestRemoteNativeAgentRevocationStopsActiveModel(t *testing.T) {
	native := nativeRecoveryConfig(t)
	provider := holdNativeModel(t)
	h := newRemoteHarness(t, runs.Config{APIKey: "local-provider-test-key", BaseURL: provider.server.URL, Timeout: 90 * time.Second})
	runner := h.start(t, h.join(t, "native-revoked", "native", nil, native))
	room, agent := h.room(t, "Revoked model authority", runner.config.RunnerID)
	run := h.send(t, room, agent, "Wait for a result.")
	waitNativeBarrier(t, provider.started, "native model request did not start")
	// Management commits revocation independently of the remote supervisor.
	// Its persisted cancellation must stop the already-open upstream stream.
	agent.Enabled = false
	if _, err := h.store.SaveAgent(h.actor, agent); err != nil {
		t.Fatal(err)
	}
	finished := h.finished(t, run.ID)
	if finished.Status != "cancelled" || finished.Published {
		t.Fatal("revoked model did not complete confirmed cleanup", finished)
	}
	waitNativeBarrier(t, provider.ended, "revoking Agent authority left the model request open")
	if provider.calls.Load() != 1 {
		t.Fatalf("revoked model was retried %d times", provider.calls.Load())
	}
	attempt, err := h.store.GetAttempt(h.ctx, finished.AttemptID)
	if err != nil || !attempt.CancelRequested || attempt.ModelInFlight {
		t.Fatal("revoked attempt retained model authority", attempt, err)
	}
}

func TestRemoteNativeServerRestartInterruptsModelStream(t *testing.T) {
	native := nativeRecoveryConfig(t)
	provider := holdNativeModel(t)
	h := newRemoteHarness(t, runs.Config{APIKey: "local-provider-test-key", BaseURL: provider.server.URL, Timeout: 90 * time.Second})
	runner := h.start(t, h.join(t, "native-restart", "native", nil, native))
	room, agent := h.room(t, "Server restart", runner.config.RunnerID)
	run := h.send(t, room, agent, "Wait for a result.")
	waitNativeBarrier(t, provider.started, "native model request did not start")
	old := h.manager.Swap(nil)
	shutdown, stop := context.WithTimeout(h.ctx, 10*time.Second)
	if err := old.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	stop()
	newManager, err := New(h.ctx, h.store, h.config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Store(newManager)
	finished := h.finished(t, run.ID)
	if finished.Status != "interrupted" || finished.Published {
		t.Fatalf("lost model stream falsely completed: %+v", finished)
	}
	waitNativeBarrier(t, provider.ended, "server restart did not close its old upstream stream")
	remoteEventually(t, 15*time.Second, "runner did not rejoin after model stream interruption", func() bool { return newManager.IsOnline(runner.config.RunnerID) })
	if provider.calls.Load() != 1 {
		t.Fatalf("server restart replayed model work: %d", provider.calls.Load())
	}
	runs, err := h.store.ListRoomRuns(h.ctx, room.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal(runs, err)
	}
}

func TestRemoteNativeDaemonSIGKILLRecovery(t *testing.T) {
	native := nativeRecoveryConfig(t)
	provider := holdNativeModel(t)
	h := newRemoteHarness(t, runs.Config{APIKey: "local-provider-test-key", BaseURL: provider.server.URL, Timeout: 90 * time.Second})
	config := h.join(t, "native-killed", "native", nil, native)
	nativeJSON, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRemoteDaemonProcessHelper$", "-test.timeout=3m")
	command.Env = append(os.Environ(), "CAMP_REMOTE_DAEMON_HELPER="+config.DataDir, "CAMP_REMOTE_NATIVE_CONFIG="+string(nativeJSON))
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- command.Wait() }()
	var reaped atomic.Bool
	t.Cleanup(func() {
		if !reaped.Load() {
			_ = command.Process.Kill()
			select {
			case <-processDone:
			case <-time.After(15 * time.Second):
				t.Error("killed daemon was not reaped")
			}
		}
	})
	remoteEventually(t, 90*time.Second, "native daemon subprocess did not connect", func() bool {
		select {
		case err := <-processDone:
			reaped.Store(true)
			t.Fatalf("daemon helper exited: %v", err)
		default:
		}
		return h.manager.Load().IsOnline(config.RunnerID)
	})
	room, agent := h.room(t, "Killed daemon", config.RunnerID)
	run := h.send(t, room, agent, "Wait for a result.")
	waitNativeBarrier(t, provider.started, "native model request did not reach the kill barrier")
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-processDone:
		reaped.Store(true)
		if err == nil {
			t.Fatal("SIGKILL did not terminate the daemon")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("SIGKILL did not reap the daemon")
	}
	// The restarted native runner must reconcile the previous LayerFS/Docker
	// ownership before advertising execution readiness or replaying any delivery.
	restarted := h.start(t, config)
	finished := h.finished(t, run.ID)
	if finished.Status != "interrupted" || finished.Published {
		t.Fatalf("killed execution outcome: %+v", finished)
	}
	waitNativeBarrier(t, provider.ended, "killed daemon left its model connection alive")
	if provider.calls.Load() != 1 {
		t.Fatalf("daemon restart replayed a prompt: %d", provider.calls.Load())
	}
	registered, err := h.store.GetRunner(h.ctx, restarted.config.RunnerID)
	if err != nil || !registered.Enabled {
		t.Fatal(registered, err)
	}
	var capabilities map[string]any
	if err = json.Unmarshal(registered.Capabilities, &capabilities); err != nil || capabilities["ready"] != true {
		t.Fatal("native recovery did not confirm cleanup", capabilities, err)
	}
	entries, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(entries), "\n") {
		if strings.Contains(line, config.DataDir) && strings.Contains(line, finished.AttemptID) {
			t.Fatal("killed attempt retained a mount after native recovery")
		}
	}
}

func TestRemoteDaemonProcessHelper(t *testing.T) {
	directory := os.Getenv("CAMP_REMOTE_DAEMON_HELPER")
	if directory == "" {
		t.Skip("subprocess helper")
	}
	config, err := daemon.LoadConfig(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(os.Getenv("CAMP_REMOTE_NATIVE_CONFIG")), &config.Native); err != nil {
		t.Fatal(err)
	}
	if err = daemon.Run(context.Background(), config, io.Discard); err != nil {
		t.Fatal(err)
	}
}
