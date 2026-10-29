package collaboration

import (
	"context"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (s *Service) PrepareDocument(ctx context.Context, resource, commit, path string, retry bool) (storage.Document, error) {
	if s.documents == nil {
		return storage.Document{}, &storage.Error{Code: "execution_unavailable", Message: "Document preparation is not configured."}
	}
	return s.documents.Prepare(ctx, resource, commit, path, retry)
}
