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

func TestRemoteNativeCollaborationAcrossRunners(t *testing.T) {
	layerRuntime, dshRuntime := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME"), os.Getenv("CAMP_TEST_DSH_RUNTIME")
	if layerRuntime == "" || dshRuntime == "" {
		t.Skip("Set native runtime paths for the Linux remote collaboration acceptance")
	}
	var calls atomic.Int32
	var participants atomic.Value
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("X-Api-Key") != "server-only-native-test-key" {
			t.Error("The Server proxy did not use its private model credential")
		}
		ids := participants.Load().(map[string]string)
		number := calls.Add(1)
		name, input := "", map[string]any{}
		switch number {
		case 1:
			name, input = "bash", map[string]any{"description": "Draft the guide", "command": "test -z \"$DEEPSEEK_API_KEY\" && printf 'Draft from native writer\\n' > guide.txt && cat guide.txt"}
		case 2:
			if !strings.Contains(string(body), "Draft from native writer") {
				t.Error("The writer's actual tool result did not reach the model")
			}
			name, input = "camp_handoff", map[string]any{"targetAgentId": ids["reviewer"], "instruction": "Read guide.txt, review the native draft, then return it to the writer.", "artifactPaths": []string{"guide.txt"}}
		case 3:
			var request struct {
				Tools []struct{ Name string } `json:"tools"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
			}
			for _, tool := range request.Tools {
				if tool.Name == "bash" || tool.Name == "write" || tool.Name == "edit" {
					t.Error("Read-only reviewer received a mutation tool", tool.Name)
				}
			}
			name, input = "read", map[string]any{"file_path": "guide.txt"}
		case 4:
			if !strings.Contains(string(body), "Draft from native writer") {
				t.Error("The second Runner could not read the first Runner's checkpoint")
			}
			name, input = "camp_handoff", map[string]any{"targetAgentId": ids["writer"], "instruction": "Reviewed native draft. Replace the draft text with Reviewed native result and finish.", "artifactPaths": []string{"guide.txt"}}
		case 5:
			if !strings.Contains(string(body), "Reviewed native draft") {
				t.Error("Reviewer findings did not reach the returning writer")
			}
			name, input = "bash", map[string]any{"description": "Apply the review", "command": "test \"$(cat guide.txt)\" = 'Draft from native writer' && printf 'Reviewed native result\\n' > guide.txt && cat guide.txt"}
		case 6:
			name, input = "camp_finish", map[string]any{"summary": "The native writer applied the independent read-only review.", "artifactPaths": []string{"guide.txt"}}
		default:
			t.Error("A concluded native round issued an extra model request", number)
			w.WriteHeader(500)
			return
		}
		block := map[string]any{"type": "tool_use", "id": fmt.Sprintf("native-step-%d", number), "name": name, "input": input}
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("native-model-%d", number), "model": "deepseek-v4-pro", "usage": map[string]int{"input_tokens": 3, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": block},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]string{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 1}},
			{"type": "message_stop"},
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		}
	}))
	defer provider.Close()
	h := newRemoteHarness(t, runs.Config{BaseURL: provider.URL, APIKey: "server-only-native-test-key", Model: "deepseek-v4-pro", MaxSteps: 6, Timeout: time.Minute})
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "camp-sandbox")
	build := exec.CommandContext(h.ctx, "go", "build", "-o", helper, "../../cmd/camp-sandbox")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Build sandbox: %v %s", err, output)
	}
	native := nativeexecutor.Config{RuntimeDir: dshRuntime, LayerFSRuntimeDir: layerRuntime, SandboxHelper: helper, NodePath: node}
	a := h.start(t, h.join(t, "native-writer", "native", nil, native))
	b := h.start(t, h.join(t, "native-reviewer", "native", nil, native))
	if calls.Load() != 0 {
		t.Fatal("Runner readiness made a model request")
	}
	room, writer := h.room(t, "Native collaboration", a.config.RunnerID)
	reviewer, err := h.store.CreateRoomAgent(h.actor, room.ID, storage.Agent{Name: "Read-only native reviewer", Handle: "native-reviewer", Access: "read", Enabled: true, PreferredRunnerID: b.config.RunnerID})
	if err != nil {
		t.Fatal(err)
	}
	participants.Store(map[string]string{"writer": writer.ID, "reviewer": reviewer.ID})
	resource, err := h.store.GetResource(h.ctx, room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	resource.Environment, _ = json.Marshal(projectenv.Profile{Kind: "command", Checks: []projectenv.Check{{Name: "content", Arguments: []string{"bash", "-c", "test \"$(cat guide.txt)\" = 'Reviewed native result'"}}}})
	if _, err = h.store.UpdateResource(h.actor, resource); err != nil {
		t.Fatal(err)
	}
	h.lostResponse.Store(true)
	task, err := h.manager.Load().CreateTask(h.actor, room.ID, storage.CreateTaskInput{RequestID: "native-collaboration", Goal: "Draft a guide, have the approved reviewer inspect it on the second Runner, apply the review, and finish.", ParticipantAgentIDs: []string{writer.ID, reviewer.ID}, FirstAgentID: writer.ID})
	if err != nil {
		t.Fatal(err)
	}
	remoteEventually(t, 90*time.Second, "Native collaboration did not finish", func() bool {
		task, _ = h.store.GetTask(h.ctx, task.ID)
		if task.Status == "needs_attention" {
			rounds, _ := h.store.ListTaskRuns(h.ctx, task.ID)
			t.Fatalf("Native task needs attention: %+v; rounds: %+v", task, rounds)
		}
		return task.Status == "completed"
	})
	rounds, err := h.store.ListTaskRuns(h.ctx, task.ID)
	if err != nil || len(rounds) != 3 || calls.Load() != 6 || task.ModelRequestsUsed != 6 {
		t.Fatalf("Unexpected native execution count: rounds=%d calls=%d used=%d error=%v", len(rounds), calls.Load(), task.ModelRequestsUsed, err)
	}
	if rounds[0].RunnerID != a.config.RunnerID || rounds[1].RunnerID != b.config.RunnerID || rounds[2].RunnerID != a.config.RunnerID || rounds[1].InputCommit != rounds[0].ResultCommit || rounds[2].InputCommit != rounds[0].ResultCommit {
		t.Fatal("Native draft or frozen routing changed across handoff", rounds)
	}
	for index, round := range rounds {
		h.mu.Lock()
		result := h.results[round.AttemptID]
		h.mu.Unlock()
		if !result.CleanupConfirmed || result.Intent == nil || round.Status != "succeeded" || round.Published != (index == 2) {
			t.Fatalf("Native round did not complete safely: %+v %+v", round, result)
		}
		if index < 2 && (len(result.Checks) != 0 || result.Intent.Kind != "handoff") {
			t.Fatal("Intermediate round checked or finished the draft", result)
		}
		if index == 2 && (result.Intent.Kind != "finish" || len(result.Checks) != 1 || result.Checks[0].Status != "passed") {
			t.Fatal("Final writer omitted configured checks", result)
		}
	}
	file, err := h.manager.Load().files.ReadFile(h.ctx, room.ResourceID, task.PublishedCommit, "guide.txt")
	if err != nil || string(file) != "Reviewed native result\n" {
		t.Fatalf("Native final artifact: %q %v", file, err)
	}
	publications, err := h.store.ListPublications(h.ctx, room.ResourceID, 0)
	if err != nil || len(publications) != 1 {
		t.Fatal("Native collaboration publication was not unique", publications, err)
	}
	// A lost completion response reuses its receipt without re-running the model.
	h.mu.Lock()
	firstResult := h.results[rounds[0].AttemptID]
	h.mu.Unlock()
	data, _ := json.Marshal(firstResult)
	if receipt := h.request(t, a.config, "POST", "/api/daemon/operations/"+rounds[0].AttemptID+"/complete", data, nil); receipt.Disposition != "committed" {
		t.Fatal(receipt)
	}
	if calls.Load() != 6 {
		t.Fatal("Completion replay repeated a native model request")
	}
}
