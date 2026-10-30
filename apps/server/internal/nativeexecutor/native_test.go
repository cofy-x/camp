package nativeexecutor

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestNativeExecutorTLSIsolationAndResume(t *testing.T) {
	layerRuntime, dshRuntime := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME"), os.Getenv("CAMP_TEST_DSH_RUNTIME")
	if dshRuntime == "" || runtime.GOOS != "darwin" && layerRuntime == "" {
		t.Skip("Set the native DSH runtime and, on Linux, the LayerFS runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	helper := filepath.Join(data, "camp-sandbox")
	build := exec.CommandContext(ctx, "go", "build", "-o", helper, "../../cmd/camp-sandbox")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sandbox: %v %s", err, output)
	}
	var calls atomic.Int32
	var resumed atomic.Bool
	model := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		number := calls.Add(1)
		if r.Header.Get("x-api-key") != "attempt-only-token" && r.Header.Get("Authorization") != "Bearer attempt-only-token" {
			t.Errorf("missing scoped model token: %s", r.URL)
		}
		content := map[string]any{"type": "text", "text": "Retain native-runner-memory-token for the next request."}
		stop := "end_turn"
		if number == 1 {
			content = map[string]any{"type": "tool_use", "id": "write", "name": "bash", "input": map[string]string{"description": "Write the isolated result", "command": "test -z \"$DEEPSEEK_API_KEY\" && test ! -e /camp-ca.pem && printf 'remote native result\\n' > result.txt"}}
			stop = "tool_use"
		}
		if number >= 3 {
			resumed.Store(strings.Contains(string(body), "native-runner-memory-token"))
		}
		if strings.Contains(string(body), "Finish collaboration with the Camp tool") {
			if !strings.Contains(string(body), "camp_finish") {
				t.Error("Collaboration tool schema is missing")
			}
			content = map[string]any{"type": "tool_use", "id": "finish-call", "name": "camp_finish", "input": map[string]any{"summary": "Native collaboration finished.", "artifactPaths": []string{"result.txt"}}}
			stop = "tool_use"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		contents := []map[string]any{content}
		if strings.Contains(string(body), "Finish collaboration with the Camp tool") {
			contents = append(contents,
				map[string]any{"type": "tool_use", "id": "late-write", "name": "bash", "input": map[string]string{"description": "Attempt a late mutation", "command": "printf late > after-finish.txt"}},
				map[string]any{"type": "tool_use", "id": "second-finish", "name": "camp_finish", "input": map[string]any{"summary": "Different decision", "artifactPaths": []string{}}})
		}
		events := []map[string]any{{"type": "message_start", "message": map[string]any{"id": "remote-native-test", "model": "deepseek-v4-pro", "usage": map[string]int{"input_tokens": 3, "output_tokens": 0}}}}
		for index, block := range contents {
			events = append(events, map[string]any{"type": "content_block_start", "index": index, "content_block": block}, map[string]any{"type": "content_block_stop", "index": index})
		}
		events = append(events, map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 1}}, map[string]any{"type": "message_stop"})
		for _, event := range events {
			encoded, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
		}
	}))
	defer model.Close()
	ca := filepath.Join(data, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: model.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	executor, err := Open(ctx, Config{DataDir: data, RuntimeDir: dshRuntime, LayerFSRuntimeDir: layerRuntime, DockerSocket: os.Getenv("CAMP_TEST_DOCKER_SOCKET"), SandboxHelper: helper, NodePath: node, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := executor.Close(); err != nil {
			t.Error(err)
		}
	}()
	profile, _ := json.Marshal(projectenv.Profile{Kind: "command", Checks: []projectenv.Check{{Name: "content", Arguments: []string{"bash", "-c", "test \"$(cat result.txt)\" = 'remote native result'"}}}})
	run := func(id string, resume bool) Result {
		directory := filepath.Join(data, id)
		if err := os.MkdirAll(filepath.Join(directory, "project"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "project", "brief.txt"), []byte("Keep the source."), 0600); err != nil {
			t.Fatal(err)
		}
		if resume {
			if err := os.WriteFile(filepath.Join(directory, "project", "result.txt"), []byte("remote native result\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		request := Request{AttemptID: id, RunID: id, ResourceID: "resource", BaselineCommit: id, Directory: directory, Persona: "You are a test writer.", Access: "write", Prompt: "Perform the requested task.", Model: "deepseek-v4-pro", ModelURL: model.URL, ModelToken: "attempt-only-token", MaxTokens: 4096, MaxSteps: 8, Timeout: 60 * time.Second, Environment: profile, SessionID: "native-runner-session", SessionKey: "native-runner-conversation:1", Resume: resume}
		if id == "coordination" || id == "journal-failure" {
			request.Task = &p.Collaboration{ID: "task", AgentID: "writer", Participants: []p.Participant{{ID: "writer", Name: "Writer", Access: "write"}}}
			request.Prompt = "Finish collaboration with the Camp tool."
			request.PersistIntent = func(_ context.Context, intent p.CoordinationIntent) error {
				if id == "journal-failure" {
					return errors.New("Injected durable journal failure")
				}
				if intent.Kind != "finish" || intent.CallID != "finish-call" {
					t.Error("unexpected intent", intent)
				}
				return nil
			}
		}
		return executor.Execute(ctx, request, func(kind, text string) error { return nil })
	}
	first := run("first", false)
	if first.Status != "succeeded" || !first.CleanupConfirmed || !first.SessionResumable || len(first.Checks) != 1 || first.Checks[0].Status != "passed" || first.ArchivePath == "" {
		t.Fatalf("first: %+v", first)
	}
	file, err := os.Open(first.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	destination := filepath.Join(data, "verified")
	if err := resources.ExtractArchive(ctx, file, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "result.txt"))
	if err != nil || string(content) != "remote native result\n" {
		t.Fatal("result lost", err)
	}
	second := run("second", true)
	if second.Status != "succeeded" || !second.SessionResumable || !resumed.Load() {
		t.Fatalf("resume: %+v; native context=%v", second, resumed.Load())
	}
	before := calls.Load()
	third := run("coordination", true)
	if third.Status != "succeeded" || third.Intent == nil || third.Intent.Kind != "finish" || !third.CleanupConfirmed || calls.Load() != before+1 {
		t.Fatalf("coordination: %+v; model calls=%d", third, calls.Load()-before)
	}
	if third.Intent.CallID != "finish-call" || third.Intent.Summary != "Native collaboration finished." {
		t.Fatal("Later tool replaced durable decision", third.Intent)
	}
	archive, err := os.Open(third.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	destination = filepath.Join(data, "verified-coordination")
	if err = resources.ExtractArchive(ctx, archive, destination); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(destination, "after-finish.txt")); !os.IsNotExist(err) {
		t.Fatal("same-batch tool mutated files after finish", err)
	}
	before = calls.Load()
	failed := run("journal-failure", true)
	if failed.Status != "interrupted" || failed.Intent != nil || !failed.CleanupConfirmed || calls.Load() != before+1 {
		t.Fatalf("journal failure did not stop native execution: %+v; model calls=%d", failed, calls.Load()-before)
	}
}
