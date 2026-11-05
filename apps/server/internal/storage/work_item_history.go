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

const workHistoryPageBytes = 320 << 10

// WorkHistoryPage contains whole audit records. Cursor preserves the first
// page's identity frontier; mutable delivery/task/timer status stays current.
type WorkHistoryPage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor string            `json:"nextCursor"`
}

type workHistoryCursor struct {
	Version int    `json:"v"`
	WorkID  string `json:"w"`
	Kind    string `json:"k"`
	After   int64  `json:"a"`
	Through int64  `json:"t"`
}

func validWorkHistoryKind(kind string) bool {
	switch kind {
	case "evidence", "decisions", "tasks", "timers", "actions":
		return true
	default:
		return false
	}
}

func decodeWorkHistoryCursor(raw, id, kind string) (workHistoryCursor, error) {
	var out workHistoryCursor
	invalid := func() (workHistoryCursor, error) {
		return out, problem("invalid_page", "Use this work item's original history cursor and kind.")
	}
	if len(raw) > 1024 {
		return invalid()
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&out) != nil || decoder.Decode(&extra) != io.EOF || out.Version != 1 || out.WorkID != id || out.Kind != kind || out.After <= 0 || out.Through < out.After {
		return invalid()
	}
	return out, nil
}

func encodeWorkHistoryCursor(cursor workHistoryCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// WorkItemHistory allows audit access beyond the bounded detail preview. Reads
// use current enabled-member visibility and never admit work or model requests.
func (s *Store) WorkItemHistory(ctx context.Context, id, kind, cursor string, limit int) (out WorkHistoryPage, err error) {
	out.Items = []json.RawMessage{}
	if !validWorkHistoryKind(kind) || limit < 0 || limit > 20 {
		return out, problem("invalid_page", "Choose evidence, decisions, tasks, timers or actions and at most 20 records.")
	}
	if limit == 0 {
		limit = 20
	}
	page := workHistoryCursor{Version: 1, WorkID: id, Kind: kind}
	if cursor != "" {
		page, err = decodeWorkHistoryCursor(cursor, id, kind)
		if err != nil {
			return out, err
		}
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if _, err := getWorkItem(ctx, q, id); err != nil {
			return err
		}
		frontier, err := workHistoryFrontier(ctx, q, id, kind)
		if err != nil {
			return err
		}
		if cursor == "" {
			page.Through = frontier
		}
		if page.Through > frontier {
			return problem("invalid_page", "The history cursor exceeds its retained frontier.")
		}
		rows, err := workHistoryRows(ctx, q, id, kind, page.After, page.Through, int64(limit+1))
		if err != nil {
			return err
		}
		for index, row := range rows {
			if index == limit {
				break
			}
			if !json.Valid([]byte(row.Value)) {
				return problem("invalid_page", "A retained history record is not valid JSON.")
			}
			out.Items = append(out.Items, json.RawMessage(row.Value))
			candidate := page
			candidate.After = row.Seq
			out.NextCursor = ""
			if index+1 < len(rows) {
				out.NextCursor = encodeWorkHistoryCursor(candidate)
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				return err
			}
			if len(encoded)+1 > workHistoryPageBytes {
				out.Items = out.Items[:len(out.Items)-1]
				if len(out.Items) == 0 {
					return problem("invalid_page", "This retained record exceeds the bounded history response.")
				}
				out.NextCursor = encodeWorkHistoryCursor(page)
				break
			}
			page.After = row.Seq
		}
		return nil
	})
	return
}

type workHistoryRow struct {
	Seq   int64
	Value string
}

func workHistoryFrontier(ctx context.Context, q *sqlgen.Queries, id, kind string) (int64, error) {
	switch kind {
	case "evidence":
		return q.WorkHistoryEvidenceFrontier(ctx, id)
	case "decisions":
		return q.WorkHistoryDecisionsFrontier(ctx, id)
	case "tasks":
		return q.WorkHistoryTasksFrontier(ctx, id)
	case "timers":
		return q.WorkHistoryTimersFrontier(ctx, id)
	case "actions":
		return q.WorkHistoryActionsFrontier(ctx, id)
	}
	return 0, problem("invalid_page", "Choose a supported history kind.")
}

func workHistoryRows(ctx context.Context, q *sqlgen.Queries, id, kind string, after, through, limit int64) ([]workHistoryRow, error) {
	out := []workHistoryRow{}
	switch kind {
	case "evidence":
		rows, err := q.WorkHistoryEvidencePage(ctx, sqlgen.WorkHistoryEvidencePageParams{WorkItemID: id, AfterSeq: after, ThroughSeq: through, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, workHistoryRow{Seq: row.Seq, Value: row.Value})
		}
	case "decisions":
		rows, err := q.WorkHistoryDecisionsPage(ctx, sqlgen.WorkHistoryDecisionsPageParams{WorkItemID: id, AfterSeq: after, ThroughSeq: through, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, workHistoryRow{Seq: row.Seq, Value: row.Value})
		}
	case "tasks":
		rows, err := q.WorkHistoryTasksPage(ctx, sqlgen.WorkHistoryTasksPageParams{WorkItemID: id, AfterSeq: after, ThroughSeq: through, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, workHistoryRow{Seq: row.Seq, Value: row.Value})
		}
	case "timers":
		rows, err := q.WorkHistoryTimersPage(ctx, sqlgen.WorkHistoryTimersPageParams{WorkItemID: id, AfterSeq: after, ThroughSeq: through, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, workHistoryRow{Seq: row.Seq, Value: row.Value})
		}
	case "actions":
		rows, err := q.WorkHistoryActionsPage(ctx, sqlgen.WorkHistoryActionsPageParams{WorkItemID: id, AfterSeq: after, ThroughSeq: through, ResultLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, workHistoryRow{Seq: row.Seq, Value: row.Value})
		}
	}
	return out, nil
}
