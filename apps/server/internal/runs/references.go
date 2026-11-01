package runs

import (
	"context"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func hasFileReferences(references []storage.ReferenceInput) bool {
	for _, reference := range references {
		if reference.Kind == "file" {
			return true
		}
	}
	return false
}

func (m *Manager) validateReferenceFiles(ctx context.Context, room string, references []storage.ReferenceInput) error {
	for _, reference := range references {
		if reference.Kind != "file" {
			continue
		}
		if _, err := m.store.ValidateFileReference(ctx, room, reference); err != nil {
			return err
		}
		if _, err := m.resources.ReadFile(ctx, reference.ResourceID, reference.Commit, reference.Path); err != nil {
			return &storage.Error{Code: "invalid_reference", Message: "The referenced file is unavailable at the selected version. Refresh Files and select the reference again."}
		}
	}
	return nil
}
