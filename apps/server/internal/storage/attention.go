package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const attentionPageBytes = 320 << 10

type AttentionTarget struct {
	App        string `json:"app"`
	RoomID     string `json:"roomId"`
	WorkItemID string `json:"workItemId,omitempty"`
	ReviewID   string `json:"reviewId,omitempty"`
	TaskID     string `json:"taskId,omitempty"`
}

// AttentionItem is a current navigation projection, never an authorization or
// another owner of the source's status, question, revision or recovery receipt.
type AttentionItem struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Source      string          `json:"source"`
	RoomID      string          `json:"roomId"`
	RoomName    string          `json:"roomName"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason"`
	Priority    int64           `json:"priority"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
	TextLimited bool            `json:"textLimited,omitempty"`
	Target      AttentionTarget `json:"target"`
}

type AttentionCounts struct {
	Judgment  int64 `json:"judgment"`
	Review    int64 `json:"review"`
	Exception int64 `json:"exception"`
}

type AttentionPage struct {
	Items      []AttentionItem `json:"items"`
	NextCursor string          `json:"nextCursor"`
	HasMore    bool            `json:"hasMore"`
	Total      int64           `json:"total"`
	Counts     AttentionCounts `json:"counts"`
}

type AttentionOptions struct {
	RoomID string
	Kind   string
	Cursor string
	Limit  int
}

type attentionCursor struct {
	Version   int    `json:"v"`
	ActorID   string `json:"actor"`
	RoomID    string `json:"room"`
	Kind      string `json:"kind"`
	Priority  int64  `json:"priority"`
	CreatedAt string `json:"createdAt"`
	ID        string `json:"id"`
}

func encodeAttentionCursor(cursor attentionCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeAttentionCursor(raw string, expected attentionCursor) (attentionCursor, error) {
	invalid := func() (attentionCursor, error) {
		return attentionCursor{}, problem("invalid_page", "Carry the original attention cursor with the same account and filters.")
	}
	if len(raw) > 2048 {
		return invalid()
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return invalid()
	}
	var out attentionCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&out) != nil || decoder.Decode(&extra) != io.EOF {
		return invalid()
	}
	canonical, _ := json.Marshal(out)
	_, stampErr := time.Parse(time.RFC3339Nano, out.CreatedAt)
	prefix, entity, found := strings.Cut(out.ID, ":")
	if !bytes.Equal(canonical, decoded) || out.Version != 1 || out.ActorID != expected.ActorID || out.RoomID != expected.RoomID || out.Kind != expected.Kind || out.Priority < 0 || out.Priority > 2 || stampErr != nil || len(out.CreatedAt) > 64 || !found || entity == "" || !validText(out.ID, 256, false) || (prefix != "work" && prefix != "review" && prefix != "task" && prefix != "delivery") {
		return invalid()
	}
	return out, nil
}

// Attention reads current business responsibility and counts in one snapshot.
// Inbox read positions are deliberately absent: reading a notice cannot answer
// a question, decide a review, or settle an uncertain external action.
func (s *Store) Attention(ctx context.Context, options AttentionOptions) (out AttentionPage, err error) {
	out.Items = []AttentionItem{}
	actor, authenticated := UserFromContext(ctx)
	if !authenticated {
		return out, problem("unauthenticated", "Sign in to read your pending work.")
	}
	if options.Kind == "" {
		options.Kind = "all"
	}
	if options.Limit == 0 {
		options.Limit = 20
	}
	if !validText(options.RoomID, 128, true) || options.Limit < 1 || options.Limit > 20 || (options.Kind != "all" && options.Kind != "judgment" && options.Kind != "review" && options.Kind != "exception") {
		return out, problem("invalid_page", "Choose an attention category and at most 20 items.")
	}
	cursor := attentionCursor{Version: 1, ActorID: actor.ID, RoomID: options.RoomID, Kind: options.Kind}
	if options.Cursor != "" {
		cursor, err = decodeAttentionCursor(options.Cursor, cursor)
		if err != nil {
			return out, err
		}
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if options.RoomID != "" {
			if _, err := q.GetRoom(ctx, options.RoomID); err != nil {
				return missing(err, "The Room")
			}
		}
		counts, err := q.AttentionCounts(ctx, sqlgen.AttentionCountsParams{ActorID: actor.ID, RoomID: options.RoomID})
		if err != nil {
			return err
		}
		out.Total = counts.Total
		out.Counts = AttentionCounts{Judgment: counts.Judgment, Review: counts.Review, Exception: counts.Exception}
		rows, err := q.AttentionPage(ctx, sqlgen.AttentionPageParams{ActorID: actor.ID, RoomID: options.RoomID, Kind: options.Kind, AfterID: cursor.ID, AfterPriority: cursor.Priority, AfterCreatedAt: cursor.CreatedAt, PageLimit: int64(options.Limit + 1)})
		if err != nil {
			return err
		}
		for index, row := range rows {
			if index == options.Limit {
				out.HasMore = true
				break
			}
			title, summary := truncateUTF8(row.Title, 512), truncateUTF8(row.Summary, 2048)
			item := AttentionItem{ID: row.ID, Kind: row.Kind, Source: row.Source, RoomID: row.RoomID, RoomName: row.RoomName, Title: title, Summary: summary, Status: row.Status, Reason: row.Reason, Priority: row.Priority, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, TextLimited: title != row.Title || summary != row.Summary, Target: AttentionTarget{App: row.TargetApp, RoomID: row.RoomID, WorkItemID: row.WorkItemID, ReviewID: row.ReviewID, TaskID: row.TaskID}}
			out.Items = append(out.Items, item)
			raw, err := json.Marshal(out)
			if err != nil {
				return err
			}
			if len(raw)+2049 > attentionPageBytes {
				out.Items = out.Items[:len(out.Items)-1]
				if len(out.Items) == 0 {
					return problem("invalid_page", "This attention item exceeds the bounded response.")
				}
				out.HasMore = true
				break
			}
			cursor.Priority, cursor.CreatedAt, cursor.ID = row.Priority, row.CreatedAt, row.ID
		}
		if out.HasMore {
			out.NextCursor = encodeAttentionCursor(cursor)
		}
		return nil
	})
	return
}
