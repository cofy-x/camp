// Package dsh adapts the public DSH protocol independently of Camp business state.
package dsh

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/execution"
)

type Config struct {
	Model     string
	MaxTokens int
	MaxSteps  int
	Resume    bool
	Cwd       string
}

type Event struct {
	Type string
	Text string
}

type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type frameResult struct {
	frame rpcFrame
	err   error
}

// Run uses the public stdio protocol and requires a completed turn, wait response, and successful process exit.
func Run(ctx context.Context, cmd *exec.Cmd, c Config, session, prompt string, emit func(Event) error) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	output := func(text string) error { return emit(Event{Type: "output", Text: text}) }
	in, err := cmd.StdinPipe()
	if err != nil {
		return -1, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	frames := make(chan frameResult, 16)
	readCtx, cancelRead := context.WithCancel(context.Background())
	defer cancelRead()
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		defer close(frames)
		s := bufio.NewScanner(out)
		s.Buffer(make([]byte, 4096), 2<<20)
		for s.Scan() {
			if readCtx.Err() != nil {
				continue
			}
			var f rpcFrame
			err := json.Unmarshal(s.Bytes(), &f)
			if err == nil && f.JSONRPC != "2.0" {
				err = fmt.Errorf("Invalid JSON-RPC version")
			}
			select {
			case frames <- frameResult{f, err}:
			case <-readCtx.Done():
				continue
			}
		}
		err := s.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case frames <- frameResult{err: err}:
		case <-readCtx.Done():
		}
	}()
	var stderrErr error
	go func() {
		defer readers.Done()
		s := bufio.NewScanner(stderr)
		s.Buffer(make([]byte, 4096), 256<<10)
		for s.Scan() {
			if err := output("[DSH] " + s.Text() + "\n"); err != nil {
				stderrErr = err
				_ = cmd.Process.Kill()
				return
			}
		}
		if err := s.Err(); err != nil {
			stderrErr = err
			_ = cmd.Process.Kill()
		}
	}()
	// Drain output before reaping the process; early termination closes input, then stops the namespace after a grace period.
	exited := make(chan error, 1)
	go func() { readers.Wait(); exited <- cmd.Wait() }()
	steps, turnReason, requestID := 0, "", 0
	enc := json.NewEncoder(in)
	rpc := func(method string, params any) (json.RawMessage, error) {
		requestID++
		phase := ctx
		var cancel context.CancelFunc
		if method != "session/wait" {
			phase, cancel = context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
		}
		// The deadline also interrupts pipe writes if the runtime stops reading.
		stopClose := context.AfterFunc(phase, func() { _ = in.Close() })
		defer stopClose()
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": requestID, "method": method, "params": params}); err != nil {
			return nil, err
		}
		for {
			select {
			case <-phase.Done():
				return nil, fmt.Errorf("%s: %w", method, phase.Err())
			case item, open := <-frames:
				if !open {
					return nil, fmt.Errorf("DSH closed before %s completed", method)
				}
				if item.err != nil {
					return nil, fmt.Errorf("DSH communication failed during %s: %w", method, item.err)
				}
				f := item.frame
				if f.ID == requestID && f.Method == "" {
					if f.Error != nil {
						return nil, fmt.Errorf("%s: %s", method, f.Error.Message)
					}
					if len(f.Result) == 0 {
						return nil, fmt.Errorf("DSH returned no result for %s", method)
					}
					return f.Result, nil
				}
				if f.ID != 0 {
					return nil, fmt.Errorf("Unexpected DSH protocol request or response")
				}
				if f.Method != "session.event" {
					continue
				}
				var p struct {
					SessionID string `json:"sessionId"`
					Event     struct {
						Type string          `json:"type"`
						Data json.RawMessage `json:"data"`
					} `json:"event"`
				}
				if err := json.Unmarshal(f.Params, &p); err != nil {
					return nil, fmt.Errorf("Invalid DSH event format")
				}
				if p.SessionID != session {
					continue
				}
				switch p.Event.Type {
				case "step/start":
					steps++
					if err := emit(Event{Type: "model", Text: fmt.Sprintf("[DSH] Model step %d\n", steps)}); err != nil {
						return nil, err
					}
					if steps > c.MaxSteps {
						return nil, fmt.Errorf("The observed model step limit (%d) was exceeded; the next request may already have started", c.MaxSteps)
					}
				case "turn/end":
					var data struct {
						Reason struct {
							Kind string `json:"kind"`
						} `json:"reason"`
					}
					if err := json.Unmarshal(p.Event.Data, &data); err != nil {
						return nil, fmt.Errorf("Invalid DSH completion event format")
					}
					turnReason = data.Reason.Kind
					if err := emit(Event{Type: "completion", Text: "[DSH] Turn ended: " + string(p.Event.Data) + "\n"}); err != nil {
						return nil, err
					}
				case "assistant/message":
					text, err := assistantText(p.Event.Data)
					if err != nil {
						return nil, err
					}
					if text != "" {
						if err := emit(Event{Type: "assistant", Text: text + "\n"}); err != nil {
							return nil, err
						}
					}
				case "tool/call", "tool/result":
					if err := emit(Event{Type: map[string]string{"tool/call": "tool_call", "tool/result": "tool_result"}[p.Event.Type], Text: eventText(p.Event.Data) + "\n"}); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	_, runErr := func() (json.RawMessage, error) {
		cwd := c.Cwd
		if cwd == "" {
			cwd = "/workspace"
		}
		result, err := rpc("initialize", map[string]any{"cwd": cwd, "provider": "deepseek-official", "model": c.Model, "maxTokens": c.MaxTokens})
		if err != nil {
			return nil, err
		}
		var identity struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		}
		if json.Unmarshal(result, &identity) != nil || identity.ServerInfo.Name != "deepseek-harness-sdk-runtime" {
			return nil, fmt.Errorf("Unexpected DSH runtime identity")
		}
		if c.Resume {
			if _, err := rpc("session/resume", map[string]string{"sessionId": session}); err != nil {
				return nil, err
			}
		}
		result, err = rpc("session/prompt", map[string]any{"sessionId": session, "contentBlocks": []map[string]string{{"type": "text", "text": prompt}}})
		if err != nil {
			return nil, err
		}
		var receipt struct {
			MessageID string `json:"messageId"`
		}
		if json.Unmarshal(result, &receipt) != nil || receipt.MessageID == "" {
			return nil, fmt.Errorf("DSH returned no submission receipt")
		}
		if _, err := rpc("session/wait", map[string]string{"sessionId": session}); err != nil {
			return nil, err
		}
		if turnReason != "completed" {
			return nil, fmt.Errorf("DSH turn did not complete: %s", turnReason)
		}
		return rpc("shutdown", nil)
	}()
	_ = in.Close()
	// Stop delivering events, but drain the pipes until the process exits.
	cancelRead()
	exitErr := execution.Stop(cmd, exited)
	code := cmd.ProcessState.ExitCode()
	return code, errors.Join(runErr, exitErr, stderrErr)
}

// Assistant messages persist only visible text, never model thinking or tool payloads.
func assistantText(data json.RawMessage) (string, error) {
	var event struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return "", fmt.Errorf("Invalid DSH assistant message format: %w", err)
	}
	var parts []string
	for _, block := range event.Message.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func eventText(data json.RawMessage) string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return string(data)
	}
	var parts []string
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if text, ok := x["text"].(string); ok {
				parts = append(parts, text)
			}
			for _, key := range []string{"content", "message", "result", "output"} {
				if child, ok := x[key]; ok {
					visit(child)
				}
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(value)
	if len(parts) == 0 {
		return string(data)
	}
	return strings.Join(parts, "\n")
}
