package remoteruns

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// modelProxy streams the Messages API without following redirects or retrying
// a request. Only the frozen server configuration chooses the upstream address.
func (m *Manager) modelProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := m.store.GetAttempt(r.Context(), id)
	if err != nil {
		machineError(w, 403, "Model capability is invalid.")
		return
	}
	token := r.Header.Get("X-Api-Key")
	if token == "" {
		token = bearer(r)
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(digest([]byte(token))), []byte(a.ModelTokenHash)) != 1 {
		machineError(w, 403, "Model capability is invalid.")
		return
	}
	if a.Phase != "executing" {
		machineError(w, 403, "Model access is only available during execution.")
		return
	}
	if _, err = m.store.ValidateAttempt(r.Context(), id, a.RunnerID, false); err != nil {
		machineError(w, 403, "Execution authorization ended.")
		return
	}
	run, err := m.store.GetRun(r.Context(), a.RunID)
	if err != nil {
		machineError(w, 503, "Execution state is unavailable.")
		return
	}
	model, err := m.models.Resolve(run.RuntimeKey)
	if err != nil {
		machineError(w, 503, "Frozen model configuration is unavailable.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	data, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		machineError(w, 413, "Model request exceeds its limit.")
		return
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(data, &body) != nil {
		machineError(w, 400, "Invalid Messages request.")
		return
	}
	var name string
	var tokens int
	if json.Unmarshal(body["model"], &name) != nil || json.Unmarshal(body["max_tokens"], &tokens) != nil || name != run.Model || tokens < 1 || tokens > run.MaxTokens {
		machineError(w, 400, "Model or output budget differs from the frozen request.")
		return
	}
	for _, key := range []string{"base_url", "baseUrl", "api_key", "apiKey"} {
		if _, ok := body[key]; ok {
			machineError(w, 400, "Upstream connection fields are controlled by the server.")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(run.TimeoutSeconds)*time.Second)
	defer cancel()
	started := time.Now()
	interrupt := func(stage, cause, reason string) {
		// Provider errors can contain keys, URLs, or generated content. Record
		// only our own classifications and identifiers for stream diagnostics.
		if cause == "request_cancelled" {
			switch {
			case m.ctx.Err() != nil:
				cause = "server_shutdown"
			case r.Context().Err() != nil:
				cause = "runner_disconnected"
			default:
				cause = "execution_stopped"
			}
		}
		m.logger.Warn("Remote model request interrupted", "attempt_id", id, "stage", stage, "cause", cause, "elapsed_ms", time.Since(started).Milliseconds())
		if cause != "execution_stopped" && cause != "server_shutdown" {
			_ = m.diagnostics.Record("connection:"+run.RuntimeKey, "error", "The model response was interrupted ("+cause+"); this request was not retried.")
		}
		m.modelInterrupted(id, reason)
	}
	stopLifetime := context.AfterFunc(m.ctx, cancel)
	defer stopLifetime()
	callID := newID()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		machineError(w, 503, "The server is stopping; the model request was not sent.")
		return
	}
	if m.proxyCalls[id] == nil {
		m.proxyCalls[id] = map[string]context.CancelFunc{}
	}
	if len(m.proxyCalls[id]) > 0 {
		m.mu.Unlock()
		machineError(w, 409, "A model request is already in progress.")
		return
	}
	m.proxyCalls[id][callID] = cancel
	m.mu.Unlock()
	modelStarted, released := false, false
	release := func() error {
		if released {
			return nil
		}
		var err error
		if modelStarted {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err = m.store.EndAttemptModel(cleanup, id)
			stop()
		}
		m.mu.Lock()
		delete(m.proxyCalls[id], callID)
		if len(m.proxyCalls[id]) == 0 {
			delete(m.proxyCalls, id)
		}
		m.mu.Unlock()
		released = true
		return err
	}
	defer func() {
		if err := release(); err != nil {
			interrupt("release", "persistence_error", "The model response state could not be saved; the request was not retried.")
		}
	}()
	if err = m.store.BeginAttemptModel(ctx, id, a.RunnerID); err != nil {
		machineError(w, 403, "Model execution authorization ended.")
		return
	}
	modelStarted = true
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(model.BaseURL, "/")+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		machineError(w, 503, "The model connection could not be created.")
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Api-Key", model.APIKey)
	upstream.Header.Set("Anthropic-Version", "2023-06-01")
	if beta := r.Header.Get("Anthropic-Beta"); len(beta) < 1024 && beta != "" {
		upstream.Header.Set("Anthropic-Beta", beta)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.DisableKeepAlives = true
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer transport.CloseIdleConnections()
	response, err := client.Do(upstream)
	if err != nil {
		interrupt("connect", modelFailureCause(err), "The model connection ended before a response; the request was not retried.")
		machineError(w, 502, "The upstream model connection ended. This request was not retried.")
		return
	}
	defer response.Body.Close()
	control := http.NewResponseController(w)
	_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
	contentType := response.Header.Get("Content-Type")
	streaming := strings.Contains(contentType, "text/event-stream")
	if streaming {
		w.Header().Set("Content-Type", "text/event-stream")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Provider errors can contain sensitive connection details. Never expose the
		// raw upstream error body to a runner or a browser execution log.
		machineError(w, response.StatusCode, "The configured model provider rejected the request. It was not retried.")
		_ = m.diagnostics.Record("connection:"+run.RuntimeKey, "error", "The configured provider rejected a model request.")
		return
	}
	w.WriteHeader(response.StatusCode)
	_ = m.diagnostics.Record("connection:"+run.RuntimeKey, "ready", "A real model request reached the configured provider.")
	buffer := make([]byte, 16<<10)
	var stream modelStreamCompletion
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			complete := false
			if streaming {
				if end, ok := stream.feed(buffer[:n]); ok {
					n, complete = end, true
					// DSH can stop reading and issue its next request as soon as
					// message_stop arrives. Persist completion and release the
					// request slot before making that frame visible to it.
					if err = release(); err != nil {
						interrupt("release", "persistence_error", "The model response state could not be saved; the request was not retried.")
						return
					}
				}
			}
			output := buffer[:n]
			if complete && output[len(output)-1] == '\r' {
				// CR is a complete SSE line ending. Normalize the last delimiter
				// to CRLF when its optional LF has not arrived yet, so consumers
				// that look for CRLF also see the complete terminal frame.
				output = append(output, '\n')
			}
			_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
			// After terminal completion the SDK may close its request while
			// Flush returns. The daemon owns delivery errors; this old request
			// must not interrupt work that has already consumed its response.
			if _, err = w.Write(output); err != nil {
				if !complete {
					interrupt("write", modelFailureCause(err), "The runner model stream disconnected; it cannot be resumed.")
				}
				return
			}
			if err = control.Flush(); err != nil {
				if !complete {
					interrupt("flush", modelFailureCause(err), "The runner model stream could not be flushed; it cannot be resumed.")
				}
				return
			}
			if complete {
				// A terminal Messages event, rather than a later TCP EOF, owns
				// successful stream completion. Closing this body also releases
				// providers that retain their socket after the terminal event.
				m.logger.Debug("Remote model response completed", "attempt_id", id, "completion", "message_stop", "elapsed_ms", time.Since(started).Milliseconds())
				return
			}
		}
		if readErr == io.EOF {
			if streaming {
				interrupt("read", "missing_message_stop", "The model stream ended without a complete response; the request was not retried.")
			}
			return
		}
		if readErr != nil {
			interrupt("read", modelFailureCause(readErr), "The model stream was interrupted; the request was not retried.")
			return
		}
	}
}

func modelFailureCause(err error) string {
	var network net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "request_cancelled"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "truncated_transport"
	case errors.As(err, &network) && network.Timeout():
		return "transport_timeout"
	default:
		return "transport_error"
	}
}

// modelStreamCompletion observes SSE framing without retaining generated text.
// Large events still pass through unchanged; they cannot be terminal frames.
// A complete, valid message_stop event is deliberately distinguished from a
// message_delta stop_reason or an incomplete frame at transport EOF.
type modelStreamCompletion struct {
	line     []byte
	data     []byte
	event    string
	overflow bool
	lastCR   bool
}

func (s *modelStreamCompletion) feed(chunk []byte) (int, bool) {
	const maxFrame = 64 << 10
	for offset, b := range chunk {
		if b == '\n' && s.lastCR {
			s.lastCR = false
			continue
		}
		s.lastCR = b == '\r'
		if b != '\n' && b != '\r' {
			if len(s.line) < maxFrame {
				s.line = append(s.line, b)
			} else {
				s.overflow = true
			}
			continue
		}
		line := string(s.line)
		s.line = s.line[:0]
		if line == "" {
			var event struct {
				Type string `json:"type"`
			}
			complete := !s.overflow && (s.event == "" || s.event == "message_stop") && json.Unmarshal(s.data, &event) == nil && event.Type == "message_stop"
			s.data, s.event, s.overflow = s.data[:0], "", false
			if complete {
				end := offset + 1
				if b == '\r' && end < len(chunk) && chunk[end] == '\n' {
					end++
				}
				return end, true
			}
			continue
		}
		if s.overflow || strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			s.event = value
		case "data":
			if len(s.data)+len(value)+1 > maxFrame {
				s.overflow = true
			} else {
				s.data = append(s.data, value...)
				s.data = append(s.data, '\n')
			}
		}
	}
	return 0, false
}

func (m *Manager) modelInterrupted(id, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := m.store.GetAttempt(ctx, id)
	if err != nil || a.CancelRequested || a.Phase == "finished" {
		return
	}
	_, _ = m.store.InterruptAttempt(ctx, id, reason)
	m.Wake()
}
