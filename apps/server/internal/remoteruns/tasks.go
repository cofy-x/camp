package remoteruns

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"os"
	"path/filepath"
)

func (m *Manager) CreateTask(ctx context.Context, room string, in storage.CreateTaskInput) (storage.CollaborationTask, error) {
	// The storage request receipt is checked before route or model changes can
	// reinterpret an already accepted authorization.
	if t, found, err := m.store.FindTaskCreation(ctx, room, in); err != nil || found {
		return t, err
	}
	if ready, reason, _ := m.Availability(); !ready {
		return storage.CollaborationTask{}, problem("execution_unavailable", reason)
	}
	for _, ref := range in.References {
		if ref.Kind == "file" {
			if _, err := m.store.ValidateFileReference(ctx, room, ref); err != nil {
				return storage.CollaborationTask{}, err
			}
			if _, err := m.files.ReadFile(ctx, ref.ResourceID, ref.Commit, ref.Path); err != nil {
				return storage.CollaborationTask{}, problem("invalid_reference", "The referenced immutable file is unavailable.")
			}
		}
	}
	revision, model := m.models.Snapshot()
	t, err := m.store.CreateTask(ctx, room, in, storage.Budget{RequireRunner: true, RuntimeKey: revision, Model: model.Model, MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: model.TimeoutSeconds})
	if err == nil {
		m.Wake()
	}
	return t, err
}
func (m *Manager) OperateTask(ctx context.Context, id, action string, in storage.TaskOperation) (storage.CollaborationTask, error) {
	t, err := m.store.OperateTask(ctx, id, action, in)
	if err == nil {
		if action == "cancel" && t.ActiveAttemptID != "" {
			m.stopProxy(t.ActiveAttemptID)
		}
		m.Wake()
	}
	return t, err
}

func (m *Manager) taskDraftEvidence(ctx context.Context, run storage.Run, directory string) error {
	names, err := m.files.ListFiles(ctx, run.ResourceID, run.PreviousDraftCommit)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range names {
		data, err := m.files.ReadFile(ctx, run.ResourceID, run.PreviousDraftCommit, name)
		if err != nil {
			return err
		}
		target := filepath.Join("previous-task-draft", filepath.FromSlash(name))
		if err = root.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		file, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
