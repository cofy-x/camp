package eventflow

import (
	"context"
	"encoding/json"
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
	timerScans       atomic.Int64
	mu               sync.Mutex
	recovered        bool
	candidates       []orchestration.Candidate
	delivery         *orchestration.Delivery
	evaluation       orchestration.Assessment
	result           orchestration.DeliveryResult
	evaluationWrites int
	deliveryWrites   int
	failFirst        bool
}

func (s *queueStore) FireWorkItemTimers(context.Context) error {
	s.timerScans.Add(1)
	return nil
}

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
	if len(s.candidates) == 0 {
		return nil, nil
	}
	value := s.candidates[0]
	s.candidates = s.candidates[1:]
	return &value, nil
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
	store := &queueStore{candidates: []orchestration.Candidate{candidate}, delivery: &orchestration.Delivery{ID: "delivery"}, failFirst: true}
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

func TestTimersAndDeliveryContinueWhileAssessmentWaitsForProvider(t *testing.T) {
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
	store := &queueStore{candidates: []orchestration.Candidate{candidate}, delivery: &orchestration.Delivery{ID: "delivery"}}
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
	scansAtAssessment := store.timerScans.Load()
	deadline := time.Now().Add(time.Second)
	for channel.calls.Load() == 0 || store.timerScans.Load() == scansAtAssessment {
		if time.Now().After(deadline) {
			t.Fatalf("provider blocked timers or delivery: timer scans=%d, deliveries=%d", store.timerScans.Load(), channel.calls.Load())
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAssessmentsProgressInIndependentRoomsWhileProviderWaits(t *testing.T) {
	started := make(chan struct{})
	allowCompletion := make(chan struct{})
	var modelCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		var body struct {
			Messages []struct{ Content string }
		}
		var candidate orchestration.Candidate
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 1 || json.Unmarshal([]byte(body.Messages[0].Content), &candidate) != nil {
			http.Error(w, "Invalid assessment fixture request", http.StatusBadRequest)
			return
		}
		if candidate.Policy.RoomID == "slow" {
			close(started)
			<-r.Context().Done()
			return
		}
		select {
		case <-allowCompletion:
			assessmentMessage(w, `{"outcome":"ignore","reason":"Independent request assessed."}`)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	slow, ready := assessmentCandidate(), assessmentCandidate()
	slow.Policy.RoomID = "slow"
	ready.ID, ready.Policy.RoomID = "independent", "ready"
	store := &queueStore{candidates: []orchestration.Candidate{slow, ready}}
	models := frozenModels{model: configuration.Model{BaseURL: server.URL, APIKey: "private-key", Model: "model", MaxTokens: 1024, TimeoutSeconds: 30}}
	manager, err := New(context.Background(), store, models, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Channel: &countingChannel{}, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("the slow assessment did not reach the provider")
	}
	close(allowCompletion)
	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		result, writes := store.evaluation, store.evaluationWrites
		store.mu.Unlock()
		if result.Outcome == "ignore" {
			if writes != 1 || modelCalls.Load() != 2 {
				t.Fatal("independent progress repeated a provider request or completion", writes, modelCalls.Load())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the independent assessment did not commit while the other provider request waited")
		}
		time.Sleep(time.Millisecond)
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
