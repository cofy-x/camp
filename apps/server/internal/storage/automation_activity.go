package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type AutomationEventPage struct {
	Items      []orchestration.SourceEvent `json:"items"`
	Through    int64                       `json:"through"`
	NextAfter  *int64                      `json:"nextAfter"`
	NextBefore *int64                      `json:"nextBefore"`
	HasMore    bool                        `json:"hasMore"`
}

type AutomationEventQuery struct {
	Direction string
	SourceID  string
	After     int64
	Before    *int64
	Through   *int64
	Limit     int
}

// AutomationEvents preserves the original ascending admission-history traversal.
func (s *Store) AutomationEvents(ctx context.Context, room string, after int64, through *int64, limit int) (AutomationEventPage, error) {
	return s.AutomationActivity(ctx, room, AutomationEventQuery{After: after, Through: through, Limit: limit})
}

// AutomationActivity freezes source identities through a Room frontier. Decision
// state remains current; previews are bounded read projections of visible facts.
func (s *Store) AutomationActivity(ctx context.Context, room string, in AutomationEventQuery) (page AutomationEventPage, err error) {
	page.Items = []orchestration.SourceEvent{}
	if in.Direction == "" {
		in.Direction = "asc"
	}
	if in.Direction != "asc" && in.Direction != "desc" {
		return page, problem("invalid_page", "Choose ascending or descending event order.")
	}
	if (in.Direction == "asc" && in.Before != nil) || (in.Direction == "desc" && in.After != 0) || in.Limit < 0 || in.Limit > 20 {
		return page, problem("invalid_page", "Choose one event cursor and at most 20 events.")
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		if _, e := q.GetRoom(ctx, room); e != nil {
			return missing(e, "The Room")
		}
		if in.SourceID != "" && in.SourceID != "room" && in.SourceID != "task" {
			if !validText(in.SourceID, 128, false) {
				return problem("invalid_page", "Choose a source belonging to this Room.")
			}
			source, e := q.GetGitHubSource(ctx, in.SourceID)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e != nil || source.RoomID != room {
				return problem("invalid_page", "Choose a source belonging to this Room.")
			}
		}
		frontier, e := q.AutomationFrontier(ctx, room)
		if e != nil {
			return e
		}
		page.Through = frontier
		if in.Through != nil {
			page.Through = *in.Through
		}
		before := int64(0)
		if in.Before != nil {
			before = *in.Before
		}
		if page.Through < 0 || page.Through > frontier || in.After < 0 || in.After > page.Through || (in.Before != nil && (before < 1 || before > page.Through)) {
			return problem("invalid_page", "Choose an event cursor within the frozen Room frontier.")
		}
		// Seek to an inclusive SQL upper bound without an OR sentinel or adding
		// one to the maximum SQLite sequence. The public before remains exclusive.
		upper := page.Through
		if in.Before != nil {
			upper = before - 1
		}
		rows, e := readAutomationActivity(ctx, q, room, in, page.Through, upper)
		if e != nil {
			return e
		}
		for index, row := range rows {
			if index == in.Limit {
				break
			}
			item, e := automationActivityEvent(row)
			if e != nil {
				return e
			}
			previous := page
			page.Items = append(page.Items, item)
			page.HasMore = index+1 < len(rows)
			page.NextAfter, page.NextBefore = nil, nil
			if page.HasMore {
				seq := item.Seq
				if in.Direction == "desc" {
					page.NextBefore = &seq
				} else {
					page.NextAfter = &seq
				}
			}
			encoded, e := json.Marshal(page)
			if e != nil {
				return e
			}
			if len(encoded)+1 > 64<<10 {
				page = previous
				if len(page.Items) == 0 {
					return problem("invalid_page", "The stored event cannot fit in a bounded activity page.")
				}
				break
			}
		}
		return nil
	})
	return
}

func readAutomationActivity(ctx context.Context, q *sqlgen.Queries, room string, in AutomationEventQuery, through, before int64) ([]sqlgen.AutomationActivityDescendingRow, error) {
	limit := int64(in.Limit + 1)
	if in.Direction == "desc" {
		if in.SourceID == "" {
			return q.AutomationActivityDescending(ctx, sqlgen.AutomationActivityDescendingParams{RoomID: room, ThroughSeq: through, BeforeSeq: before, PageLimit: limit})
		}
		rows, err := q.AutomationActivitySourceDescending(ctx, sqlgen.AutomationActivitySourceDescendingParams{RoomID: room, SourceID: in.SourceID, ThroughSeq: through, BeforeSeq: before, PageLimit: limit})
		out := make([]sqlgen.AutomationActivityDescendingRow, len(rows))
		for i, row := range rows {
			out[i] = sqlgen.AutomationActivityDescendingRow(row)
		}
		return out, err
	}
	if in.SourceID == "" {
		rows, err := q.AutomationActivityAscending(ctx, sqlgen.AutomationActivityAscendingParams{RoomID: room, ThroughSeq: through, AfterSeq: in.After, PageLimit: limit})
		out := make([]sqlgen.AutomationActivityDescendingRow, len(rows))
		for i, row := range rows {
			out[i] = sqlgen.AutomationActivityDescendingRow(row)
		}
		return out, err
	}
	rows, err := q.AutomationActivitySourceAscending(ctx, sqlgen.AutomationActivitySourceAscendingParams{RoomID: room, SourceID: in.SourceID, ThroughSeq: through, AfterSeq: in.After, PageLimit: limit})
	out := make([]sqlgen.AutomationActivityDescendingRow, len(rows))
	for i, row := range rows {
		out[i] = sqlgen.AutomationActivityDescendingRow(row)
	}
	return out, err
}

func automationActivityEvent(row sqlgen.AutomationActivityDescendingRow) (item orchestration.SourceEvent, err error) {
	if err = json.Unmarshal([]byte(row.Value), &item); err != nil {
		return item, err
	}
	item.Seq, item.Status, item.Reason, item.TaskID, item.EvaluationID, item.PolicyRevision = row.Seq, row.Status, row.Reason, row.TaskID, row.EvaluationID, row.PolicyRevision
	item.Content = ""
	item.Preview = automationTextPreview(row.Preview, 320)
	if row.AssessmentOutcome == "accept" || row.AssessmentOutcome == "ignore" || row.AssessmentOutcome == "unavailable" {
		item.AssessmentOutcome = row.AssessmentOutcome
		item.AssessmentReason = automationTextPreview(row.AssessmentReason, 512)
	}
	return
}

func automationTextPreview(text string, maxBytes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= maxBytes {
		return text
	}
	return truncateUTF8(text, maxBytes-3) + "..."
}
