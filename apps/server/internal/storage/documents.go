package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type DocumentSection struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Text  string `json:"text"`
}
type DocumentError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type DocumentSummary struct {
	ID           string         `json:"id"`
	Format       string         `json:"format"`
	Status       string         `json:"status"`
	SectionCount int            `json:"sectionCount"`
	Warnings     []string       `json:"warnings"`
	Error        *DocumentError `json:"error,omitempty"`
}
type Document struct {
	ID               string            `json:"id"`
	ResourceID       string            `json:"resourceId"`
	Commit           string            `json:"commit"`
	Path             string            `json:"path"`
	Format           string            `json:"format"`
	Status           string            `json:"status"`
	ExtractorVersion string            `json:"extractorVersion"`
	Sections         []DocumentSection `json:"sections"`
	Warnings         []string          `json:"warnings"`
	Error            *DocumentError    `json:"error,omitempty"`
	CreatedAt        string            `json:"createdAt"`
	UpdatedAt        string            `json:"updatedAt"`
}

func (d Document) Summary() *DocumentSummary {
	return &DocumentSummary{ID: d.ID, Format: d.Format, Status: d.Status, SectionCount: len(d.Sections), Warnings: d.Warnings, Error: d.Error}
}
func (d Document) Terminal() bool {
	return d.Status == "ready" || d.Status == "unreadable" || d.Status == "failed" || d.Status == "unavailable"
}
func DocumentFormat(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "pdf"
	case ".docx":
		return "docx"
	}
	return ""
}
func documentRow(snapshot string) (Document, error) {
	var d Document
	err := json.Unmarshal([]byte(snapshot), &d)
	return d, err
}
func (s *Store) GetDocument(ctx context.Context, id string) (Document, error) {
	data, err := s.queries().GetDocument(ctx, id)
	if err != nil {
		return Document{}, missing(err, "The prepared document")
	}
	return documentRow(data)
}
func latestDocument(ctx context.Context, q *sqlgen.Queries, resource, commit, path string) (*Document, error) {
	data, err := q.LatestDocument(ctx, sqlgen.LatestDocumentParams{ResourceID: resource, CommitID: commit, Path: path})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := documentRow(data)
	return &d, err
}
func (s *Store) LatestDocument(ctx context.Context, resource, commit, path string) (*Document, error) {
	if !validReferencePath(path) || len(commit) != 40 || !commitValid(commit) {
		return nil, problem("invalid_reference", "Select an exact file version and a safe relative path.")
	}
	if _, err := s.ValidateResourceRevision(ctx, resource, commit); err != nil {
		return nil, err
	}
	return latestDocument(ctx, s.queries(), resource, commit, path)
}
func documentEvent(ctx context.Context, q *sqlgen.Queries, d Document) error {
	rooms, err := q.DocumentRooms(ctx, d.ResourceID)
	if err != nil {
		return err
	}
	for _, room := range rooms {
		if err := event(ctx, q, "document.updated", room, d.ID); err != nil {
			return err
		}
	}
	return nil
}

// EnsureDocument converges concurrent preparation requests on one versioned job.
// Retry appends a new artifact; existing message snapshots keep their old IDs.
func (s *Store) EnsureDocument(ctx context.Context, resource, commit, path, version string, retry bool) (Document, error) {
	var saved Document
	if !validReferencePath(path) || len(commit) != 40 || !commitValid(commit) || DocumentFormat(path) == "" || !validText(version, 128, false) {
		return saved, problem("invalid_document", "Choose a PDF or DOCX file at an exact version.")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if _, err := validateResourceRevision(ctx, q, resource, commit); err != nil {
			return err
		}
		data, err := q.LatestDocumentVersion(ctx, sqlgen.LatestDocumentVersionParams{ResourceID: resource, CommitID: commit, Path: path, ExtractorVersion: version})
		if err == nil {
			saved, err = documentRow(data)
			if err != nil {
				return err
			}
			if !retry || !saved.Terminal() {
				return nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		stamp := now()
		saved = Document{ID: newID(), ResourceID: resource, Commit: commit, Path: path, Format: DocumentFormat(path), Status: "queued", ExtractorVersion: version, Sections: []DocumentSection{}, Warnings: []string{}, CreatedAt: stamp, UpdatedAt: stamp}
		encoded, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		if err = q.CreateDocument(ctx, sqlgen.CreateDocumentParams{ID: saved.ID, ResourceID: resource, CommitID: commit, Path: path, ExtractorVersion: version, Status: saved.Status, Snapshot: string(encoded), CreatedAt: stamp, UpdatedAt: stamp}); err != nil {
			return err
		}
		return documentEvent(ctx, q, saved)
	})
	return saved, err
}
func (s *Store) PendingDocuments(ctx context.Context) ([]Document, error) {
	rows, err := s.queries().PendingDocuments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Document, 0, len(rows))
	for _, row := range rows {
		d, err := documentRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}
func validateDocumentResult(d Document) error {
	if !d.Terminal() {
		return problem("invalid_document", "A document result must have a terminal status.")
	}
	if d.Status != "ready" {
		if len(d.Sections) > 0 || d.Error == nil {
			return problem("invalid_document", "An unreadable document cannot contain prepared text.")
		}
	} else if len(d.Sections) == 0 || len(d.Sections) > 2000 || d.Error != nil {
		return problem("invalid_document", "The prepared text result is invalid.")
	}
	total := 0
	for i, section := range d.Sections {
		label := "Paragraph"
		if d.Format == "pdf" {
			label = "Page"
		}
		if section.ID != fmt.Sprintf("p%d", i+1) || section.Label != fmt.Sprintf("%s %d", label, i+1) || !utf8.ValidString(section.Text) {
			return problem("invalid_document", "The source locations are invalid.")
		}
		total += len(section.Text)
	}
	if d.Status == "ready" && total == 0 || total > 1<<20 || d.Format == "pdf" && len(d.Sections) > 200 || len(d.Warnings) > 32 {
		return problem("invalid_document", "The prepared text exceeds its limit.")
	}
	for _, warning := range d.Warnings {
		if !validText(warning, 2048, false) {
			return problem("invalid_document", "A document warning is invalid.")
		}
	}
	if d.Error != nil && (!validText(d.Error.Code, 64, false) || !validText(d.Error.Message, 2048, false)) {
		return problem("invalid_document", "The document error is invalid.")
	}
	return nil
}

// ChangeDocument owns the queue state machine. Terminal snapshots are immutable.
func (s *Store) ChangeDocument(ctx context.Context, id, status string, result *Document) (Document, error) {
	var saved Document
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		data, err := q.GetDocument(ctx, id)
		if err != nil {
			return missing(err, "The prepared document")
		}
		saved, err = documentRow(data)
		if err != nil {
			return err
		}
		previous := saved.Status
		if saved.Terminal() {
			return problem("document_finished", "This prepared document is immutable. Retry to create a new attempt.")
		}
		switch status {
		case "extracting":
			if previous != "queued" {
				return problem("document_busy", "The document is already being prepared.")
			}
		case "queued":
			if previous != "extracting" {
				return nil
			}
		default:
			if result == nil || previous != "extracting" {
				return problem("invalid_document", "Only active preparation can publish a result.")
			}
			saved.Status = status
			saved.Sections = result.Sections
			saved.Warnings = result.Warnings
			if saved.Sections == nil {
				saved.Sections = []DocumentSection{}
			}
			if saved.Warnings == nil {
				saved.Warnings = []string{}
			}
			saved.Error = result.Error
			if err := validateDocumentResult(saved); err != nil {
				return err
			}
		}
		saved.Status = status
		saved.UpdatedAt = now()
		encoded, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		count, err := q.UpdateDocument(ctx, sqlgen.UpdateDocumentParams{ID: id, Status: status, Snapshot: string(encoded), UpdatedAt: saved.UpdatedAt, PreviousStatus: previous})
		if err != nil {
			return err
		}
		if count != 1 {
			return problem("document_busy", "The document preparation state changed.")
		}
		return documentEvent(ctx, q, saved)
	})
	return saved, err
}
func snapshotDocument(ctx context.Context, q *sqlgen.Queries, ref ReferenceInput, needsAgent bool) (*DocumentSummary, error) {
	var d *Document
	if ref.DocumentID != "" {
		data, err := q.GetDocument(ctx, ref.DocumentID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, problem("invalid_reference", "The prepared document does not exist.")
		} else if err != nil {
			return nil, err
		}
		parsed, err := documentRow(data)
		if err != nil {
			return nil, err
		}
		d = &parsed
		if d.ResourceID != ref.ResourceID || d.Commit != ref.Commit || d.Path != ref.Path {
			return nil, problem("invalid_reference", "The prepared document belongs to a different file version.")
		}
		if !d.Terminal() {
			return nil, problem("document_preparing", "The document is still being prepared. Wait, then send again.")
		}
	} else {
		var err error
		d, err = latestDocument(ctx, q, ref.ResourceID, ref.Commit, ref.Path)
		if err != nil {
			return nil, err
		}
	}
	if d == nil || !d.Terminal() {
		if needsAgent && DocumentFormat(ref.Path) != "" {
			return nil, problem("document_preparing", "The document is being prepared for reading. Wait, then send again.")
		}
		return nil, nil
	}
	return d.Summary(), nil
}
