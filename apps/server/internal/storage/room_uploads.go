package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// UploadReceipt records ownership and exact retry identity independently of a
// message draft. Removing a draft reference never removes saved evidence.
type UploadReceipt struct {
	Reference Reference
	SHA256    string
}

func uploadReference(row sqlgen.RoomUpload, commit string) Reference {
	size := int(row.Size)
	return Reference{UploadedByUserID: row.UploadedByUserID, Kind: "file", ResourceID: row.ResourceID, Commit: commit, Path: row.Name, Title: row.Name, Size: &size, Origin: "upload", CreatedAt: row.CreatedAt}
}

func ValidateUploadRequestID(requestID string) error {
	if !validText(requestID, 128, false) || requestID == "." || requestID == ".." || strings.ContainsFunc(requestID, unicode.IsControl) || strings.ContainsAny(requestID, "/\\") {
		return problem("invalid_upload", "Use a nonempty upload request ID of at most 128 UTF-8 bytes without paths or control characters.")
	}
	return nil
}

func (s *Store) GetRoomUpload(ctx context.Context, room, requestID string) (UploadReceipt, error) {
	if err := ValidateUploadRequestID(requestID); err != nil {
		return UploadReceipt{}, err
	}
	u, err := s.queries().GetRoomUpload(ctx, sqlgen.GetRoomUploadParams{RoomID: room, UploadedByUserID: actorID(ctx), RequestID: requestID})
	if err != nil {
		return UploadReceipt{}, missing(err, "The attachment")
	}
	r, err := s.queries().GetResource(ctx, u.ResourceID)
	if err != nil {
		return UploadReceipt{}, err
	}
	ref := uploadReference(u, r.CurrentCommit)
	if d, err := latestDocument(ctx, s.queries(), ref.ResourceID, ref.Commit, ref.Path); err != nil {
		return UploadReceipt{}, err
	} else if d != nil {
		ref.Document = d.Summary()
	}
	return UploadReceipt{Reference: ref, SHA256: u.Sha256}, nil
}

func (s *Store) ListRoomUploads(ctx context.Context, room, before string) ([]Reference, string, error) {
	if _, err := s.GetRoom(ctx, room); err != nil {
		return nil, "", err
	}
	var created string
	if before != "" {
		cursor, err := s.queries().GetResourceUpload(ctx, before)
		if errors.Is(err, sql.ErrNoRows) || err == nil && cursor.RoomID != room {
			return nil, "", problem("invalid_upload", "The attachment cursor must belong to this room.")
		}
		if err != nil {
			return nil, "", err
		}
		created = cursor.CreatedAt
	}
	rows, err := s.queries().ListRoomUploads(ctx, sqlgen.ListRoomUploadsParams{RoomID: room, BeforeResource: before, BeforeCreated: created})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > 200 {
		rows = rows[:200]
		next = rows[len(rows)-1].ResourceID
	}
	out := make([]Reference, 0, len(rows))
	for _, row := range rows {
		r, err := s.queries().GetResource(ctx, row.ResourceID)
		if err != nil {
			return nil, "", err
		}
		ref := uploadReference(row, r.CurrentCommit)
		if d, err := latestDocument(ctx, s.queries(), ref.ResourceID, ref.Commit, ref.Path); err != nil {
			return nil, "", err
		} else if d != nil {
			ref.Document = d.Summary()
		}
		out = append(out, ref)
	}
	return out, next, nil
}

// RegisterRoomUpload commits the resource, receipt, and event together. The
// caller has already durably saved the immutable objects and owns compensation.
// Concurrent exact retries converge on the first committed receipt.
func (s *Store) RegisterRoomUpload(ctx context.Context, room, requestID, resourceID, commit, name string, size int, digest string) (UploadReceipt, error) {
	if err := ValidateUploadRequestID(requestID); err != nil {
		return UploadReceipt{}, err
	}
	decoded, err := hex.DecodeString(digest)
	if !validText(name, 255, false) || !validReferencePath(name) || strings.Contains(name, "/") || len(resourceID) != 32 || len(commit) != 40 || !commitValid(commit) || size < 0 || size > 8<<20 || err != nil || len(decoded) != 32 || strings.ToLower(digest) != digest {
		return UploadReceipt{}, problem("invalid_upload", "The attachment metadata is invalid.")
	}
	var saved UploadReceipt
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if _, err := q.GetRoom(ctx, room); err != nil {
			return missing(err, "The room")
		}
		old, err := q.GetRoomUpload(ctx, sqlgen.GetRoomUploadParams{RoomID: room, UploadedByUserID: actorID(ctx), RequestID: requestID})
		if err == nil {
			if old.Name != name || old.Size != int64(size) || old.Sha256 != digest {
				return problem("request_conflict", "This upload request ID already belongs to a different file. Choose a new request ID.")
			}
			r, err := q.GetResource(ctx, old.ResourceID)
			saved = UploadReceipt{Reference: uploadReference(old, r.CurrentCommit), SHA256: old.Sha256}
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		createdAt := now()
		resource := Resource{ID: resourceID, Name: "Uploaded file", CurrentCommit: commit, Environment: json.RawMessage(`{}`), CreatedAt: createdAt, UpdatedAt: createdAt}
		if err := createResource(ctx, q, resource); err != nil {
			return err
		}
		row := sqlgen.RoomUpload{UploadedByUserID: actorID(ctx), RoomID: room, RequestID: requestID, ResourceID: resourceID, Name: name, Size: int64(size), Sha256: digest, CreatedAt: createdAt}
		if err := q.CreateRoomUpload(ctx, sqlgen.CreateRoomUploadParams(row)); err != nil {
			return err
		}
		saved = UploadReceipt{Reference: uploadReference(row, commit), SHA256: digest}
		return event(ctx, q, "upload.created", room, resourceID)
	})
	return saved, err
}

func requireWorkingResource(ctx context.Context, q *sqlgen.Queries, resourceID string) error {
	_, err := q.GetResourceUpload(ctx, resourceID)
	if err == nil {
		return problem("invalid_resource", "Uploaded attachments are read-only evidence, not room working files.")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
