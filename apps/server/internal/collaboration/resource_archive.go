package collaboration

import (
	"bytes"
	"context"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// ResourceArchive requires an explicit retained revision. Constructing the whole
// bounded archive before HTTP headers keeps failures from looking like downloads.
func (s *Service) ResourceArchive(ctx context.Context, id, commit string) ([]byte, error) {
	if commit == "" {
		return nil, &storage.Error{Code: "invalid_reference", Message: "Choose a saved resource revision to download."}
	}
	if _, err := s.store.ValidateResourceRevision(ctx, id, commit); err != nil {
		return nil, err
	}
	var archive bytes.Buffer
	if err := s.files.ExportZIP(ctx, id, commit, &archive); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &storage.Error{Code: "invalid_resource", Message: "These saved files could not be exported as a ZIP archive."}
	}
	return archive.Bytes(), nil
}
