package collaboration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (s *Service) Upload(ctx context.Context, room, requestID, name string, content []byte) (storage.Reference, error) {
	if err := storage.ValidateUploadRequestID(requestID); err != nil {
		return storage.Reference{}, err
	}
	if err := resources.ValidateUploadName(name); err != nil {
		return storage.Reference{}, err
	}
	if len(content) > resources.MaxFileBytes {
		return storage.Reference{}, &storage.Error{Code: "file_too_large", Message: "Attachments must be no larger than 8 MiB."}
	}
	if _, err := s.store.GetRoom(ctx, room); err != nil {
		return storage.Reference{}, err
	}
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	previous, err := s.store.GetRoomUpload(ctx, room, requestID)
	if err == nil {
		if previous.SHA256 != digest || previous.Reference.Path != name || previous.Reference.Size == nil || *previous.Reference.Size != len(content) {
			return storage.Reference{}, &storage.Error{Code: "request_conflict", Message: "This upload request ID already belongs to a different file. Choose a new request ID."}
		}
		return s.prepareUpload(ctx, previous.Reference)
	}
	var failure *storage.Error
	if !errors.As(err, &failure) || failure.Code != "not_found" {
		return storage.Reference{}, err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return storage.Reference{}, &storage.Error{Code: "execution_unavailable", Message: "Uploading files requires Git on the Camp server."}
	}
	id := newID()
	result, err := s.files.Upload(ctx, id, name, content)
	if err != nil {
		return storage.Reference{}, err
	}
	saved, err := s.store.RegisterRoomUpload(ctx, room, requestID, id, result.Commit, name, len(content), digest)
	if err != nil || saved.Reference.ResourceID != id {
		// A failed or cancelled request may race a committed retry. Only discard
		// our candidate after confirming it is not registered in the database.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, lookupErr := s.store.GetResource(cleanup, id)
		if errors.As(lookupErr, &failure) && failure.Code == "not_found" {
			if cleanupErr := s.files.DiscardImported(id); cleanupErr != nil {
				return storage.Reference{}, errors.Join(err, cleanupErr)
			}
		}
	}
	if err != nil {
		return storage.Reference{}, err
	}
	return s.prepareUpload(ctx, saved.Reference)
}

// Saving the receipt precedes preparation. A retry can repair a lost enqueue
// without creating another attachment or changing its immutable original.
func (s *Service) prepareUpload(ctx context.Context, ref storage.Reference) (storage.Reference, error) {
	if s.documents == nil || storage.DocumentFormat(ref.Path) == "" {
		return ref, nil
	}
	d, err := s.documents.Prepare(ctx, ref.ResourceID, ref.Commit, ref.Path, false)
	if err != nil {
		return ref, err
	}
	ref.Document = d.Summary()
	return ref, nil
}
