package dsh

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The subprocess fixture exercises framing, ordering and real OS exit/reaping
// without DSH installation, network access or any model credential.
func TestRPCProcessHelper(t *testing.T) {
	mode := os.Getenv("CAMP_RPC_FIXTURE")
	if mode == "" {
		return
	}
	emit := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	event := func(kind string, data any) {
		emit(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{"sessionId": "unit", "event": map[string]any{"type": kind, "data": data}}})
	}
	s := bufio.NewScanner(os.Stdin)
	resumed := false
	for s.Scan() {
		var frame rpcFrame
		_ = json.Unmarshal(s.Bytes(), &frame)
		result := any(map[string]any{})
		switch frame.Method {
		case "initialize":
			if mode == "malformed" {
				fmt.Fprintln(os.Stdout, "not-json")
				continue
			}
			result = map[string]any{"serverInfo": map[string]string{"name": "deepseek-harness-sdk-runtime", "version": "0.0.1"}}
		case "session/prompt":
			if mode == "resume" && !resumed {
				os.Exit(4)
			}
			result = map[string]string{"messageId": "accepted"}
		case "session/resume":
			if mode == "resume-error" {
				emit(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "error": map[string]any{"code": -1, "message": "Saved session is missing"}})
				continue
			}
			resumed = true
		case "session/wait":
			event("assistant/message", map[string]any{"message": map[string]any{"content": []map[string]string{{"type": "text", "text": "partial output"}}}})
			if mode == "eof" {
				os.Exit(3)
			}
			if mode == "cancel" {
				continue
			}
			if mode == "rpc-error" {
				emit(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "error": map[string]any{"code": -1, "message": "fixture model failure"}})
				continue
			}
			if mode == "steps" {
				for range 3 {
					event("step/start", map[string]int{"step": 1})
				}
			}
			if mode != "idle-only" {
				reason := "completed"
				if mode == "failed-turn" {
					reason = "error"
				}
				event("turn/end", map[string]any{"reason": map[string]string{"kind": reason}})
			}
		case "shutdown":
			emit(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "result": result})
			os.Exit(0)
		}
		emit(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "result": result})
		if mode == "blocked-write" {
			time.Sleep(30 * time.Second)
		}
	}
	os.Exit(0)
}

func TestResumePrecedesPromptAndFailureNeverSubmits(t *testing.T) {
	for _, mode := range []string{"resume", "resume-error"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestRPCProcessHelper$")
			cmd.Env = append(os.Environ(), "CAMP_RPC_FIXTURE="+mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var output strings.Builder
			code, err := Run(ctx, cmd, Config{Model: "fixture", MaxTokens: 32, MaxSteps: 2, Resume: true}, "unit", "new request", func(e Event) error { output.WriteString(e.Text); return nil })
			if mode == "resume" && (err != nil || code != 0) {
				t.Fatalf("resume: %d %v", code, err)
			}
			if mode == "resume-error" && (err == nil || output.Len() != 0) {
				t.Fatalf("missing session submitted a prompt: %s %v", output.String(), err)
			}
		})
	}
}

func TestCancellationInterruptsBlockedProtocolWrite(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestRPCProcessHelper$")
	cmd.Env = append(os.Environ(), "CAMP_RPC_FIXTURE=blocked-write")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Run(ctx, cmd, Config{MaxSteps: 2, MaxTokens: 32}, "unit", strings.Repeat("x", 1<<20), func(Event) error { return nil })
	if err == nil || cmd.ProcessState == nil || time.Since(start) > 9*time.Second {
		t.Fatalf("blocked write did not cancel and reap: %v", err)
	}
}

func TestRPCCompletionAndFailure(t *testing.T) {
	for _, mode := range []string{"success", "failed-turn", "idle-only", "rpc-error", "eof", "malformed", "steps", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestRPCProcessHelper$")
			cmd.Env = append(os.Environ(), "CAMP_RPC_FIXTURE="+mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var output strings.Builder
			code, err := Run(ctx, cmd, Config{Model: "fixture", MaxTokens: 32, MaxSteps: 2}, "unit", "prompt", func(event Event) error {
				_, err := output.WriteString(event.Text)
				if mode == "cancel" && strings.Contains(output.String(), "partial output") {
					cancel()
				}
				return err
			})
			if mode == "success" {
				if err != nil || code != 0 || !strings.Contains(output.String(), "partial output") {
					t.Fatalf("run = %d %v %s", code, err, output.String())
				}
			} else if err == nil {
				t.Fatalf("%s was accepted as success", mode)
			}
			if cmd.ProcessState == nil {
				t.Fatal("process was not reaped")
			}
			if mode != "malformed" && !strings.Contains(output.String(), "partial output") {
				t.Fatal("partial output lost")
			}
		})
	}
}

func TestAssistantTextExcludesThinkingAndToolPayloads(t *testing.T) {
	data := json.RawMessage(`{"message":{"content":[{"type":"thinking","text":"private reasoning"},{"type":"text","text":"Visible answer"},{"type":"tool_use","text":"not a response","input":{"text":"tool input"}},{"type":"text","text":"Second paragraph"}]}}`)
	text, err := assistantText(data)
	if err != nil || text != "Visible answer\nSecond paragraph" {
		t.Fatalf("Unexpected assistant text: %q %v", text, err)
	}
	text, err = assistantText(json.RawMessage(`{"message":{"content":[{"type":"tool_use","id":"call","input":{"command":"ls"}}]}}`))
	if err != nil || text != "" {
		t.Fatalf("Tool-only assistant message became visible prose: %q %v", text, err)
	}
}
