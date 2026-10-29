package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func validateReferenceInput(ref ReferenceInput) error {
	switch ref.Kind {
	case "message":
		if !validText(ref.MessageID, 128, false) || ref.ResourceID != "" || ref.Commit != "" || ref.Path != "" || ref.DocumentID != "" {
			return problem("invalid_reference", "A message reference must identify only a message.")
		}
	case "file":
		if ref.DocumentID != "" && !validText(ref.DocumentID, 128, false) || ref.MessageID != "" || !validText(ref.ResourceID, 128, false) || len(ref.Commit) != 40 || !commitValid(ref.Commit) || !validReferencePath(ref.Path) {
			return problem("invalid_reference", "A file reference needs a resource, an exact 40-character revision, and a safe relative file path.")
		}
	default:
		return problem("invalid_reference", "Use a message or file reference.")
	}
	return nil
}

func validReferencePath(value string) bool {
	if !validText(value, 4096, false) || value == "." || value == ".." || path.IsAbs(value) || path.Clean(value) != value || strings.HasPrefix(value, "../") || strings.Contains(value, "\\") {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func validateReferencesInput(refs []ReferenceInput) error {
	if len(refs) > MaxReferences {
		return problem("invalid_reference", "Attach at most eight references.")
	}
	seen := make(map[ReferenceInput]bool, len(refs))
	for _, ref := range refs {
		if err := validateReferenceInput(ref); err != nil {
			return err
		}
		identity := ref
		identity.DocumentID = ""
		if seen[identity] {
			return problem("invalid_reference", "The same reference cannot be attached twice.")
		}
		seen[identity] = true
	}
	return nil
}

func validateResourceRevision(ctx context.Context, q *sqlgen.Queries, resourceID, commit string) (Resource, error) {
	if commit != "" && (len(commit) != 40 || !commitValid(commit)) {
		return Resource{}, problem("invalid_reference", "Use an exact 40-character resource revision.")
	}
	r, err := q.GetResource(ctx, resourceID)
	if err != nil {
		return Resource{}, missing(err, "The resource")
	}
	if commit == "" || commit == r.CurrentCommit {
		return resourceRow(r), nil
	}
	known, err := q.ResourceRevisionKnown(ctx, sqlgen.ResourceRevisionKnownParams{ResourceID: resourceID, CommitID: commit})
	if err != nil {
		return Resource{}, err
	}
	if !known.Valid || !known.Bool {
		return Resource{}, problem("invalid_reference", "This revision is not retained by the resource or its runs.")
	}
	return resourceRow(r), nil
}

// ValidateResourceRevision rejects arbitrary Git expressions before file access.
// An omitted revision selects the current pointer returned with the resource.
func (s *Store) ValidateResourceRevision(ctx context.Context, resourceID, commit string) (Resource, error) {
	return validateResourceRevision(ctx, s.queries(), resourceID, commit)
}

func validateFileReference(ctx context.Context, q *sqlgen.Queries, room string, ref ReferenceInput) (Resource, error) {
	if err := validateReferenceInput(ref); err != nil {
		return Resource{}, err
	}
	if ref.Kind != "file" {
		return Resource{}, problem("invalid_reference", "This is not a file reference.")
	}
	return validateResourceRevision(ctx, q, ref.ResourceID, ref.Commit)
}

// ValidateFileReference is the admission preflight boundary for immutable bytes.
// SendMessage repeats these checks inside its message-and-run transaction.
func (s *Store) ValidateFileReference(ctx context.Context, room string, ref ReferenceInput) (Resource, error) {
	return validateFileReference(ctx, s.queries(), room, ref)
}

func truncateReferenceText(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end], true
}

func referenceRunMetadata(ref *Reference, run Run) {
	ref.SourceRunID = run.ID
	ref.SourceStatus = run.Status
	ref.ResultCommit = run.ResultCommit
	published := run.Published
	ref.Published = &published
	var checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(run.Checks, &checks); err != nil {
		ref.Checks = "Unavailable"
		return
	}
	if len(checks) == 0 {
		var environment struct {
			Checks []json.RawMessage `json:"checks"`
		}
		if json.Unmarshal(run.Environment, &environment) == nil && len(environment.Checks) > 0 {
			ref.Checks = "Not run"
		} else {
			ref.Checks = "Not configured"
		}
		return
	}
	parts := make([]string, 0, len(checks))
	for _, check := range checks {
		parts = append(parts, check.Name+": "+check.Status)
	}
	ref.Checks, _ = truncateReferenceText(strings.Join(parts, "; "), 2048)
}

func snapshotReferences(ctx context.Context, q *sqlgen.Queries, room string, inputs []ReferenceInput, needsAgent bool) ([]Reference, error) {
	refs := make([]Reference, 0, len(inputs))
	textBytes := 0
	for _, in := range inputs {
		ref := Reference{Kind: in.Kind, MessageID: in.MessageID, ResourceID: in.ResourceID, Commit: in.Commit, Path: in.Path}
		if in.Kind == "message" {
			m, err := q.GetMessage(ctx, in.MessageID)
			if errors.Is(err, sql.ErrNoRows) || err == nil && m.RoomID != room {
				return nil, problem("invalid_reference", "The referenced message must exist in this room.")
			}
			if err != nil {
				return nil, err
			}
			ref.Content, ref.Truncated = truncateReferenceText(m.Content, MaxReferenceTextSize)
			textBytes += len(ref.Content)
			if textBytes > MaxReferencesTextSize {
				return nil, problem("invalid_reference", "Referenced message excerpts exceed 64 KiB. Attach fewer messages.")
			}
			ref.Title, _ = truncateReferenceText(strings.SplitN(strings.TrimSpace(m.Content), "\n", 2)[0], 160)
			ref.CreatedAt = m.CreatedAt
			ref.ThreadRootID = m.ThreadRootID.String
			switch m.AuthorKind {
			case "user":
				ref.Author = m.AuthorName
				if ref.Author == "" {
					ref.Author = "Team member"
				}
			case "system":
				ref.Author = "Camp"
			case "agent":
				a, err := q.GetAgent(ctx, m.AgentID.String)
				if err != nil {
					return nil, err
				}
				ref.Author = a.Name
			}
			if m.RunID != "" {
				run, err := getRun(ctx, q, m.RunID)
				if err != nil {
					return nil, err
				}
				referenceRunMetadata(&ref, run)
				if m.AuthorKind == "agent" {
					ref.Author = run.AgentSnapshot.Name
				}
			}
		} else {
			resource, err := validateFileReference(ctx, q, room, in)
			if err != nil {
				return nil, err
			}
			ref.Title = resource.Name + " / " + in.Path
			upload, uploadErr := q.GetResourceUpload(ctx, in.ResourceID)
			if uploadErr == nil {
				if upload.Name != in.Path {
					return nil, problem("invalid_reference", "The attachment path does not match the saved file.")
				}
				ref = uploadReference(upload, in.Commit)
			} else if !errors.Is(uploadErr, sql.ErrNoRows) {
				return nil, uploadErr
			}
			snapshot, err := q.ResourceResultRun(ctx, sqlgen.ResourceResultRunParams{RoomID: room, ResourceID: in.ResourceID, CommitID: in.Commit})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				run, err := runFromSnapshot(snapshot)
				if err != nil {
					return nil, err
				}
				referenceRunMetadata(&ref, run)
			}
			ref.Document, err = snapshotDocument(ctx, q, in, needsAgent)
			if err != nil {
				return nil, err
			}
		}
		refs = append(refs, ref)
	}
	return refs, nil
}
