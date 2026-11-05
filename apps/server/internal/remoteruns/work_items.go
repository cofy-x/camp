package remoteruns

import (
	"context"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) OperateWorkItem(ctx context.Context, id string, in orchestration.WorkItemOperation) (orchestration.WorkItem, error) {
	// Resolve committed intent before today's model/route readiness can change
	// the outcome of an uncertain accepted request.
	if old, found, err := m.store.FindWorkItemOperation(ctx, id, in); err != nil || found {
		return old, err
	}
	var budget storage.Budget
	needsGrant := in.Kind == "execute" || in.Kind == "answer_continue"
	if in.Kind == "answer_continue" {
		if current, e := m.store.GetWorkItem(ctx, id); e == nil && current.ActiveTaskID != "" {
			needsGrant = false
		}
	}
	if needsGrant {
		if ready, reason, _ := m.Availability(); !ready {
			return orchestration.WorkItem{}, problem("execution_unavailable", reason)
		}
		revision, model := m.models.Snapshot()
		budget = storage.Budget{RequireRunner: true, RuntimeKey: revision, Model: model.Model, MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: model.TimeoutSeconds}
	}
	w, err := m.store.OperateWorkItem(ctx, id, in, budget)
	if err == nil {
		m.Wake()
	}
	return w, err
}

func (m *Manager) CreateWorkItem(ctx context.Context, room string, in orchestration.WorkItemInput) (orchestration.WorkItem, error) {
	if old, found, err := m.store.FindCreateWorkItem(ctx, room, in); err != nil || found {
		return old, err
	}
	if !in.Start {
		return m.store.CreateWorkItem(ctx, room, in)
	}
	if ready, reason, _ := m.Availability(); !ready {
		return orchestration.WorkItem{}, problem("execution_unavailable", reason)
	}
	revision, model := m.models.Snapshot()
	w, err := m.store.CreateWorkItemWithBudget(ctx, room, in, storage.Budget{RequireRunner: true, RuntimeKey: revision, Model: model.Model, MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: model.TimeoutSeconds})
	if err == nil {
		m.Wake()
	}
	return w, err
}
