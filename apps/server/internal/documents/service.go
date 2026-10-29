// Package documents owns durable preparation of immutable file evidence. Its
// bounded worker is separate from model execution and can safely resume a queue.
package documents

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/documenttext"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type Extractor interface {
	Extract(context.Context, string, []byte) (documenttext.Result, error)
}
type Service struct {
	store     *storage.Store
	files     *resources.Manager
	extractor Extractor
	logger    *slog.Logger
	cancel    context.CancelFunc
	ctx       context.Context
	wake      chan struct{}
	done      chan struct{}
	once      sync.Once
}

func New(ctx context.Context, store *storage.Store, files *resources.Manager, extractor Extractor, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	worker, cancel := context.WithCancel(context.Background())
	s := &Service{store: store, files: files, extractor: extractor, logger: logger, ctx: worker, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	pending, err := store.PendingDocuments(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, d := range pending {
		if d.Status == "extracting" {
			if _, err := store.ChangeDocument(ctx, d.ID, "queued", nil); err != nil {
				cancel()
				return nil, err
			}
		}
	}
	go s.work()
	s.Wake()
	return s, nil
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Shutdown(ctx context.Context) error {
	s.once.Do(s.cancel)
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Prepare(ctx context.Context, resource, commit, path string, retry bool) (storage.Document, error) {
	if _, err := s.store.ValidateResourceRevision(ctx, resource, commit); err != nil {
		return storage.Document{}, err
	}
	if storage.DocumentFormat(path) == "" {
		return storage.Document{}, &storage.Error{Code: "invalid_document", Message: "Text preparation supports PDF and DOCX files."}
	}
	if _, err := s.files.ReadFile(ctx, resource, commit, path); err != nil {
		return storage.Document{}, &storage.Error{Code: "invalid_reference", Message: "The selected file is unavailable at this version."}
	}
	d, err := s.store.EnsureDocument(ctx, resource, commit, path, documenttext.Version, retry)
	if err == nil {
		s.Wake()
	}
	return d, err
}
func (s *Service) work() {
	defer close(s.done)
	timer := time.NewTicker(500 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
		}
		pending, err := s.store.PendingDocuments(s.ctx)
		if err != nil {
			if s.ctx.Err() == nil {
				s.logger.Error("Cannot read document preparation queue")
			}
			continue
		}
		for _, d := range pending {
			if s.ctx.Err() != nil {
				return
			}
			if d.Status != "queued" {
				continue
			}
			active, err := s.store.ChangeDocument(s.ctx, d.ID, "extracting", nil)
			if err != nil {
				continue
			}
			s.extract(active)
		}
	}
}
func (s *Service) extract(d storage.Document) {
	content, err := s.files.ReadFile(s.ctx, d.ResourceID, d.Commit, d.Path)
	if d.ExtractorVersion != documenttext.Version {
		err = &documenttext.Failure{Code: "unavailable", Message: "The document reader version changed before this preparation completed. Retry to prepare with the current reader.", Retryable: true}
	}
	var parsed documenttext.Result
	if err == nil {
		if s.extractor == nil {
			err = &documenttext.Failure{Code: "unavailable", Message: "Document text preparation is not configured on this server.", Retryable: true}
		} else {
			parsed, err = s.extractor.Extract(s.ctx, d.Path, content)
		}
	}
	if s.ctx.Err() != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := s.store.ChangeDocument(cleanup, d.ID, "queued", nil); err != nil {
			s.logger.Error("Cannot requeue interrupted document preparation")
		}
		return
	}
	result := storage.Document{Sections: []storage.DocumentSection{}, Warnings: []string{}}
	status := "ready"
	if err != nil {
		var failure *documenttext.Failure
		if !errors.As(err, &failure) {
			failure = &documenttext.Failure{Code: "reader_failed", Message: "The document could not be prepared. The original file is still available.", Retryable: true}
		}
		status = "failed"
		switch failure.Code {
		case "unavailable":
			status = "unavailable"
		case "malformed", "encrypted", "no_text", "limit_exceeded", "input_limit", "unsupported_format":
			status = "unreadable"
		}
		result.Error = &storage.DocumentError{Code: failure.Code, Message: failure.Message, Retryable: failure.Retryable}
	} else {
		result.Warnings = parsed.Warnings
		if result.Warnings == nil {
			result.Warnings = []string{}
		}
		for _, section := range parsed.Sections {
			result.Sections = append(result.Sections, storage.DocumentSection{ID: section.ID, Label: section.Label, Text: section.Text})
		}
	}
	// Once parsing has finished, retry only the same immutable database write.
	// A storage failure must not repeatedly start an expensive reader process.
	for s.ctx.Err() == nil {
		_, saveErr := s.store.ChangeDocument(s.ctx, d.ID, status, &result)
		if saveErr == nil {
			return
		}
		var problem *storage.Error
		if errors.As(saveErr, &problem) && problem.Code == "document_finished" {
			return
		}
		if errors.As(saveErr, &problem) && problem.Code == "invalid_document" {
			status = "failed"
			result = storage.Document{Sections: []storage.DocumentSection{}, Warnings: []string{}, Error: &storage.DocumentError{Code: "invalid_result", Message: "The document reader returned invalid text or source locations.", Retryable: true}}
		}
		s.logger.Error("Cannot save document preparation result")
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
