package runnertools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

type testBackend struct{}

func (*testBackend) Failure() error { return nil }
func (*testBackend) Call(context.Context, sandboxexec.Request, func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
	return sandboxexec.Frame{}, nil
}

func testTools(t *testing.T, readOnly bool) *Tools {
	t.Helper()
	tools, err := NewTools(context.Background(), &testBackend{}, readOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tools.Close() })
	return tools
}

func postTool(t *testing.T, tools *Tools, endpoint string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, tools.Endpoint()+endpoint, bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer "+tools.Token())
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func shellInput(id string) map[string]any {
	return map[string]any{"commandId": id, "command": "echo hello", "workdir": "/workspace", "timeoutMs": 1000, "stdoutMaxBytes": 1024}
}

func TestToolsRejectForeignCapabilitiesAndReadOnlyCommands(t *testing.T) {
	tools := testTools(t, true)
	tools.call = func(context.Context, sandboxexec.Request, func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
		t.Error("A rejected request reached the sandbox")
		return sandboxexec.Frame{}, nil
	}
	for _, origin := range []bool{false, true} {
		req, _ := http.NewRequest(http.MethodPost, tools.Endpoint()+"/fs", strings.NewReader(`{}`))
		if origin {
			req.Header.Set("Authorization", "Bearer "+tools.Token())
			req.Header.Set("Origin", "http://localhost")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("Unauthorized status: %d", res.StatusCode)
		}
	}
	res := postTool(t, tools, "/shell", shellInput("read-only-command-1"))
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("Read-only shell returned %d", res.StatusCode)
	}
}

func TestCancelBeforeStartDoesNotExecuteOrReplay(t *testing.T) {
	tools := testTools(t, false)
	var calls atomic.Int32
	tools.call = func(context.Context, sandboxexec.Request, func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
		calls.Add(1)
		return sandboxexec.Frame{}, nil
	}
	id := "cancel-before-start-1"
	res := postTool(t, tools, "/shell/cancel", map[string]string{"commandId": id})
	_ = res.Body.Close()
	res = postTool(t, tools, "/shell", shellInput(id))
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if calls.Load() != 0 || !bytes.Contains(data, []byte(`"cleanupConfirmed":true`)) || !bytes.Contains(data, []byte(`"aborted":true`)) {
		t.Fatalf("Cancelled request ran or lacked cleanup: %d %s", calls.Load(), data)
	}
	res = postTool(t, tools, "/shell", shellInput(id))
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict || calls.Load() != 0 {
		t.Fatal("Cancelled command identity was replayed")
	}
}

func TestCommandCancellationWaitsForCleanupAndStreamsOutput(t *testing.T) {
	tools := testTools(t, false)
	started, release := make(chan struct{}), make(chan struct{})
	tools.call = func(ctx context.Context, _ sandboxexec.Request, output func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
		_ = output(sandboxexec.Frame{Type: "stdout", Data: []byte("real progress\n")})
		close(started)
		<-ctx.Done()
		<-release
		return sandboxexec.Frame{Type: "exit", Code: float64(130), Reason: "cancelled", Clean: true}, nil
	}
	id := "cancel-running-command-1"
	res := postTool(t, tools, "/shell", shellInput(id))
	defer res.Body.Close()
	decoder := json.NewDecoder(res.Body)
	var progress map[string]any
	if err := decoder.Decode(&progress); err != nil || progress["text"] != "real progress\n" {
		t.Fatalf("Missing live progress: %v %v", progress, err)
	}
	<-started
	ack := make(chan []byte, 1)
	go func() {
		response := postTool(t, tools, "/shell/cancel", map[string]string{"commandId": id})
		data, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		ack <- data
	}()
	select {
	case data := <-ack:
		t.Fatalf("Cancellation acknowledged before cleanup: %s", data)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if data := <-ack; !bytes.Contains(data, []byte(`"cleanupConfirmed":true`)) {
		t.Fatalf("Missing cleanup acknowledgement: %s", data)
	}
	var terminal map[string]any
	if err := decoder.Decode(&terminal); err != nil || terminal["cleanupConfirmed"] != true {
		t.Fatalf("Missing terminal confirmation: %v %v", terminal, err)
	}
}

func TestOutputTextKeepsSplitUnicode(t *testing.T) {
	out := outputText{all: tailBuffer{limit: 1024}}
	bytes := []byte("文")
	if text := out.append(bytes[:1], false); text != "" {
		t.Fatalf("Emitted incomplete UTF-8: %q", text)
	}
	if text := out.append(bytes[1:], false); text != "文" {
		t.Fatalf("Lost split UTF-8: %q", text)
	}
}

func TestOutputRetainsTailAndSeparateCaptureBudget(t *testing.T) {
	var tail = tailBuffer{limit: 5}
	_, _ = tail.Write([]byte("abc"))
	_, _ = tail.Write([]byte("defg"))
	if tail.String() != "cdefg" || !tail.truncated {
		t.Fatalf("Wrong output tail: %q", tail.String())
	}
}

func TestToolSealingDrainsExistingMutationAndRejectsNewOperations(t *testing.T) {
	tools := testTools(t, false)
	if err := tools.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- tools.Seal(context.Background()) }()
	// The lease is deliberately held as a mutation barrier.
	select {
	case err := <-result:
		t.Fatal("seal returned before mutation settled", err)
	case <-time.After(30 * time.Millisecond):
	}
	tools.release()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := tools.acquire(context.Background()); err == nil {
		tools.release()
		t.Fatal("new operation acquired sealed tools")
	}
	response := postTool(t, tools, "/fs", map[string]string{"operation": "resolve", "path": "/workspace"})
	defer response.Body.Close()
	if response.StatusCode != http.StatusGone {
		t.Fatal(response.StatusCode)
	}
}
