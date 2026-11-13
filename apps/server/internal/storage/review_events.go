package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const reviewEventPageBytes = 128 << 10

type ReviewEventOptions struct {
	Through int64
	Cursor  string
	Limit   int
}

// ReviewEventPage contains complete immutable facts, newest first. Its cursor
// also names the first page so returning from older records keeps the frontier.
type ReviewEventPage struct {
	Events     []ReviewEvent `json:"events"`
	Through    int64         `json:"through"`
	Cursor     string        `json:"cursor"`
	NextCursor string        `json:"nextCursor"`
}

type reviewEventCursor struct {
	Version int    `json:"v"`
	Actor   string `json:"u"`
	Review  string `json:"r"`
	Through int64  `json:"t"`
	Before  int64  `json:"b"`
}

func encodeReviewEventCursor(page reviewEventCursor) string {
	raw, _ := json.Marshal(page)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeReviewEventCursor(raw string, expected reviewEventCursor) (reviewEventCursor, error) {
	invalid := func() (reviewEventCursor, error) {
		return reviewEventCursor{}, problem("invalid_page", "Carry this review's original history cursor with the same account and revision.")
	}
	if len(raw) > 1024 {
		return invalid()
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return invalid()
	}
	var out reviewEventCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&out) != nil || decoder.Decode(&extra) != io.EOF {
		return invalid()
	}
	canonical, _ := json.Marshal(out)
	if !bytes.Equal(canonical, decoded) || out.Version != 1 || out.Actor != expected.Actor || out.Review != expected.Review || out.Through < 1 || out.Before < 1 || out.Before > out.Through || (expected.Through != 0 && expected.Through != out.Through) {
		return invalid()
	}
	return out, nil
}

func (s *Store) ReviewEvents(ctx context.Context, id string, options ReviewEventOptions) (out ReviewEventPage, err error) {
	out.Events = []ReviewEvent{}
	if options.Limit == 0 {
		options.Limit = 20
	}
	if options.Limit < 1 || options.Limit > 20 || options.Through < 0 {
		return out, problem("invalid_page", "Choose a retained review revision and at most 20 records.")
	}
	actor, present := UserFromContext(ctx)
	if !present {
		return out, problem("unauthenticated", "Sign in to read review history.")
	}
	page := reviewEventCursor{Version: 1, Actor: actor.ID, Review: id, Through: options.Through}
	if options.Cursor != "" {
		page, err = decodeReviewEventCursor(options.Cursor, page)
		if err != nil {
			return out, err
		}
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		review, e := getReview(ctx, q, id)
		if e != nil {
			return e
		}
		if page.Through == 0 {
			page.Through = review.Revision
		}
		if page.Through > review.Revision {
			return problem("invalid_page", "The history revision exceeds the retained review.")
		}
		if options.Cursor == "" {
			page.Before = page.Through
		}
		out.Through, out.Cursor = page.Through, encodeReviewEventCursor(page)
		rows, e := q.ReviewEventPage(ctx, sqlgen.ReviewEventPageParams{ReviewID: id, BeforeRevision: page.Before, ThroughRevision: page.Through, ResultLimit: int64(options.Limit + 1)})
		if e != nil {
			return e
		}
		for index, row := range rows {
			next := page
			next.Before = row.Revision
			if index == options.Limit {
				out.NextCursor = encodeReviewEventCursor(next)
				break
			}
			out.Events = append(out.Events, ReviewEvent{ID: row.ID, Seq: row.Seq, ReviewID: row.ReviewID, ActorUserID: row.ActorUserID, ActorName: row.ActorName, Action: row.Action, Comment: row.Comment, Commit: row.CommitID, ReviewerUserID: row.ReviewerUserID, Revision: row.Revision, MessageID: row.MessageID, CreatedAt: row.CreatedAt})
			encoded, e := json.Marshal(out)
			if e != nil {
				return e
			}
			// Reserve a complete next cursor and the response newline.
			if len(encoded)+1025 > reviewEventPageBytes {
				out.Events = out.Events[:len(out.Events)-1]
				if len(out.Events) == 0 {
					return problem("invalid_page", "This retained event exceeds the bounded history response.")
				}
				out.NextCursor = encodeReviewEventCursor(next)
				break
			}
		}
		return nil
	})
	return
}
