package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// Mentions require a token boundary; email addresses and longer handles do not
// address a member. Delivery is resolved against current enabled membership.
func mentionsHandle(content, handle string) bool {
	content = strings.ToLower(content)
	target := "@" + strings.ToLower(handle)
	for offset := 0; offset < len(content); {
		i := strings.Index(content[offset:], target)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(target)
		previous, _ := utf8.DecodeLastRuneInString(content[:i])
		next, _ := utf8.DecodeRuneInString(content[end:])
		before := i == 0 || !handleCharacter(previous)
		after := end == len(content) || !handleCharacter(next)
		if before && after {
			return true
		}
		offset = end
	}
	return false
}
func handleCharacter(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '@'
}
func notifyMentions(ctx context.Context, q *sqlgen.Queries, m Message) error {
	users, e := q.ListUsers(ctx)
	if e != nil {
		return e
	}
	for _, u := range users {
		if u.Enabled && u.ID != m.AuthorUserID && mentionsHandle(m.Content, u.Username) {
			if e = q.CreateInboxItem(ctx, sqlgen.CreateInboxItemParams{ID: newID(), UserID: u.ID, Kind: "mention", RoomID: m.RoomID, MessageID: m.ID, CreatedAt: now()}); e != nil {
				return e
			}
		}
	}
	return nil
}
func notifyRunFinished(ctx context.Context, q *sqlgen.Queries, r Run, m Message) error {
	if r.RequestedByUserID == "" || r.TaskID != "" {
		return nil
	}
	return q.CreateInboxItem(ctx, sqlgen.CreateInboxItemParams{ID: newID(), UserID: r.RequestedByUserID, Kind: "run_finished", RoomID: r.RoomID, MessageID: m.ID, RunID: r.ID, CreatedAt: now()})
}

type Draft struct {
	Kind      string          `json:"kind"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Revision  int64           `json:"revision"`
	UpdatedAt string          `json:"updatedAt"`
}

func validDraft(kind, key string) error {
	if (kind != "conversation" && kind != "document" && kind != "review") || !validText(key, 1200, false) {
		return problem("invalid_draft", "Choose a conversation, document, or review and a draft key of at most 1200 bytes.")
	}
	return nil
}
func (s *Store) GetDraft(ctx context.Context, kind, key string) (Draft, error) {
	if e := validDraft(kind, key); e != nil {
		return Draft{}, e
	}
	r, e := s.queries().GetDraft(ctx, sqlgen.GetDraftParams{UserID: actorID(ctx), Kind: kind, DraftKey: key})
	if errors.Is(e, sql.ErrNoRows) {
		return Draft{Kind: kind, Key: key, Value: json.RawMessage(`null`)}, nil
	}
	return Draft{r.Kind, r.DraftKey, json.RawMessage(r.Value), r.Revision, r.UpdatedAt}, e
}
func (s *Store) SaveDraft(ctx context.Context, kind, key string, value json.RawMessage, revision int64) (Draft, error) {
	if e := validDraft(kind, key); e != nil {
		return Draft{}, e
	}
	var object map[string]json.RawMessage
	limit := 256 << 10
	if kind == "document" {
		limit = 2 << 20
	}
	if len(value) > limit || json.Unmarshal(value, &object) != nil || object == nil || revision < 0 {
		return Draft{}, problem("invalid_draft", "A draft must be a JSON object within its size limit and include its current revision.")
	}
	if kind == "document" {
		var content string
		if raw, ok := object["content"]; ok && (json.Unmarshal(raw, &content) != nil || len(content) > MaxTextSize) {
			return Draft{}, problem("invalid_draft", "Document text must be at most 1 MiB.")
		}
	}
	var out Draft
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		current, e := q.GetDraft(ctx, sqlgen.GetDraftParams{UserID: actorID(ctx), Kind: kind, DraftKey: key})
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if current.Revision != revision {
			return problem("revision_conflict", "This draft changed in another window. Keep your local text and reload the saved draft.")
		}
		out = Draft{kind, key, value, revision + 1, now()}
		return q.SaveDraft(ctx, sqlgen.SaveDraftParams{UserID: actorID(ctx), Kind: kind, DraftKey: key, Value: string(value), Revision: out.Revision, UpdatedAt: out.UpdatedAt})
	})
	return out, e
}

// DeleteDraft preserves a revision tombstone so a stale window cannot recreate
// discarded text using a revision that predates its deletion.
func (s *Store) DeleteDraft(ctx context.Context, kind, key string, revision int64) (Draft, error) {
	if e := validDraft(kind, key); e != nil {
		return Draft{}, e
	}
	var out Draft
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		current, e := q.GetDraft(ctx, sqlgen.GetDraftParams{UserID: actorID(ctx), Kind: kind, DraftKey: key})
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if current.Value == "null" {
			out = Draft{kind, key, json.RawMessage(`null`), current.Revision, current.UpdatedAt}
			return nil
		}
		if revision < 0 || current.Revision != revision {
			return problem("revision_conflict", "The saved draft changed in another window.")
		}
		out = Draft{kind, key, json.RawMessage(`null`), revision + 1, now()}
		return q.SaveDraft(ctx, sqlgen.SaveDraftParams{UserID: actorID(ctx), Kind: kind, DraftKey: key, Value: "null", Revision: out.Revision, UpdatedAt: out.UpdatedAt})
	})
	return out, e
}

type RoomRead struct {
	RoomID string `json:"roomId"`
	Seq    int64  `json:"seq"`
}

func (s *Store) ListRoomReads(ctx context.Context) ([]RoomRead, error) {
	rows, e := s.queries().ListRoomReads(ctx, actorID(ctx))
	out := make([]RoomRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, RoomRead{r.RoomID, r.Seq})
	}
	return out, e
}
func (s *Store) SaveRoomRead(ctx context.Context, room string, seq int64) error {
	if seq < 0 {
		return problem("invalid_cursor", "Use a nonnegative message sequence.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if _, e := q.GetRoom(ctx, room); e != nil {
			return missing(e, "The room")
		}
		next, e := q.NextMessageSequence(ctx, room)
		if e != nil {
			return e
		}
		if seq >= next {
			return problem("invalid_cursor", "The read position is beyond the room's messages.")
		}
		return q.SaveRoomRead(ctx, sqlgen.SaveRoomReadParams{UserID: actorID(ctx), RoomID: room, Seq: seq})
	})
}

type InboxItem struct {
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

func (s *Store) ListInbox(ctx context.Context) ([]InboxItem, error) {
	rows, e := s.queries().ListInbox(ctx, actorID(ctx))
	out := make([]InboxItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, InboxItem{TaskID: r.TaskID, RoomName: r.RoomName, ReviewTitle: r.ReviewTitle, ReviewID: r.ReviewID, ID: r.ID, Kind: r.Kind, RoomID: r.RoomID, MessageID: r.MessageID, RunID: r.RunID, CreatedAt: r.CreatedAt, ReadAt: r.ReadAt})
	}
	return out, e
}
func (s *Store) ReadInboxItem(ctx context.Context, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		n, err := q.ReadInboxItem(ctx, sqlgen.ReadInboxItemParams{ID: id, UserID: actorID(ctx), ReadAt: now()})
		if err == nil && n == 0 {
			return problem("not_found", "The notification does not exist.")
		}
		return err
	})
}

type DraftSummary struct {
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	Revision   int64  `json:"revision"`
	UpdatedAt  string `json:"updatedAt"`
	Title      string `json:"title"`
	ResourceID string `json:"resourceId"`
	Path       string `json:"path"`
}

func (s *Store) ListDrafts(ctx context.Context, kind string) ([]DraftSummary, error) {
	if kind != "document" && kind != "conversation" && kind != "review" {
		return nil, problem("invalid_draft", "Choose conversation, document, or review drafts.")
	}
	rows, e := s.queries().ListDrafts(ctx, sqlgen.ListDraftsParams{UserID: actorID(ctx), Kind: kind})
	out := make([]DraftSummary, 0, len(rows))
	for _, r := range rows {
		title, _ := r.Title.(string)
		resource, _ := r.ResourceID.(string)
		path, _ := r.Path.(string)
		out = append(out, DraftSummary{r.Kind, r.DraftKey, r.Revision, r.UpdatedAt, title, resource, path})
	}
	return out, e
}

func (s *Store) MessageRecipientIDs(ctx context.Context, room string, input SendMessageInput) ([]string, error) {
	if input.PostOnly {
		return []string{}, nil
	}
	members, e := s.ListMembers(ctx, room)
	if e != nil {
		return nil, e
	}
	ids := append([]string{}, input.RecipientAgentIDs...)
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, member := range members {
		if !seen[member.ID] && mentionsHandle(input.Content, member.Handle) {
			ids = append(ids, member.ID)
			seen[member.ID] = true
		}
	}
	return ids, nil
}
