package remoteruns

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// This is a native dependency check, not a paid-model evaluation. The DSH model
// loop runs against a deterministic local provider through the real proxy.
func TestRemoteNativeIntegration(t *testing.T) {
	layerRuntime, dshRuntime := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME"), os.Getenv("CAMP_TEST_DSH_RUNTIME")
	if layerRuntime == "" || dshRuntime == "" {
		t.Skip("Set CAMP_TEST_LAYERFS_RUNTIME and CAMP_TEST_DSH_RUNTIME for Linux native acceptance")
	}
	const privateKey = "server-only-private-test-key"
	var calls atomic.Int32
	var resumed atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != privateKey && r.Header.Get("Authorization") != "Bearer "+privateKey {
			t.Error("the server proxy did not use its private upstream credential")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		number := calls.Add(1)
		stop := "end_turn"
		content := map[string]any{"type": "text", "text": "Retain remote-native-memory for our next task."}
		if number == 1 {
			stop = "tool_use"
			content = map[string]any{"type": "tool_use", "id": "isolated-write", "name": "bash", "input": map[string]string{"description": "Write the isolated result", "command": "test -z \"$DEEPSEEK_API_KEY\" && test ! -e /camp-ca.pem && printf 'native remote delivery\\n' > result.txt"}}
		}
		if number >= 3 {
			resumed.Store(strings.Contains(string(body), "remote-native-memory"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "native-proxy-test", "model": "deepseek-v4-pro", "usage": map[string]int{"input_tokens": 3, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": content},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]string{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 1}},
			{"type": "message_stop"},
		}
		for _, event := range events {
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		}
		w.(http.Flusher).Flush()
		// Exercise the actual DSH parser's message_stop lifecycle: the provider
		// deliberately does not end its transport after the complete response.
		<-r.Context().Done()
	}))
	defer provider.Close()
	h := newRemoteHarness(t, runs.Config{APIKey: privateKey, BaseURL: provider.URL, Model: "deepseek-v4-pro", MaxTokens: 4096, MaxSteps: 8, Timeout: 90 * time.Second})
	helper := os.Getenv("CAMP_TEST_SANDBOX_HELPER")
	if helper == "" {
		helper = filepath.Join(t.TempDir(), "camp-sandbox")
		command := exec.CommandContext(h.ctx, "go", "build", "-o", helper, "../../cmd/camp-sandbox")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build sandbox: %v %s", err, output)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	config := h.join(t, "native-linux", "native", nil, nativeexecutor.Config{RuntimeDir: dshRuntime, LayerFSRuntimeDir: layerRuntime, DockerSocket: os.Getenv("CAMP_TEST_DOCKER_SOCKET"), SandboxHelper: helper, NodePath: node})
	runner := h.start(t, config)
	room, agent := h.room(t, "Native remote work", runner.config.RunnerID)
	resource, err := h.store.GetResource(h.ctx, room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	resource.Environment, _ = json.Marshal(projectenv.Profile{Kind: "command", Checks: []projectenv.Check{{Name: "content", Arguments: []string{"bash", "-c", "test \"$(cat result.txt)\" = 'native remote delivery'"}}}})
	if _, err = h.store.UpdateResource(h.actor, resource); err != nil {
		t.Fatal(err)
	}
	finish := func(id string) storage.Run {
		var result storage.Run
		remoteEventually(t, 100*time.Second, "native remote execution did not finish", func() bool { result, _ = h.store.GetRun(h.ctx, id); return terminal(result.Status) })
		return result
	}
	first := finish(h.send(t, room, agent, "Write a result in the isolated workspace.").ID)
	if first.Status != "succeeded" || !first.Published || first.RunnerID != config.RunnerID {
		t.Fatalf("native publication: %+v", first)
	}
	var checks []projectenv.CheckResult
	if err = json.Unmarshal(first.Checks, &checks); err != nil || len(checks) != 1 || checks[0].Status != "passed" {
		t.Fatal(checks, err)
	}
	content, err := h.manager.Load().files.ReadFile(h.ctx, first.ResourceID, first.ResultCommit, "result.txt")
	if err != nil || string(content) != "native remote delivery\n" {
		t.Fatal(string(content), err)
	}
	second := finish(h.send(t, room, agent, "Recall the previous native context and confirm the result.").ID)
	if second.Status != "succeeded" || second.SessionMode != "resume" || second.SessionID != first.SessionID || !resumed.Load() {
		t.Fatalf("native remote resume: %+v, context=%v", second, resumed.Load())
	}
	if calls.Load() != 3 {
		t.Fatalf("unexpected/repeated upstream model requests: %d", calls.Load())
	}
	for _, run := range []storage.Run{first, second} {
		attempt, err := h.store.GetAttempt(h.ctx, run.AttemptID)
		if err != nil || attempt.ModelInFlight || attempt.Phase != "finished" {
			t.Fatal(attempt, err)
		}
	}
	if strings.Contains(first.Output, privateKey) || strings.Contains(second.Output, privateKey) {
		t.Fatal("upstream model credential leaked to execution output")
	}
	data, err := os.ReadFile(filepath.Join(config.DataDir, "daemon.json"))
	if err != nil || strings.Contains(string(data), privateKey) {
		t.Fatal("upstream model credential reached Runner configuration", err)
	}
}
