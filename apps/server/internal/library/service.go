// Package library publishes explicit human edits through the same writer lease
// and immutable result store as agent work.
package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type Writer interface {
	WithResourceWriter(context.Context, string, func() error) error
}
type ContextWriter interface {
	WithResourceLeaseWriter(context.Context, string, func(context.Context) error) error
}
type Service struct {
	store  *storage.Store
	files  *resources.Manager
	writer Writer
}

func New(store *storage.Store, files *resources.Manager, writer Writer) *Service {
	return &Service{store, files, writer}
}

type Edit struct {
	RequestID      string `json:"requestId"`
	ExpectedCommit string `json:"expectedCommit"`
	Path           string `json:"path"`
	Content        string `json:"content"`
}
type Restore struct {
	RequestID      string `json:"requestId"`
	ExpectedCommit string `json:"expectedCommit"`
	Commit         string `json:"commit"`
}
type Copy struct {
	RequestID        string `json:"requestId"`
	ExpectedCommit   string `json:"expectedCommit"`
	Path             string `json:"path"`
	SourceResourceID string `json:"sourceResourceId"`
	SourceCommit     string `json:"sourceCommit"`
	SourcePath       string `json:"sourcePath"`
}
type Result struct {
	Resource    storage.Resource    `json:"resource"`
	Publication storage.Publication `json:"publication"`
}

func invalid(message string) error { return &storage.Error{Code: "invalid_resource", Message: message} }
func digest(kind string, input any) string {
	data, _ := json.Marshal(input)
	sum := sha256.Sum256(append([]byte(kind+":"), data...))
	return hex.EncodeToString(sum[:])
}

// Patch only compares revisions retained by this resource's durable history.
func (s *Service) Patch(ctx context.Context, id, from, to string) ([]byte, error) {
	if from == "" || to == "" {
		return nil, invalid("Choose two exact resource revisions to compare.")
	}
	for _, commit := range []string{from, to} {
		if _, err := s.store.ValidateResourceRevision(ctx, id, commit); err != nil {
			return nil, err
		}
	}
	return s.files.ExportPatch(ctx, id, from, to)
}

func (s *Service) Edit(ctx context.Context, id string, in Edit) (Result, error) {
	if !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0) || len(in.Content) > 1<<20 {
		return Result{}, invalid("The editor supports UTF-8 text up to 1 MiB.")
	}
	if err := resources.ValidateFilePath(in.Path); err != nil {
		return Result{}, invalid(err.Error())
	}
	return s.publish(ctx, id, in.RequestID, in.ExpectedCommit, "edit", in.Path, digest("edit", in), func() (string, error) {
		return s.files.WriteFile(ctx, id, in.ExpectedCommit, in.Path, []byte(in.Content))
	})
}
func (s *Service) Restore(ctx context.Context, id string, in Restore) (Result, error) {
	return s.publish(ctx, id, in.RequestID, in.ExpectedCommit, "restore", "", digest("restore", in), func() (string, error) {
		if in.Commit == "" {
			return "", invalid("Choose an exact revision to restore.")
		}
		if _, err := s.store.ValidateResourceRevision(ctx, id, in.Commit); err != nil {
			return "", err
		}
		return s.files.Restore(ctx, id, in.ExpectedCommit, in.Commit)
	})
}
func (s *Service) Copy(ctx context.Context, id string, in Copy) (Result, error) {
	if err := resources.ValidateFilePath(in.Path); err != nil {
		return Result{}, invalid(err.Error())
	}
	return s.publish(ctx, id, in.RequestID, in.ExpectedCommit, "edit", in.Path, digest("copy", in), func() (string, error) {
		if in.SourceCommit == "" {
			return "", invalid("Choose an exact source revision.")
		}
		if _, err := s.store.ValidateResourceRevision(ctx, in.SourceResourceID, in.SourceCommit); err != nil {
			return "", err
		}
		content, err := s.files.ReadFile(ctx, in.SourceResourceID, in.SourceCommit, in.SourcePath)
		if err != nil {
			return "", err
		}
		return s.files.WriteFile(ctx, id, in.ExpectedCommit, in.Path, content)
	})
}
func (s *Service) publish(ctx context.Context, id, request, expected, kind, path, hash string, save func() (string, error)) (Result, error) {
	if err := storage.ValidateUploadRequestID(request); err != nil {
		return Result{}, err
	}
	if expected == "" {
		return Result{}, invalid("Include the file version you edited.")
	}
	// Receipt recovery is read-only and must still work while a later Agent owns
	// the resource. Repeat the lookup inside the lease for competing first saves.
	old, found, err := s.store.FindPublication(ctx, id, request, hash)
	if err != nil {
		return Result{}, err
	}
	if found {
		resource, err := s.store.GetResource(ctx, id)
		resource.CurrentCommit, resource.UpdatedAt = old.Commit, old.CreatedAt
		return Result{resource, old}, err
	}
	var result Result
	apply := func(ctx context.Context) error {
		old, found, err := s.store.FindPublication(ctx, id, request, hash)
		if err != nil {
			return err
		}
		resource, err := s.store.WorkingResource(ctx, id)
		if err != nil {
			return err
		}
		if found {
			resource.CurrentCommit, resource.UpdatedAt = old.Commit, old.CreatedAt
			result = Result{resource, old}
			return nil
		}
		if resource.CurrentCommit != expected {
			return &storage.Error{Code: "revision_conflict", Message: "Room files changed. Compare the latest version with your draft before saving."}
		}
		commit, err := save()
		if err != nil {
			return err
		}
		publication, err := s.store.Publish(ctx, storage.PublishInput{ResourceID: id, ExpectedCommit: expected, Commit: commit, Kind: kind, Path: path, RequestID: request, RequestHash: hash})
		if err != nil {
			return err
		}
		resource.CurrentCommit = publication.Commit
		resource.UpdatedAt = publication.CreatedAt
		result = Result{resource, publication}
		return nil
	}
	if writer, ok := s.writer.(ContextWriter); ok {
		err = writer.WithResourceLeaseWriter(ctx, id, apply)
	} else {
		err = s.writer.WithResourceWriter(ctx, id, func() error { return apply(ctx) })
	}
	return result, err
}
