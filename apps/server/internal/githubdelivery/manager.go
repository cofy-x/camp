package githubdelivery

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// Manager has one worker and retains each network result until its transaction
// commits. A database outage never causes another external call for that claim.
type Manager struct {
	store       *storage.Store
	credentials *configuration.GitHubCredentials
	client      *Client
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	once        sync.Once
	logger      *slog.Logger
	models      Models
}

type Models interface {
	Snapshot() (string, configuration.Model)
}

func New(store *storage.Store, credentials *configuration.GitHubCredentials, files Files, models Models, logger *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{store: store, credentials: credentials, client: NewClient(credentials, files), models: models, ctx: ctx, cancel: cancel, done: make(chan struct{}), logger: logger}
	go m.run()
	return m
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
func (m *Manager) run() {
	defer close(m.done)
	var claim *storage.GitHubDeliveryClaim
	var result orchestration.GitHubDeliveryResult
	for m.ctx.Err() == nil {
		if claim == nil {
			var err error
			claim, err = m.store.ClaimGitHubDelivery(m.ctx)
			if err != nil && m.ctx.Err() == nil {
				m.logger.Error("GitHub delivery claim could not be committed")
			}
			if claim != nil {
				result = m.client.Step(m.ctx, claim.Delivery, claim.CredentialRef)
			}
		}
		if claim != nil {
			var budget storage.Budget
			if m.models != nil {
				revision, model := m.models.Snapshot()
				if model.APIKey != "" {
					budget = storage.Budget{RequireRunner: true, RuntimeKey: revision, Model: model.Model, MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: model.TimeoutSeconds}
				}
			}
			if err := m.store.CompleteGitHubDelivery(m.ctx, claim.Delivery.ID, claim.Delivery.Revision, result, budget); err == nil {
				claim = nil
			} else if m.ctx.Err() == nil {
				m.logger.Error("GitHub delivery receipt could not be committed", "delivery_id", claim.Delivery.ID)
			}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-m.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (m *Manager) Connect(ctx context.Context, input orchestration.GitHubRepositoryInput) (orchestration.GitHubRepository, error) {
	if saved, found, err := m.store.FindGitHubRepositoryRequest(ctx, input); err != nil || found {
		return saved, err
	}
	token := input.Token
	ref := ""
	var old orchestration.GitHubRepository
	if input.ID != "" {
		var err error
		old, ref, err = m.store.GitHubRepositoryCredential(ctx, input.ID)
		if err != nil {
			return old, err
		}
		if old.Revision != input.Revision {
			return old, &storage.Error{Code: "revision_conflict", Message: "The repository connection changed. Reload its settings."}
		}
		if token == "" && input.Enabled {
			credential, err := m.credentials.Read(ref)
			if err != nil {
				return old, errors.New("The private GitHub credential is unavailable")
			}
			token = credential.Token
		}
	}
	verified := old
	if input.Enabled || input.Token != "" || input.ID == "" {
		var err error
		verified, err = m.client.Verify(ctx, token, input)
		if err != nil {
			return verified, &storage.Error{Code: "invalid_repository", Message: err.Error()}
		}
	} else if input.Repository != old.Repository || input.CIWorkflow != old.CIWorkflow || input.DeploymentWorkflow != old.DeploymentWorkflow {
		return old, &storage.Error{Code: "invalid_repository", Message: "Disable the saved connection without changing its repository or workflows."}
	}
	if input.Token != "" {
		var err error
		ref, err = m.credentials.Write(input.Token, verified.ActorID)
		if err != nil {
			return verified, err
		}
	}
	return m.store.SaveGitHubRepository(ctx, input, verified, ref)
}
