// Package eventflow owns bounded assessment and external-delivery workers.
// Storage alone applies policies, authorizes tasks and commits their effects.
package eventflow

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/githubchannel"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type Store interface {
	FireWorkItemTimers(context.Context) error
	RecoverAutomationWork(context.Context) error
	ClaimAutomationEvaluation(context.Context, string, storage.Budget) (*orchestration.Candidate, error)
	CompleteAutomationEvaluation(context.Context, string, orchestration.Assessment) error
	ClaimAutomationDelivery(context.Context) (*orchestration.Delivery, error)
	CompleteAutomationDelivery(context.Context, string, orchestration.DeliveryResult) error
}

type Models interface {
	Snapshot() (string, configuration.Model)
	Resolve(string) (configuration.Model, error)
}

type Channel interface {
	Deliver(context.Context, orchestration.Delivery) orchestration.DeliveryResult
}

type Config struct {
	Channel      Channel
	PollInterval time.Duration
}

type Manager struct {
	store   Store
	models  Models
	channel Channel
	logger  *slog.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	workers sync.WaitGroup
	once    sync.Once
	period  time.Duration
}

func New(ctx context.Context, store Store, models Models, credentials *configuration.GitHubCredentials, logger *slog.Logger, configs ...Config) (*Manager, error) {
	if store == nil || models == nil || len(configs) > 1 {
		return nil, errors.New("Event processing requires storage and model configuration")
	}
	config := Config{PollInterval: time.Second}
	if len(configs) == 1 {
		config = configs[0]
		if config.PollInterval <= 0 {
			config.PollInterval = time.Second
		}
	}
	if config.Channel == nil {
		if credentials == nil {
			return nil, errors.New("Event delivery requires private GitHub credentials")
		}
		config.Channel = githubchannel.New(credentials)
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := store.RecoverAutomationWork(ctx); err != nil {
		return nil, err
	}
	worker, cancel := context.WithCancel(context.Background())
	m := &Manager{store: store, models: models, channel: config.Channel, logger: logger, ctx: worker, cancel: cancel, done: make(chan struct{}), period: config.PollInterval}
	m.workers.Add(2)
	go func() { defer m.workers.Done(); m.evaluate() }()
	go func() { defer m.workers.Done(); m.deliver() }()
	go func() { m.workers.Wait(); close(m.done) }()
	return m, nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.once.Do(m.cancel)
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) pause() bool {
	timer := time.NewTimer(m.period)
	defer timer.Stop()
	select {
	case <-m.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) evaluate() {
	var pending *orchestration.Candidate
	var result orchestration.Assessment
	for m.ctx.Err() == nil {
		if pending == nil {
			if err := m.store.FireWorkItemTimers(m.ctx); err != nil {
				m.report("fire_timers", "")
			}
			revision, model := m.models.Snapshot()
			budget := storage.Budget{RequireRunner: true, RuntimeKey: revision, Model: model.Model, MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: model.TimeoutSeconds}
			var err error
			pending, err = m.store.ClaimAutomationEvaluation(m.ctx, revision, budget)
			if err != nil {
				m.report("claim_evaluation", "")
			}
			if pending != nil {
				result = m.assess(m.ctx, *pending)
			}
		}
		if pending != nil {
			if err := m.store.CompleteAutomationEvaluation(m.ctx, pending.ID, result); err != nil {
				// Retain the completed response while retrying only its transaction.
				// Another provider request would spend a second, unapproved allowance.
				m.report("complete_evaluation", pending.ID)
			} else {
				pending = nil
			}
		}
		if !m.pause() {
			return
		}
	}
}

func (m *Manager) deliver() {
	var pending *orchestration.Delivery
	var result orchestration.DeliveryResult
	for m.ctx.Err() == nil {
		if pending == nil {
			var err error
			pending, err = m.store.ClaimAutomationDelivery(m.ctx)
			if err != nil {
				m.report("claim_delivery", "")
			}
			if pending != nil {
				result = m.channel.Deliver(m.ctx, *pending)
				if result.Status == "unknown" && !pending.Reconcile && m.ctx.Err() == nil {
					// Resolve a lost POST reply with one read-only reconciliation.
					// An unresolved result remains visible until an explicit action.
					reconcile := *pending
					reconcile.Reconcile = true
					result = m.channel.Deliver(m.ctx, reconcile)
					// A matching earlier comment confirms the effect, not the
					// outcome of this latest POST. Retain that uncertainty too.
					result.AttemptOutcome = "unknown"
				}
			}
		}
		if pending != nil {
			result.ClaimRevision = pending.Revision
			if err := m.store.CompleteAutomationDelivery(m.ctx, pending.ID, result); err != nil {
				m.report("complete_delivery", pending.ID)
			} else {
				pending = nil
			}
		}
		if !m.pause() {
			return
		}
	}
}

func (m *Manager) report(stage, id string) {
	if m.ctx.Err() == nil {
		// Persisted source text, provider errors and credentials never enter logs.
		m.logger.Error("Event processing could not commit durable state", "stage", stage, "operation_id", id)
	}
}
