package remoteruns

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/cofy-x/camp/apps/server/internal/configuration"
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
			if !strings.Contains(string(body), `\"origin\":\"upload\"`) || !strings.Contains(string(body), "/references/1/brief.md") {
				t.Error("the first model request lost the uploaded file provenance")
			}
			content = map[string]any{"type": "tool_use", "id": "isolated-write", "name": "bash", "input": map[string]string{
				"description": "Check the frozen upload and prepared dependency, then write the result",
				"command": `test -z "$DEEPSEEK_API_KEY" && test ! -e /camp-ca.pem &&
test "$(cat /references/1/brief.md)" = "Preserve this exact original." &&
test ! -e /workspace/brief.md &&
if test -w /references/1/brief.md; then exit 43; fi &&
test "$(pnpm --version)" = "12.11.2" && pnpm run test &&
printf 'native remote delivery
' > result.txt`,
			}}
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
	h := newRemoteHarness(t, configuration.ModelInput{APIKey: privateKey, BaseURL: provider.URL, Model: "deepseek-v4-pro", MaxTokens: 4096, MaxSteps: 8, Timeout: 90 * time.Second})
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
	source := nativeDependencyProject(t)
	manager := h.manager.Load()
	profile, err := projectenv.Detect(source)
	if err != nil {
		t.Fatal(err)
	}
	resourceID := newID()
	imported, err := manager.files.ImportWithPolicy(h.ctx, resourceID, source, projectenv.SnapshotPolicy(profile))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.CreateResource(h.actor, storage.Resource{ID: resourceID, Name: "Prepared project", CurrentCommit: imported.Commit, Environment: environment}); err != nil {
		t.Fatal(err)
	}
	room.ResourceID = resourceID
	room, err = h.store.UpdateRoom(h.actor, room)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("Preserve this exact original.\n")
	uploadID := newID()
	object, err := manager.files.Upload(h.ctx, uploadID, "brief.md", original)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(original)
	_, err = h.store.RegisterRoomUpload(h.actor, room.ID, "native-brief", uploadID, object.Commit, "brief.md", len(original), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	input := storage.SendMessageInput{
		RequestID: "native-write", RecipientAgentIDs: []string{agent.ID},
		Content:    "Write a result using the uploaded original and prepared project.",
		References: []storage.ReferenceInput{{Kind: "file", ResourceID: uploadID, Commit: object.Commit, Path: "brief.md"}},
	}
	sent, err := manager.Send(h.actor, room.ID, input)
	if err != nil || len(sent.Runs) != 1 {
		t.Fatal(sent, err)
	}
	finish := func(id string) storage.Run {
		var result storage.Run
		remoteEventually(t, 100*time.Second, "native remote execution did not finish", func() bool { result, _ = h.store.GetRun(h.ctx, id); return terminal(result.Status) })
		return result
	}
	first := finish(sent.Runs[0].ID)
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
	retained, err := manager.files.ReadFile(h.ctx, uploadID, object.Commit, "brief.md")
	if err != nil || string(retained) != string(original) {
		t.Fatal("the uploaded original changed", err)
	}
	if _, err = os.Stat(filepath.Join(source, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("dependency preparation changed the original directory", err)
	}
	replay, err := manager.Send(h.actor, room.ID, input)
	if err != nil || !replay.Replayed || replay.Message.ID != sent.Message.ID || calls.Load() != 2 {
		t.Fatal("request replay changed the original execution", err)
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

func nativeDependencyProject(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	lock, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tools", "project-runtime", "pnpm-lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	managerLock := strings.Split(string(lock), "\n---\n")[0] + "\n---\n"
	files := map[string]string{
		"package.json":                 `{"name":"fixture","type":"module","packageManager":"pnpm@12.11.2","dependencies":{"inside":"workspace:*"},"scripts":{"postinstall":"node -e \"require('fs').writeFileSync('should-not-exist','bad')\"","test":"node --test"}}`,
		"pnpm-workspace.yaml":          "packages:\n  - packages/*\n",
		"pnpm-lock.yaml":               managerLock + "lockfileVersion: '9.0'\nsettings:\n  autoInstallPeers: true\n  excludeLinksFromLockfile: false\nimporters:\n  .:\n    dependencies:\n      inside:\n        specifier: workspace:*\n        version: link:packages/inside\n  packages/inside: {}\n",
		"packages/inside/package.json": `{"name":"inside","version":"1.0.0","type":"module","exports":"./index.js"}`,
		"packages/inside/index.js":     "export const value = 42;\n",
		"source.test.mjs":              "import assert from 'node:assert/strict'; import fs from 'node:fs'; import {value} from 'inside'; assert.equal(value,42); assert.equal(fs.existsSync('should-not-exist'),false); assert.equal(process.env.DEEPSEEK_API_KEY,undefined); assert.equal(process.version,'v24.16.0');\n",
	}
	for name, content := range files {
		target := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return source
}
