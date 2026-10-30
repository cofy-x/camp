package layerfs

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/dsh"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/runnertools"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

type nativeToolCall struct {
	name  string
	input map[string]any
}

func TestLayerFSDSHIntegration(t *testing.T) {
	layerRuntime, dshRuntime := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME"), os.Getenv("CAMP_TEST_DSH_RUNTIME")
	if layerRuntime == "" || dshRuntime == "" {
		t.Skip("Set CAMP_TEST_LAYERFS_RUNTIME and CAMP_TEST_DSH_RUNTIME for native integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	source := filepath.Join(data, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "brief.txt"), []byte("A useful brief.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(data, "camp-sandbox")
	build := exec.CommandContext(ctx, "go", "build", "-o", helperPath, "../../cmd/camp-sandbox")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Build helper: %v\n%s", err, output)
	}
	helper, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := OpenBridge(ctx, BridgeConfig{Node: node, RuntimeDir: layerRuntime, DataRoot: data, Home: filepath.Join(data, "home")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bridge.Close(); err != nil {
			t.Error(err)
		}
	})
	prepared, err := bridge.Prepare(ctx, PrepareRequest{SourceDir: source, BaselineKey: "native-dsh-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := bridge.RemoveProject(cleanup, prepared.ProjectID); err != nil {
			t.Error(err)
		}
	})
	docker, err := NewDocker("/var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer docker.Close()
	open := func(toolCtx context.Context, runID string, readOnly bool) (Workspace, *Sandbox, *runnertools.Tools) {
		t.Helper()
		workspace, err := bridge.Create(ctx, CreateRequest{ProjectID: prepared.ProjectID, RunID: runID})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = bridge.CloseWorkspace(cleanup, workspace.WorkspaceID, true)
		})
		container, err := docker.Resolve(ctx, workspace.ContainerName)
		if err != nil {
			t.Fatal(err)
		}
		sandbox := &Sandbox{Docker: docker, ContainerID: container, User: fmt.Sprintf("%d:%d", workspace.UID, workspace.GID), Roots: sandboxexec.Roots{Workspace: workspace.MountPath, References: "/references"}}
		if err := sandbox.Setup(ctx, helper); err != nil {
			t.Fatal(err)
		}
		if err := docker.InstallFile(ctx, container, "/references/evidence.txt", []byte("Pinned source evidence.\n"), 0444); err != nil {
			t.Fatal(err)
		}
		tools, err := runnertools.NewTools(toolCtx, sandbox, readOnly)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := tools.Close(); err != nil {
				t.Error(err)
			}
		})
		return workspace, sandbox, tools
	}
	writer, writerSandbox, writerTools := open(ctx, "writer", false)
	writeCalls := []nativeToolCall{
		{"read", map[string]any{"file_path": "/workspace/brief.txt"}},
		{"edit", map[string]any{"file_path": "/workspace/brief.txt", "old_string": "useful", "new_string": "reviewed"}},
		{"write", map[string]any{"file_path": "/workspace/deliverables/memo.md", "content": "# Memo\nA useful result.\n"}},
		{"read", map[string]any{"file_path": "/references/evidence.txt"}},
		{"bash", map[string]any{"description": "Check isolated files and process cleanup", "command": "set -eu; test \"$(id -u)\" != 0; test -z \"${DEEPSEEK_API_KEY+x}${CAMP_TOOL_TOKEN+x}${CAMP_TOOL_ENDPOINT+x}\"; test ! -e /state; test ! -r /layerfs-store/global/store.sqlite; cat /workspace/deliverables/memo.md; printf 'shell result\\n' > /workspace/shell.txt; setsid sleep 120 >/dev/null 2>&1 & printf 'real-container-command\\n'", "workdir": "/workspace"}},
		{"search_files", map[string]any{"query": "reviewed"}},
	}
	output := nativeDSH(t, ctx, node, dshRuntime, filepath.Join(data, "writer-dsh"), writerTools, false, writeCalls, false)
	for _, expected := range []string{"Pinned source evidence.", "real-container-command", "A reviewed brief.", "# Memo"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("Missing real tool output %q:\n%s", expected, output)
		}
	}
	if writerSandbox.Failure() != nil {
		t.Fatal(writerSandbox.Failure())
	}
	if err := writerTools.Close(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := bridge.Checkpoint(ctx, writer.WorkspaceID, "DSH native bridge result")
	if err != nil || checkpoint.Kind != "Committed" {
		t.Fatalf("Checkpoint: %+v %v", checkpoint, err)
	}
	archive, err := bridge.Export(ctx, writer.WorkspaceID, filepath.Join(data, "result.tar"))
	if err != nil {
		t.Fatal(err)
	}
	files := readNativeArchive(t, archive.ArchivePath)
	if files["deliverables/memo.md"] != "# Memo\nA useful result.\n" || files["shell.txt"] != "shell result\n" || files["brief.txt"] != "A reviewed brief.\n" {
		t.Fatalf("Exported wrong files: %+v", files)
	}
	if original, err := os.ReadFile(filepath.Join(source, "brief.txt")); err != nil || string(original) != "A useful brief.\n" {
		t.Fatalf("Original changed: %q %v", original, err)
	}
	if err := bridge.CloseWorkspace(ctx, writer.WorkspaceID, false); err != nil {
		t.Fatal(err)
	}
	reader, _, readerTools := open(ctx, "reader", true)
	readCalls := []nativeToolCall{
		{"read", map[string]any{"file_path": "/workspace/brief.txt"}},
		{"list_files", map[string]any{"path": "/references"}},
		{"write", map[string]any{"file_path": "/workspace/forbidden", "content": "must not write"}},
		{"bash", map[string]any{"description": "Attempt an unavailable command", "command": "touch forbidden"}},
		{"read", map[string]any{"file_path": "/etc/passwd"}},
	}
	output = nativeDSH(t, ctx, node, dshRuntime, filepath.Join(data, "reader-dsh"), readerTools, true, readCalls, false)
	if !strings.Contains(output, "The path is outside Camp files.") {
		t.Fatalf("Forbidden path not rejected: %s", output)
	}
	if err := readerTools.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bridge.CloseWorkspace(ctx, reader.WorkspaceID, true); err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	cancelled, cancelledSandbox, cancelledTools := open(cancelCtx, "cancelled", false)
	started := make(chan error, 1)
	go func() {
		for attempt := 0; attempt < 100; attempt++ {
			code, err := docker.Exec(ctx, cancelledSandbox.ContainerID, cancelledSandbox.User, []string{"/usr/bin/test", "-f", "/tmp/camp-native-ready"}, io.Discard, io.Discard)
			if err != nil {
				started <- err
				cancelRun()
				return
			}
			if code == 0 {
				started <- nil
				cancelRun()
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		started <- fmt.Errorf("Native cancellation command did not start")
		cancelRun()
	}()
	_ = nativeDSH(t, cancelCtx, node, dshRuntime, filepath.Join(data, "cancelled-dsh"), cancelledTools, false, []nativeToolCall{
		{"bash", map[string]any{"description": "Wait with a detached child for cancellation", "command": "setsid sleep 120 >/dev/null 2>&1 & echo $! >/tmp/camp-native-child; printf ready >/tmp/camp-native-ready; wait", "timeoutMs": 120000}},
	}, true)
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := cancelledTools.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cancelledSandbox.Failure(); err != nil {
		t.Fatalf("Ordinary cancellation destroyed its sandbox: %v", err)
	}
	code, err := docker.Exec(ctx, cancelledSandbox.ContainerID, cancelledSandbox.User, []string{"/bin/sh", "-c", `pid=$(cat /tmp/camp-native-child); test ! -e /proc/"$pid"`}, io.Discard, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("Cancelled command left its detached child: %d %v", code, err)
	}
	if err := bridge.CloseWorkspace(ctx, cancelled.WorkspaceID, true); err != nil {
		t.Fatal(err)
	}
	t.Log("Real DSH providers, Go capability RPC, LayerFS edits, isolated commands, detached-child cleanup on completion and cancellation, read-only catalog, checkpoint/export and original preservation passed.")
}

func nativeDSH(t *testing.T, ctx context.Context, node, runtime, directory string, tools *runnertools.Tools, readOnly bool, calls []nativeToolCall, expectCancellation bool) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	patch, err := dsh.RemoteOverlay("Complete the explicitly requested work using Camp tools.", readOnly)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "runtime.patch.yml"), patch, 0600); err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	step := 0
	var names []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			t.Error(err)
			http.Error(w, "read failure", 500)
			return
		}
		if bytes.Contains(data, []byte(tools.Token())) {
			t.Error("Tool capability leaked into model context")
		}
		var body struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Error(err)
			http.Error(w, "invalid JSON", 500)
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		names = nil
		for _, tool := range body.Tools {
			names = append(names, tool.Name)
		}
		content := map[string]any{"type": "text", "text": "Native fixture complete."}
		stop := "end_turn"
		if step < len(calls) {
			call := calls[step]
			content = map[string]any{"type": "tool_use", "id": fmt.Sprintf("call-%d", step), "name": call.name, "input": call.input}
			stop = "tool_use"
		}
		step++
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "native-dsh", "model": "deepseek-v4-pro", "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": content}, {"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]string{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 1}}, {"type": "message_stop"},
		} {
			encoded, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
		}
	}))
	defer model.Close()
	cmd, err := execution.Command(execution.Config{RuntimeDir: runtime, NodePath: node, StateDir: filepath.Join(directory, "state"), APIKey: "native-keyless-fixture", BaseURL: model.URL, RemoteTools: true, ToolEndpoint: tools.Endpoint(), ToolToken: tools.Token()}, directory, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	var outputMutex sync.Mutex
	code, err := dsh.Run(ctx, cmd, dsh.Config{Model: "deepseek-v4-pro", MaxTokens: 1024, MaxSteps: 8}, "native-layerfs", "Exercise the requested native file and command tools.", func(event dsh.Event) error {
		outputMutex.Lock()
		defer outputMutex.Unlock()
		output.WriteString(event.Text)
		return nil
	})
	if expectCancellation {
		if ctx.Err() == nil || err == nil {
			t.Fatalf("Cancelled DSH reported successful completion: code=%d err=%v", code, err)
		}
		return output.String()
	}
	if err != nil || code != 0 {
		t.Fatalf("Native DSH failed: code=%d err=%v\n%s", code, err, output.String())
	}
	mutex.Lock()
	defer mutex.Unlock()
	expected := []string{"list_files", "read", "search_files"}
	if !readOnly {
		expected = append(expected, "bash", "edit", "write")
	}
	sort.Strings(names)
	sort.Strings(expected)
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("Wrong native catalog: %v", names)
	}
	if step != len(calls)+1 {
		t.Fatalf("Unexpected model request count: %d", step)
	}
	return output.String()
}

func readNativeArchive(t *testing.T, filename string) map[string]string {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result := map[string]string{}
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(reader, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		result[strings.TrimPrefix(header.Name, "./")] = string(data)
	}
	return result
}
