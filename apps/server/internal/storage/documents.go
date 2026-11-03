package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

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
	d, err := documentRow(data)
	return documentReadView(d), err
}
func latestDocument(ctx context.Context, q *sqlgen.Queries, resource, commit, path string) (*Document, error) {
	data, err := q.LatestReadyDocument(ctx, sqlgen.LatestReadyDocumentParams{ResourceID: resource, CommitID: commit, Path: path})
	if errors.Is(err, sql.ErrNoRows) {
		data, err = q.LatestDocument(ctx, sqlgen.LatestDocumentParams{ResourceID: resource, CommitID: commit, Path: path})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := documentRow(data)
	d = documentReadView(d)
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

const DocumentPreparationDisabledCode = "document_preparation_disabled"
const DocumentPreparationDisabledMessage = "PDF and DOCX text preparation is disabled. Use Markdown or plain text; the original file is unchanged."

// Historical rows remain untouched. Pending work is never restarted, and read
// views must not advertise a retry operation that this Server no longer owns.
func documentReadView(d Document) Document {
	if d.Status == "ready" {
		return d
	}
	if d.Status == "queued" || d.Status == "extracting" || d.Error == nil {
		d.Status = "unavailable"
		d.Sections = []DocumentSection{}
		d.Error = &DocumentError{Code: DocumentPreparationDisabledCode, Message: DocumentPreparationDisabledMessage, Retryable: false}
	} else {
		failure := *d.Error
		failure.Retryable = false
		d.Error = &failure
	}
	return d
}

func snapshotDocument(ctx context.Context, q *sqlgen.Queries, ref ReferenceInput, _ bool) (*DocumentSummary, error) {
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
	} else {
		var err error
		d, err = latestDocument(ctx, q, ref.ResourceID, ref.Commit, ref.Path)
		if err != nil {
			return nil, err
		}
	}
	if d == nil {
		return nil, nil
	}
	view := documentReadView(*d)
	return view.Summary(), nil
}
