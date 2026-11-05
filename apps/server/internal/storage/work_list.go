package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const workListPageBytes = 320 << 10

// WorkListItem contains current navigation facts, never a grant or audit history.
type WorkListItem struct {
	Seq          int64                          `json:"seq"`
	ID           string                         `json:"id"`
	RoomID       string                         `json:"roomId"`
	RoomName     string                         `json:"roomName"`
	OwnerUserID  string                         `json:"ownerUserId"`
	Goal         string                         `json:"goal"`
	Status       string                         `json:"status"`
	Revision     int64                          `json:"revision"`
	DueAt        string                         `json:"dueAt,omitempty"`
	Question     string                         `json:"question,omitempty"`
	QuestionKind orchestration.WorkQuestionKind `json:"questionKind,omitempty"`
	ActiveTaskID string                         `json:"activeTaskId,omitempty"`
	Execution    *orchestration.WorkExecution   `json:"execution,omitempty"`
	Result       *orchestration.WorkResult      `json:"result,omitempty"`
	NeedsYou     bool                           `json:"needsYou"`
	CreatedAt    string                         `json:"createdAt"`
	UpdatedAt    string                         `json:"updatedAt"`
}

type WorkListPage struct {
	Items      []WorkListItem `json:"items"`
	NextCursor string         `json:"nextCursor"`
	HasMore    bool           `json:"hasMore"`
}

type WorkListOptions struct {
	Scope  string
	View   string
	RoomID string
	Cursor string
	Limit  int
}

type workListCursor struct {
	Version  int    `json:"v"`
	Actor    string `json:"u"`
	Scope    string `json:"s"`
	View     string `json:"k"`
	RoomID   string `json:"r"`
	Through  int64  `json:"t"`
	Priority int64  `json:"p"`
	Before   int64  `json:"b"`
}

func encodeWorkListCursor(cursor workListCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeWorkListCursor(raw string, expected workListCursor) (workListCursor, error) {
	invalid := func() (workListCursor, error) {
		return workListCursor{}, problem("invalid_page", "Carry the original Work cursor with the same account and filters.")
	}
	if len(raw) > 2048 {
		return invalid()
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return invalid()
	}
	var out workListCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&out) != nil || decoder.Decode(&extra) != io.EOF {
		return invalid()
	}
	canonical, _ := json.Marshal(out)
	if !bytes.Equal(canonical, decoded) || out.Version != 1 || out.Actor != expected.Actor || out.Scope != expected.Scope || out.View != expected.View || out.RoomID != expected.RoomID || out.Before <= 0 || out.Through < out.Before || out.Priority < 0 || out.Priority > 1 {
		return invalid()
	}
	return out, nil
}

// ListWorkItems filters before keyset pagination, revalidates the current actor
// inside one read transaction, and does not admit execution or change a matter.
func (s *Store) ListWorkItems(ctx context.Context, options WorkListOptions) (out WorkListPage, err error) {
	out.Items = []WorkListItem{}
	actor, present := UserFromContext(ctx)
	if !present {
		return out, problem("unauthenticated", "Sign in to read Work.")
	}
	if options.Scope == "" {
		options.Scope = "mine"
	}
	if options.View == "" {
		options.View = "active"
	}
	if options.Limit == 0 {
		options.Limit = 20
	}
	if (options.Scope != "mine" && options.Scope != "team") || (options.View != "active" && options.View != "needs_you" && options.View != "done" && options.View != "all") || options.Limit < 1 || options.Limit > 20 || len(options.RoomID) > 128 {
		return out, problem("invalid_page", "Choose a Work view and scope, and at most 20 items.")
	}
	page := workListCursor{Version: 1, Actor: actor.ID, Scope: options.Scope, View: options.View, RoomID: options.RoomID, Priority: 1}
	if options.Cursor != "" {
		page, err = decodeWorkListCursor(options.Cursor, page)
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
		frontier, err := q.WorkListFrontier(ctx)
		if err != nil {
			return err
		}
		if options.Cursor == "" {
			page.Through = frontier
		}
		if page.Through > frontier {
			return problem("invalid_page", "The Work cursor exceeds its retained frontier.")
		}
		rows, err := q.WorkListPage(ctx, sqlgen.WorkListPageParams{ActorID: actor.ID, Scope: options.Scope, RoomID: options.RoomID, View: options.View, ThroughSeq: page.Through, BeforePriority: page.Priority, BeforeSeq: page.Before, PageLimit: int64(options.Limit + 1)})
		if err != nil {
			return err
		}
		for index, row := range rows {
			if index == options.Limit {
				out.HasMore = true
				break
			}
			item, err := workItemRow(sqlgen.WorkItem{Seq: row.Seq, ID: row.ID, RoomID: row.RoomID, OwnerUserID: row.OwnerUserID, PolicyID: row.PolicyID, Status: row.Status, Revision: row.Revision, Value: row.Value, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
			if err != nil {
				return err
			}
			if err = populateWorkView(ctx, q, &item); err != nil {
				return err
			}
			out.Items = append(out.Items, WorkListItem{Seq: item.Seq, ID: item.ID, RoomID: item.RoomID, RoomName: row.RoomName, OwnerUserID: item.OwnerUserID, Goal: item.Goal, Status: item.Status, Revision: item.Revision, DueAt: item.DueAt, Question: item.Question, QuestionKind: item.QuestionKind, ActiveTaskID: item.ActiveTaskID, Execution: item.Execution, Result: item.Result, NeedsYou: row.NeedsYou == 1, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
			encoded, err := json.Marshal(out)
			if err != nil {
				return err
			}
			// Reserve the bounded cursor's encoded size as well as the newline.
			if len(encoded)+2049 > workListPageBytes {
				out.Items = out.Items[:len(out.Items)-1]
				if len(out.Items) == 0 {
					return problem("invalid_page", "This Work summary exceeds the bounded response.")
				}
				out.HasMore = true
				break
			}
			page.Before, page.Priority = item.Seq, row.NeedsYou
		}
		if out.HasMore {
			out.NextCursor = encodeWorkListCursor(page)
		}
		return nil
	})
	return
}
