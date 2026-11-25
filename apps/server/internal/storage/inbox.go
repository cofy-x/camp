package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const InboxPageLimit = 40
const inboxPageBytes = 320 << 10

type InboxItem struct {
	Title       string `json:"title"`
	Question    string `json:"question"`
	Summary     string `json:"summary"`
	WorkItemID  string `json:"workItemId,omitempty"`
	TaskID      string `json:"taskId,omitempty"`
	RoomName    string `json:"roomName"`
	ReviewTitle string `json:"reviewTitle"`
	ReviewID    string `json:"reviewId,omitempty"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	RoomID      string `json:"roomId"`
	MessageID   string `json:"messageId"`
	RunID       string `json:"runId"`
	CreatedAt   string `json:"createdAt"`
	ReadAt      string `json:"readAt"`
}

type InboxPage struct {
	Items      []InboxItem `json:"items"`
	NextCursor string      `json:"nextCursor"`
	HasMore    bool        `json:"hasMore"`
}

type inboxCursor struct {
	ActorID   string `json:"actor"`
	CreatedAt string `json:"createdAt"`
	ID        string `json:"id"`
}

func decodeInboxCursor(value, actor string) (inboxCursor, error) {
	invalid := problem("invalid_page", "Carry the original Activity cursor for this account.")
	if len(value) > 1024 {
		return inboxCursor{}, invalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != value {
		return inboxCursor{}, invalid
	}
	var cursor inboxCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.ActorID != actor || !validText(cursor.ID, 128, false) || len(cursor.CreatedAt) > 64 {
		return inboxCursor{}, invalid
	}
	canonical, _ := json.Marshal(cursor)
	if _, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt); err != nil || !bytes.Equal(raw, canonical) {
		return inboxCursor{}, invalid
	}
	return cursor, nil
}

func (s *Store) CountUnreadInbox(ctx context.Context) (count int64, err error) {
	if actorID(ctx) == "" {
		return 0, problem("unauthenticated", "Sign in to read your Activity.")
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		count, err = q.CountUnreadInbox(ctx, actorID(ctx))
		return err
	})
	return
}

func (s *Store) ListInbox(ctx context.Context) ([]InboxItem, error) {
	page, err := s.Inbox(ctx, "", InboxPageLimit)
	return page.Items, err
}

func (s *Store) Inbox(ctx context.Context, before string, limit int) (out InboxPage, err error) {
	out.Items = []InboxItem{}
	if limit < 1 || limit > InboxPageLimit {
		return out, problem("invalid_page", "Read between one and 40 Activity items at a time.")
	}
	actor := actorID(ctx)
	if actor == "" {
		return out, problem("unauthenticated", "Sign in to read your Activity.")
	}
	// A nonempty sentinel above all stored RFC3339 timestamps lets the same
	// indexed keyset query serve the initial and subsequent pages.
	cursor := inboxCursor{ActorID: actor, CreatedAt: "\uffff"}
	if before != "" {
		cursor, err = decodeInboxCursor(before, actor)
		if err != nil {
			return out, err
		}
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		rows, err := q.InboxPage(ctx, sqlgen.InboxPageParams{
			UserID: actor, BeforeCreatedAt: cursor.CreatedAt,
			BeforeID: cursor.ID, PageLimit: int64(limit + 1),
		})
		if err != nil {
			return err
		}
		// Include room for the cursor, envelope, separators, and trailing newline.
		size := 2048
		for _, row := range rows {
			item := InboxItem{
				Title: truncateUTF8(row.Title, 512), Question: truncateUTF8(row.Question, 2048),
				Summary: truncateUTF8(row.Summary, 2048), WorkItemID: row.ResolvedWorkItemID,
				TaskID: row.TaskID, RoomName: row.RoomName, ReviewTitle: row.ReviewTitle,
				ReviewID: row.ReviewID, ID: row.ID, Kind: row.Kind, RoomID: row.RoomID,
				MessageID: row.MessageID, RunID: row.RunID, CreatedAt: row.CreatedAt, ReadAt: row.ReadAt,
			}
			data, err := json.Marshal(item)
			if err != nil {
				return err
			}
			size += len(data) + 1
			if len(out.Items) == limit || size > inboxPageBytes {
				out.HasMore = true
				break
			}
			out.Items = append(out.Items, item)
			cursor.CreatedAt, cursor.ID = row.CreatedAt, row.ID
		}
		if out.HasMore {
			raw, _ := json.Marshal(cursor)
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		}
		return nil
	})
	return
}

func (s *Store) ReadInboxItem(ctx context.Context, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		readAt, err := q.GetInboxReadState(ctx, sqlgen.GetInboxReadStateParams{ID: id, UserID: actorID(ctx)})
		if err != nil {
			return missing(err, "The notification")
		}
		if readAt != "" {
			return nil
		}
		n, err := q.ReadInboxItem(ctx, sqlgen.ReadInboxItemParams{ID: id, UserID: actorID(ctx), ReadAt: now()})
		if err != nil || n == 0 {
			return err
		}
		return event(ctx, q, "personal_read.updated", "", actorID(ctx))
	})
}
