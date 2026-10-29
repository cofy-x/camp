package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func nativeConfig(t *testing.T) Config {
	t.Helper()
	runtimeDir := os.Getenv("CAMP_TEST_DSH_RUNTIME")
	if runtimeDir == "" {
		t.Skip("set CAMP_TEST_DSH_RUNTIME to the installed tools/dsh-runtime directory")
	}
	if runtime.GOOS != "linux" {
		t.Skip("native isolation requires Linux Bubblewrap")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	layerRuntime := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME")
	helper := os.Getenv("CAMP_TEST_SANDBOX_HELPER")
	if layerRuntime == "" || helper == "" {
		t.Skip("set CAMP_TEST_LAYERFS_RUNTIME and CAMP_TEST_SANDBOX_HELPER for native LayerFS integration")
	}
	return Config{LayerFSRuntimeDir: layerRuntime, SandboxHelper: helper, DockerSocket: os.Getenv("CAMP_DOCKER_SOCKET"), RuntimeDir: runtimeDir, NodePath: node, APIKey: "camp-integration-keyless", Model: "deepseek-v4-pro", MaxTokens: 1024, MaxSteps: 4, Timeout: 30 * time.Second}
}

func TestDSHIntegration(t *testing.T) {
	base := nativeConfig(t)
	t.Run("write-check-publish-and-resume", func(t *testing.T) {
		var calls atomic.Int32
		var lastRequest string
		var requestMu sync.Mutex
		m, s, dir, room := integrationManager(t, base, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			requestMu.Lock()
			lastRequest = string(body)
			requestMu.Unlock()
			if calls.Add(1) == 1 {
				integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "write", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": `test -z "$DEEPSEEK_API_KEY" || exit 90
if touch /home/outside-project 2>/dev/null; then exit 91; fi
test ! -e /data/camp-workspace/camp/.env || exit 92
printf 'export const greet = (name = "friend") => "Hello, " + name + "!";\n' > greet.mjs
printf 'import {test} from "node:test"; import assert from "node:assert/strict"; import {greet} from "./greet.mjs"; test("default",()=>assert.equal(greet(),"Hello, friend!"));\n' > greet.test.mjs
node --test`}}, "tool_use")
				return
			}
			integrationMessage(t, w, map[string]any{"type": "text", "text": "Recorded durable-native-memory-token and verified greet."}, "end_turn")
		})
		resource, err := s.GetResource(context.Background(), room.ResourceID)
		if err != nil {
			t.Fatal(err)
		}
		resource.Environment, _ = json.Marshal(projectenv.Profile{Kind: "command", Checks: []projectenv.Check{{Name: "test", Arguments: []string{"/node", "--test"}}}})
		if _, err = s.UpdateResource(context.Background(), resource); err != nil {
			t.Fatal(err)
		}
		first := waitRun(t, m, sendTo(t, m, room, "Implement greet and test it").Runs[0].ID)
		if first.Status != "succeeded" || !first.Published || len(first.Files) != 2 || first.TestExitCode == nil || *first.TestExitCode != 0 {
			t.Fatalf("first run: %+v", first)
		}
		readiness, err := m.CheckReadiness(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		for _, component := range readiness.Components {
			if (component.ID == "connection" || component.ID == "execution") && (component.Status != "ready" || component.LastSuccessAt == "") {
				t.Fatalf("completed native execution was not observed: %+v", component)
			}
		}
		second := waitRun(t, m, sendTo(t, m, room, "Recall the recorded token").Runs[0].ID)
		if second.Status != "succeeded" || second.SessionMode != "resume" || second.SessionID != first.SessionID {
			t.Fatalf("native resume: first=%+v second=%+v", first, second)
		}
		requestMu.Lock()
		body := lastRequest
		requestMu.Unlock()
		if !strings.Contains(body, "durable-native-memory-token") {
			t.Fatal("native persisted context did not reach the next model request")
		}
		logs, err := filepath.Glob(filepath.Join(dir, "sessions", "*", "1", "sessions", "*"))
		if err != nil || len(logs) == 0 {
			t.Fatalf("missing native session log: %v %v", logs, err)
		}
		if err = m.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{first.ID, second.ID} {
			for _, name := range []string{"project", "result.tar", "result.json", "runtime.patch.yml"} {
				if _, err := os.Stat(filepath.Join(m.dir, id, name)); !os.IsNotExist(err) {
					t.Fatalf("native terminal scratch survived: %s %s %v", id, name, err)
				}
			}
		}
		again, err := New(context.Background(), s, dir, m.config, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer again.Shutdown(context.Background())
		readiness, err = again.CheckReadiness(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		for _, component := range readiness.Components {
			if component.ID == "connection" && component.Status != "ready" {
				t.Fatal("restart lost provider connection observation")
			}
		}
		third := waitRun(t, again, sendTo(t, again, room, "Continue after a service restart").Runs[0].ID)
		if third.Status != "succeeded" || third.SessionMode != "resume" || third.SessionID != first.SessionID {
			t.Fatalf("restart resume: %+v", third)
		}
	})

	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage+"-native-session-never-replays-input", func(t *testing.T) {
			var calls atomic.Int32
			m, _, dir, room := integrationManager(t, base, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				integrationMessage(t, w, map[string]any{"type": "text", "text": "A completed response."}, "end_turn")
			})
			first := waitRun(t, m, sendTo(t, m, room, "First explicit request").Runs[0].ID)
			if first.Status != "succeeded" {
				t.Fatalf("first response: %+v", first)
			}
			paths, err := filepath.Glob(filepath.Join(dir, "sessions", "*", "1", "sessions"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("native state missing: %v %v", paths, err)
			}
			if damage == "missing" {
				err = os.RemoveAll(paths[0])
			} else {
				err = filepath.WalkDir(paths[0], func(name string, entry os.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if entry.IsDir() {
						return nil
					}
					return os.WriteFile(name, []byte("deliberately corrupted native session\n"), 0600)
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			failed := waitRun(t, m, sendTo(t, m, room, "A request whose resume fails").Runs[0].ID)
			if failed.Status != "failed" || calls.Load() != 1 {
				t.Fatalf("failed resume submitted model input: %+v calls=%d", failed, calls.Load())
			}
			rebuilt := waitRun(t, m, sendTo(t, m, room, "A new explicit request after the failure").Runs[0].ID)
			if rebuilt.Status != "succeeded" || rebuilt.SessionMode != "new" || rebuilt.SessionGeneration <= first.SessionGeneration || calls.Load() != 2 {
				t.Fatalf("session was not rebuilt safely: %+v calls=%d", rebuilt, calls.Load())
			}
		})
	}

	t.Run("private-state-attachments-survive-and-stay-isolated", func(t *testing.T) {
		// The pinned DeepSeek route has no image input capability. This fixture
		// verifies the persistent attachment mount, not image understanding.
		var calls atomic.Int32
		var manager atomic.Pointer[Manager]
		var fixture string
		var fixtureMu sync.Mutex
		m, s, _, room := integrationManager(t, base, func(w http.ResponseWriter, r *http.Request) {
			call := calls.Add(1)
			if call == 1 {
				states, err := filepath.Glob(filepath.Join(filepath.Dir(manager.Load().dir), "sessions", "*", "1"))
				if err != nil || len(states) != 1 {
					t.Errorf("private state missing: %v %v", states, err)
					http.Error(w, "state missing", 500)
					return
				}
				fixtureMu.Lock()
				fixture = filepath.Join(states[0], "attachments", "camp-sentinel.txt")
				fixtureMu.Unlock()
				if err := os.MkdirAll(filepath.Dir(fixture), 0700); err != nil {
					t.Error(err)
					return
				}
				if err := os.WriteFile(fixture, []byte("private-attachment-sentinel"), 0600); err != nil {
					t.Error(err)
					return
				}
			}
			if call%2 == 1 {
				command := "if test -e /state/attachments/camp-sentinel.txt; then echo leaked-private-state; else echo isolated-private-state; fi"
				if call == 3 {
					command = "if test -e /state/attachments/camp-sentinel.txt; then echo leaked-private-state; else echo isolated-private-state; fi"
				}
				integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "attachment-mount", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": command}}, "tool_use")
				return
			}
			integrationMessage(t, w, map[string]any{"type": "text", "text": "Private state inspected."}, "end_turn")
		})
		manager.Store(m)
		first := waitRun(t, m, sendTo(t, m, room, "Read the private fixture").Runs[0].ID)
		if first.Status != "succeeded" || !strings.Contains(first.Output, "isolated-private-state") {
			t.Fatalf("private mount inaccessible: %+v", first)
		}
		other := newAgent(t, s, room, "isolated", "write")
		second := waitRun(t, m, sendTo(t, m, room, "Inspect your own state", other.ID).Runs[0].ID)
		if second.Status != "succeeded" || !strings.Contains(second.Output, "isolated-private-state") {
			t.Fatalf("agent state leaked: %+v", second)
		}
		third := waitRun(t, m, sendTo(t, m, room, "Read the private fixture again").Runs[0].ID)
		if third.Status != "succeeded" || third.SessionMode != "resume" || !strings.Contains(third.Output, "isolated-private-state") {
			t.Fatalf("private state did not persist: %+v", third)
		}
		fixtureMu.Lock()
		defer fixtureMu.Unlock()
		if data, err := os.ReadFile(fixture); err != nil || string(data) != "private-attachment-sentinel" {
			t.Fatalf("attachment lost: %q %v", data, err)
		}
	})

	t.Run("inherited-ownership-outlives-supervisor-descriptor", func(t *testing.T) {
		tag := fmt.Sprintf("camp-rooms-lock-%d", time.Now().UnixNano())
		m, _, dir, room := integrationManager(t, base, func(w http.ResponseWriter, r *http.Request) {
			integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "lock", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": fmt.Sprintf("setsid node -e 'setInterval(() => {}, 1000)' %s &\nsleep 120", tag)}}, "tool_use")
		})
		run := sendTo(t, m, room, "Hold the native process").Runs[0]
		awaitDescendant(t, tag)
		if err := m.lock.Close(); err != nil {
			t.Fatal(err)
		}
		if contender, err := AcquireOwnership(dir); err == nil {
			_ = contender.Close()
			t.Fatal("live native namespace lost inherited ownership")
		}
		if _, err := m.Cancel(context.Background(), run.ID); err != nil {
			t.Fatal(err)
		}
		if result := waitRun(t, m, run.ID); result.Status != "cancelled" {
			t.Fatalf("cancellation: %+v", result)
		}
		next, err := AcquireOwnership(dir)
		if err != nil {
			t.Fatalf("namespace retained ownership after exit: %v", err)
		}
		_ = next.Close()
	})

	t.Run("provider-failure-retains-partial-without-publication", func(t *testing.T) {
		var calls atomic.Int32
		m, s, _, room := integrationManager(t, base, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "partial", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": "echo partial-before-error > partial.txt; cat partial.txt"}}, "tool_use")
				return
			}
			http.Error(w, `{"error":{"type":"api_error","message":"forced model failure"}}`, 503)
		})
		before, _ := s.GetResource(context.Background(), room.ResourceID)
		result := waitRun(t, m, sendTo(t, m, room, "Create a partial result").Runs[0].ID)
		after, _ := s.GetResource(context.Background(), room.ResourceID)
		if result.Status != "failed" || result.Error == "" || len(result.Files) != 1 || !strings.Contains(result.Output, "partial-before-error") || result.Published || before.CurrentCommit != after.CurrentCommit || calls.Load() != 2 {
			t.Fatalf("lost failure evidence: %+v", result)
		}
	})

	for _, mode := range []string{"cancel", "timeout", "shutdown"} {
		t.Run(mode+"-cleans-descendants", func(t *testing.T) {
			config := base
			if mode == "timeout" {
				config.Timeout = 4 * time.Second
			}
			tag := fmt.Sprintf("camp-rooms-%s-%d", mode, time.Now().UnixNano())
			m, s, _, room := integrationManager(t, config, func(w http.ResponseWriter, r *http.Request) {
				integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "long", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": fmt.Sprintf("setsid node -e 'setInterval(() => {}, 1000)' %s &\necho retained-before-stop > partial.txt\nsleep 120", tag)}}, "tool_use")
			})
			room, revision := referenceResource(t, m, s, room)
			sent, err := m.Send(context.Background(), room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "stop-with-reference", Content: "Work until stopped", References: []storage.ReferenceInput{{Kind: "file", ResourceID: room.ResourceID, Commit: revision, Path: "brief.md"}}})
			if err != nil {
				t.Fatal(err)
			}
			run := sent.Runs[0]
			awaitDescendant(t, tag)
			switch mode {
			case "cancel":
				if _, err := m.Cancel(context.Background(), run.ID); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				integrationStop(t, m)
			}
			result := waitRun(t, m, run.ID)
			want := map[string]string{"cancel": "cancelled", "timeout": "failed", "shutdown": "interrupted"}[mode]
			if result.Status != want || len(result.Files) != 1 || result.Published {
				t.Fatalf("stop result: %+v", result)
			}
			if pids := integrationDescendants(t, tag); len(pids) > 0 {
				t.Fatalf("surviving descendants: %v", pids)
			}
			integrationStop(t, m)
			scratch, err := filepath.Glob(filepath.Join(m.dir, run.ID, ".references-*"))
			if err != nil || len(scratch) != 0 {
				t.Fatalf("reference scratch remained after %s: %v %v", mode, scratch, err)
			}
		})
	}

	t.Run("cancel-one-agent-while-another-finishes-and-rebuild-session", func(t *testing.T) {
		var calls atomic.Int32
		tag := fmt.Sprintf("camp-rooms-parallel-%d", time.Now().UnixNano())
		m, s, _, room := integrationManager(t, base, func(w http.ResponseWriter, r *http.Request) {
			call := calls.Add(1)
			if call == 1 {
				integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "long", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": fmt.Sprintf("setsid node -e 'setInterval(() => {}, 1000)' %s &\nsleep 120", tag)}}, "tool_use")
				return
			}
			if call == 2 {
				var body struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for _, tool := range body.Tools {
					if tool.Name == "bash" || tool.Name == "write" || tool.Name == "edit" {
						t.Errorf("read-only member received %s", tool.Name)
					}
				}
			}

			integrationMessage(t, w, map[string]any{"type": "text", "text": "Independent agent response."}, "end_turn")
		})
		first := sendTo(t, m, room, "Long task").Runs[0]
		awaitDescendant(t, tag)
		reader := newAgent(t, s, room, "reviewer", "read")
		second := sendTo(t, m, room, "Read without writing", reader.ID).Runs[0]
		result := waitRun(t, m, second.ID)
		if result.Status != "succeeded" || !strings.Contains(result.Output, "Independent agent response.") {
			t.Fatalf("read-only agent failed: %+v", result)
		}
		if _, err := os.Stat(filepath.Join(m.dir, result.ID, "project", "forbidden.txt")); !os.IsNotExist(err) {
			t.Fatalf("read-only agent changed files: %v", err)
		}
		still, _ := m.Get(context.Background(), first.ID)
		if still.Status != "running" {
			t.Fatalf("agents were not parallel: %+v", still)
		}
		if _, err := m.Cancel(context.Background(), first.ID); err != nil {
			t.Fatal(err)
		}
		cancelled := waitRun(t, m, first.ID)
		third := waitRun(t, m, sendTo(t, m, room, "Continue only this new request").Runs[0].ID)
		if third.Status != "succeeded" || third.SessionMode != "new" || third.SessionID == cancelled.SessionID || third.SessionGeneration <= cancelled.SessionGeneration || !strings.Contains(third.Output, "Rebuilding conversation") {
			t.Fatalf("cancelled inbox was resumed: %+v", third)
		}
	})

	t.Run("service-crash-discards-unconfirmed-store-and-cleans-on-restart", func(t *testing.T) {
		tag := fmt.Sprintf("camp-rooms-crash-%d", time.Now().UnixNano())
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "crash", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": fmt.Sprintf("setsid node -e 'setInterval(() => {}, 1000)' %s &\necho crash-result > partial.txt\nsleep 120", tag)}}, "tool_use")
		}))
		defer endpoint.Close()
		dir := t.TempDir()
		child := exec.Command(os.Args[0], "-test.run=^TestDSHIntegrationCrashHelper$")
		child.Env = append(os.Environ(), "CAMP_ROOMS_CRASH_HELPER=1", "CAMP_ROOMS_TEST_DATA="+dir, "CAMP_ROOMS_TEST_URL="+endpoint.URL, "CAMP_ROOMS_TEST_NODE="+base.NodePath)
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
		awaitDescendant(t, tag)
		if err := child.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = child.Wait()
		store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		base.BaseURL = endpoint.URL
		m, err := New(context.Background(), store, dir, base, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer integrationStop(t, m)
		deadline := time.Now().Add(8 * time.Second)
		for len(integrationDescendants(t, tag)) > 0 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if pids := integrationDescendants(t, tag); len(pids) > 0 {
			t.Fatalf("crash retained descendants: %v", pids)
		}
		rooms, err := store.ListRooms(context.Background())
		if err != nil || len(rooms) != 1 {
			t.Fatal(err)
		}
		runs, err := store.ListRoomRuns(context.Background(), rooms[0].ID)
		if err != nil || len(runs) != 1 || runs[0].Status != "interrupted" || len(runs[0].Files) != 0 || runs[0].Published {
			t.Fatalf("crash recovery: %+v %v", runs, err)
		}
	})
}

func TestDSHIntegrationPreparedCachePrunesOnlyIdleProjects(t *testing.T) {
	config := nativeConfig(t)
	m, _, dir, _ := integrationManager(t, config, func(w http.ResponseWriter, r *http.Request) {
		t.Error("cache verification must not call a model")
		http.Error(w, "unexpected", 500)
	})
	ctx := context.Background()
	source := filepath.Join(dir, "cache-source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "note.txt"), []byte("baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		workspaces, err := m.bridge.Workspaces(cleanup)
		if err != nil {
			t.Error(err)
		}
		for _, workspace := range workspaces {
			if err := m.bridge.CloseWorkspace(cleanup, workspace.WorkspaceID, true); err != nil {
				t.Error(err)
			}
		}
		for _, usage := range m.layerProjects {
			usage.active = 0
		}
		if err := m.Shutdown(cleanup); err != nil {
			t.Error(err)
		}
	}()
	acquire := func(key string) layerfs.PreparedProject {
		t.Helper()
		prepared, err := m.acquirePreparedProject(ctx, layerfs.PrepareRequest{SourceDir: source, BaselineKey: key, EnvironmentKey: "files"})
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	a := acquire("active")
	reused := acquire("active")
	if !reused.Reused || a.ProjectID != reused.ProjectID {
		t.Fatal("active baseline was not reused")
	}
	workspaces := make([]*execution, 2)
	for i := range workspaces {
		workspace, err := m.bridge.Create(ctx, layerfs.CreateRequest{ProjectID: a.ProjectID, RunID: fmt.Sprintf("cache-active-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		workspaces[i] = &execution{workspace: workspace, layerProject: a.ProjectID}
	}
	firstIdle := ""
	for _, key := range []string{"idle-one", "idle-two", "idle-three"} {
		prepared := acquire(key)
		if firstIdle == "" {
			firstIdle = prepared.ProjectID
		}
		if err := m.releasePreparedProject(ctx, prepared.ProjectID); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.layerProjects) != 3 || m.layerProjects[a.ProjectID].active != 2 || m.layerProjects[firstIdle] != nil {
		t.Fatal("native idle eviction touched active work or retained excess stores")
	}
	removed := acquire("idle-one")
	if removed.Reused {
		t.Fatal("eviction did not remove the disposable Store")
	}
	if err := m.releasePreparedProject(ctx, removed.ProjectID); err != nil {
		t.Fatal(err)
	}
	if err := m.closeLayerFS(workspaces[0]); err != nil {
		t.Fatal(err)
	}
	if m.layerProjects[a.ProjectID] == nil || m.layerProjects[a.ProjectID].active != 1 {
		t.Fatal("first close evicted another active workspace")
	}
	if err := m.closeLayerFS(workspaces[1]); err != nil {
		t.Fatal(err)
	}
	if len(m.layerProjects) != idleLayerProjects {
		t.Fatal("closed workspaces exceeded idle cache bound")
	}
	remaining, err := m.bridge.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, workspace := range remaining {
		if workspace.Status != "closed" {
			t.Fatal("native workspace was not closed")
		}
	}
}

func integrationManager(t *testing.T, config Config, handler http.HandlerFunc) (*Manager, *storage.Store, string, storage.Room) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "camp-integration-keyless" {
			t.Error("unexpected model credential")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	config.BaseURL = server.URL
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m, err := New(context.Background(), store, dir, config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { integrationStop(t, m) })
	if available, reason, _ := m.Availability(); !available {
		t.Fatalf("native runtime unavailable: %s", reason)
	}
	id := strings.Repeat("a", 32)
	created, err := m.resources.Create(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	room, err := store.SeedRoom(context.Background(), storage.Resource{ID: id, Name: "Room files", CurrentCommit: created.Commit, Environment: json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	return m, store, dir, room
}
func integrationStop(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Error(err)
	}
}
func integrationMessage(t *testing.T, w http.ResponseWriter, content map[string]any, stop string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	events := []map[string]any{{"type": "message_start", "message": map[string]any{"id": "camp-integration", "model": "deepseek-v4-pro", "usage": map[string]int{"input_tokens": 3, "output_tokens": 0}}}, {"type": "content_block_start", "index": 0, "content_block": content}, {"type": "content_block_stop", "index": 0}, {"type": "message_delta", "delta": map[string]string{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 1}}, {"type": "message_stop"}}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data); err != nil {
			return
		}
	}
}
func integrationDescendants(t *testing.T, tag string) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err == nil && strings.Contains(string(cmdline), tag) {
			pids = append(pids, pid)
		}
	}
	return pids
}
func awaitDescendant(t *testing.T, tag string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if len(integrationDescendants(t, tag)) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("native detached command did not start")
}
func TestDSHIntegrationCrashHelper(t *testing.T) {
	if os.Getenv("CAMP_ROOMS_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	config := nativeConfig(t)
	config.BaseURL = os.Getenv("CAMP_ROOMS_TEST_URL")
	config.NodePath = os.Getenv("CAMP_ROOMS_TEST_NODE")
	dir := os.Getenv("CAMP_ROOMS_TEST_DATA")
	s, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := New(context.Background(), s, dir, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	res, err := m.resources.Create(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	room, err := s.SeedRoom(context.Background(), storage.Resource{ID: id, Name: "Files", CurrentCommit: res.Commit, Environment: json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	sendTo(t, m, room, "Work before supervisor crash")
	time.Sleep(2 * time.Minute)
	t.Fatal("parent did not kill the crash helper")
}
