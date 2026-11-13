package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const reviewListPageBytes = 320 << 10

type ReviewListOptions struct {
	RoomID     string
	ResourceID string
	Path       string
	Cursor     string
	Limit      int
}

type ReviewListPage struct {
	Reviews    []Review `json:"reviews"`
	NextCursor string   `json:"nextCursor"`
}

// The key names the first unread record, so every query uses an inclusive,
// indexed seek. Creation keys stay stable when a review's current state changes.
type reviewListCursor struct {
	Version   int    `json:"v"`
	Actor     string `json:"u"`
	Scope     string `json:"s"`
	CreatedAt string `json:"t"`
	ID        string `json:"i"`
}

func encodeReviewListCursor(cursor reviewListCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeReviewListCursor(raw string, expected reviewListCursor) (reviewListCursor, error) {
	invalid := func() (reviewListCursor, error) {
		return reviewListCursor{}, problem("invalid_page", "Carry the original review cursor with the same account and filters.")
	}
	if len(raw) > 2048 {
		return invalid()
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return invalid()
	}
	var out reviewListCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&out) != nil || decoder.Decode(&extra) != io.EOF {
		return invalid()
	}
	canonical, _ := json.Marshal(out)
	_, dateErr := time.Parse(time.RFC3339Nano, out.CreatedAt)
	if !bytes.Equal(canonical, decoded) || out.Version != 1 || out.Actor != expected.Actor || out.Scope != expected.Scope || dateErr != nil || out.ID == "" || len(out.ID) > 128 {
		return invalid()
	}
	return out, nil
}

// ListReviews reads only current display facts under one authority snapshot.
// History, exact documents, decisions and their original receipts keep their
// existing owners; reading a page starts no work and changes no review state.
func (s *Store) ListReviews(ctx context.Context, options ReviewListOptions) (out ReviewListPage, err error) {
	out.Reviews = []Review{}
	actor, present := UserFromContext(ctx)
	if !present {
		return out, problem("unauthenticated", "Sign in to read reviews.")
	}
	if options.Limit == 0 {
		options.Limit = 20
	}
	if options.Limit < 1 || options.Limit > 20 || len(options.RoomID) > 128 || len(options.ResourceID) > 128 || (options.Path != "" && (options.ResourceID == "" || !validReferencePath(options.Path))) {
		return out, problem("invalid_page", "Choose a Room, source or source file and at most 20 reviews.")
	}
	page := reviewListCursor{
		Version: 1,
		Actor:   actor.ID,
		Scope: reviewFingerprint("list", "", struct{ RoomID, ResourceID, Path string }{
			options.RoomID, options.ResourceID, options.Path,
		}),
	}
	if options.Cursor != "" {
		page, err = decodeReviewListCursor(options.Cursor, page)
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
		if options.ResourceID != "" {
			if _, err := q.GetResource(ctx, options.ResourceID); err != nil {
				return missing(err, "The source")
			}
		}
		if options.Cursor == "" {
			first, err := q.ReviewListFirst(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			page.CreatedAt, page.ID = first.CreatedAt, first.ID
		}
		rows, err := reviewListRows(ctx, q, options, page)
		if err != nil {
			return err
		}
		for index, row := range rows {
			next := page
			next.CreatedAt, next.ID = row.CreatedAt, row.ID
			if index == options.Limit {
				out.NextCursor = encodeReviewListCursor(next)
				break
			}
			out.Reviews = append(out.Reviews, row)
			encoded, err := json.Marshal(out)
			if err != nil {
				return err
			}
			// Reserve a complete bounded cursor and the HTTP response newline.
			if len(encoded)+2049 > reviewListPageBytes {
				out.Reviews = out.Reviews[:len(out.Reviews)-1]
				if len(out.Reviews) == 0 {
					return problem("invalid_page", "This retained review exceeds the bounded display response.")
				}
				out.NextCursor = encodeReviewListCursor(next)
				break
			}
		}
		return nil
	})
	return
}

func reviewListRows(ctx context.Context, q *sqlgen.Queries, options ReviewListOptions, page reviewListCursor) ([]Review, error) {
	limit := int64(options.Limit + 1)
	out := []Review{}
	switch {
	case options.Path != "" && options.RoomID != "":
		rows, err := q.ReviewListRoomFilePage(ctx, sqlgen.ReviewListRoomFilePageParams{RoomID: options.RoomID, ResourceID: options.ResourceID, Path: options.Path, BeforeCreatedAt: page.CreatedAt, BeforeID: page.ID, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, reviewRow(sqlgen.GetReviewRow(row)))
		}
	case options.Path != "":
		rows, err := q.ReviewListFilePage(ctx, sqlgen.ReviewListFilePageParams{ResourceID: options.ResourceID, Path: options.Path, BeforeCreatedAt: page.CreatedAt, BeforeID: page.ID, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, reviewRow(sqlgen.GetReviewRow(row)))
		}
	case options.ResourceID != "" && options.RoomID != "":
		rows, err := q.ReviewListRoomResourcePage(ctx, sqlgen.ReviewListRoomResourcePageParams{RoomID: options.RoomID, ResourceID: options.ResourceID, BeforeCreatedAt: page.CreatedAt, BeforeID: page.ID, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, reviewRow(sqlgen.GetReviewRow(row)))
		}
	case options.ResourceID != "":
		rows, err := q.ReviewListResourcePage(ctx, sqlgen.ReviewListResourcePageParams{ResourceID: options.ResourceID, BeforeCreatedAt: page.CreatedAt, BeforeID: page.ID, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, reviewRow(sqlgen.GetReviewRow(row)))
		}
	case options.RoomID != "":
		rows, err := q.ReviewListRoomPage(ctx, sqlgen.ReviewListRoomPageParams{RoomID: options.RoomID, BeforeCreatedAt: page.CreatedAt, BeforeID: page.ID, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, reviewRow(sqlgen.GetReviewRow(row)))
		}
	default:
		rows, err := q.ReviewListGlobalPage(ctx, sqlgen.ReviewListGlobalPageParams{BeforeCreatedAt: page.CreatedAt, BeforeID: page.ID, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, reviewRow(sqlgen.GetReviewRow(row)))
		}
	}
	return out, nil
}
