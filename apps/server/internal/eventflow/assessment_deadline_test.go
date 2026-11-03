package eventflow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
)

func TestAssessmentAcceptsDelayedHeadersWithinTotalDeadline(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		// A non-streaming provider may wait for generation to finish before it
		// sends headers. This still fits the assessment's 30-second allowance.
		timer := time.NewTimer(18 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			assessmentMessage(w, `{"outcome":"accept","goal":"Prepare release notes.","reason":"Matches the release policy."}`)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	manager := &Manager{models: frozenModels{model: configuration.Model{BaseURL: server.URL, APIKey: "private-model-key", Model: "model", MaxTokens: 1024, TimeoutSeconds: 180}}}
	result := manager.assess(ctx, assessmentCandidate())
	if result.Outcome != "accept" || result.Goal != "Prepare release notes." || calls.Load() != 1 {
		t.Fatalf("a response inside the total deadline was rejected or retried: %+v, calls=%d", result, calls.Load())
	}
}

func TestAssessmentDeadlineIncludesResponseBodyAndRespectsConfiguredLimit(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name    string
		seconds int
		limit   time.Duration
	}{
		{"assessment maximum", 180, 30 * time.Second},
		{"shorter configured limit", 1, time.Second},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			cancelled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"content":[`)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(cancelled)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), scenario.limit+10*time.Second)
			defer cancel()
			manager := &Manager{models: frozenModels{model: configuration.Model{BaseURL: server.URL, APIKey: "private-model-key", Model: "model", MaxTokens: 1024, TimeoutSeconds: scenario.seconds}}}
			started := time.Now()
			result := manager.assess(ctx, assessmentCandidate())
			elapsed := time.Since(started)
			if result.Outcome != "unavailable" || calls.Load() != 1 || elapsed < scenario.limit || elapsed > scenario.limit+5*time.Second {
				t.Fatalf("body read escaped its single deadline or retried: %+v, calls=%d, elapsed=%s", result, calls.Load(), elapsed)
			}
			select {
			case <-cancelled:
			case <-ctx.Done():
				t.Fatal("the cancelled assessment kept its provider connection alive")
			}
		})
	}
}
