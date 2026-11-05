package eventflow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type queueStore struct {
	mu               sync.Mutex
	recovered        bool
	candidate        *orchestration.Candidate
	delivery         *orchestration.Delivery
	evaluation       orchestration.Assessment
	result           orchestration.DeliveryResult
	evaluationWrites int
	deliveryWrites   int
	failFirst        bool
}

func (s *queueStore) FireWorkItemTimers(context.Context) error { return nil }

func (s *queueStore) RecoverAutomationWork(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recovered = true
	return nil
}
func (s *queueStore) ClaimAutomationEvaluation(_ context.Context, revision string, budget storage.Budget) (*orchestration.Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.recovered {
		return nil, errors.New("claimed before recovery")
	}
	value := s.candidate
	s.candidate = nil
	return value, nil
}
func (s *queueStore) CompleteAutomationEvaluation(_ context.Context, id string, result orchestration.Assessment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evaluationWrites++
	if s.failFirst && s.evaluationWrites == 1 {
		return errors.New("temporary transaction failure")
	}
	s.evaluation = result
	return nil
}
func (s *queueStore) ClaimAutomationDelivery(context.Context) (*orchestration.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.recovered {
		return nil, errors.New("claimed before recovery")
	}
	value := s.delivery
	s.delivery = nil
	return value, nil
}
func (s *queueStore) CompleteAutomationDelivery(_ context.Context, id string, result orchestration.DeliveryResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveryWrites++
	if s.failFirst && s.deliveryWrites == 1 {
		return errors.New("temporary transaction failure")
	}
	s.result = result
	return nil
}

type countingChannel struct{ calls atomic.Int64 }

func (c *countingChannel) Deliver(context.Context, orchestration.Delivery) orchestration.DeliveryResult {
	c.calls.Add(1)
	return orchestration.DeliveryResult{Status: "delivered", CommentID: 99, URL: "https://github.com/owner/repo/issues/7#issuecomment-99"}
}

func TestWorkersRetryOnlyCompletedTransactionsAndDrainOnShutdown(t *testing.T) {
	var modelCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		assessmentMessage(w, `{"outcome":"ignore","reason":"No new request."}`)
	}))
	defer server.Close()
	candidate := assessmentCandidate()
	store := &queueStore{candidate: &candidate, delivery: &orchestration.Delivery{ID: "delivery"}, failFirst: true}
	channel := &countingChannel{}
	models := frozenModels{model: configuration.Model{BaseURL: server.URL, APIKey: "private-key", Model: "model", MaxTokens: 1024, TimeoutSeconds: 1}}
	manager, err := New(context.Background(), store, models, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Channel: channel, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	for {
		store.mu.Lock()
		complete := store.evaluation.Outcome == "ignore" && store.result.Status == "delivered"
		store.mu.Unlock()
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workers did not persist completed results")
		}
		time.Sleep(time.Millisecond)
	}
	if modelCalls.Load() != 1 || channel.calls.Load() != 1 {
		t.Fatalf("repeated external effect: model=%d channel=%d", modelCalls.Load(), channel.calls.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.evaluationWrites != 2 || store.deliveryWrites != 2 {
		t.Fatalf("unexpected transaction attempts: %d %d", store.evaluationWrites, store.deliveryWrites)
	}
}

func TestDeliveryContinuesWhileAssessmentWaitsForProvider(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	candidate := assessmentCandidate()
	store := &queueStore{candidate: &candidate, delivery: &orchestration.Delivery{ID: "delivery"}}
	channel := &countingChannel{}
	models := frozenModels{model: configuration.Model{BaseURL: server.URL, APIKey: "private-key", Model: "model", MaxTokens: 1024, TimeoutSeconds: 30}}
	manager, err := New(context.Background(), store, models, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Channel: channel, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("assessment did not start")
	}
	deadline := time.Now().Add(time.Second)
	for channel.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("delivery was blocked by assessment")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

type uncertainChannel struct {
	mu         sync.Mutex
	reconciles []bool
}

func (c *uncertainChannel) Deliver(_ context.Context, delivery orchestration.Delivery) orchestration.DeliveryResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconciles = append(c.reconciles, delivery.Reconcile)
	return orchestration.DeliveryResult{Status: "unknown", Reason: "The original comment could not be confirmed."}
}

func TestUncertainDeliveryReconcilesOnceWithoutPollingOrResending(t *testing.T) {
	for _, recovering := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "recovery"}[recovering], func(t *testing.T) {
			store := &queueStore{delivery: &orchestration.Delivery{ID: "uncertain", Reconcile: recovering}}
			channel := &uncertainChannel{}
			manager, err := New(context.Background(), store, frozenModels{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Channel: channel, PollInterval: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Shutdown(context.Background())
			deadline := time.Now().Add(time.Second)
			for {
				store.mu.Lock()
				complete := store.result.Status == "unknown"
				store.mu.Unlock()
				if complete {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("unknown delivery was not retained")
				}
				time.Sleep(time.Millisecond)
			}
			if err := manager.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			channel.mu.Lock()
			defer channel.mu.Unlock()
			expected := 2
			if recovering {
				expected = 1
			}
			if len(channel.reconciles) != expected {
				t.Fatalf("unexpected delivery attempts: %v", channel.reconciles)
			}
			if !channel.reconciles[len(channel.reconciles)-1] {
				t.Fatal("uncertain delivery was resent")
			}
		})
	}
}

type recoveredCommentChannel struct{ calls atomic.Int64 }

func (c *recoveredCommentChannel) Deliver(_ context.Context, delivery orchestration.Delivery) orchestration.DeliveryResult {
	c.calls.Add(1)
	if !delivery.Reconcile {
		return orchestration.DeliveryResult{Status: "unknown"}
	}
	return orchestration.DeliveryResult{Status: "delivered", CommentID: 99, URL: "https://github.com/owner/repo/issues/7#issuecomment-99"}
}

func TestMatchingEarlierCommentDoesNotRewriteUnknownLatestPost(t *testing.T) {
	store := &queueStore{delivery: &orchestration.Delivery{ID: "uncertain-resend"}}
	channel := &recoveredCommentChannel{}
	manager, err := New(context.Background(), store, frozenModels{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Channel: channel, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		result := store.result
		store.mu.Unlock()
		if result.Status == "delivered" {
			if result.AttemptOutcome != "unknown" || channel.calls.Load() != 2 {
				t.Fatal("read-only evidence invented a successful latest POST", result, channel.calls.Load())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("matching comment receipt did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}
