package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const RunActivityLimit = 50

type RunActivityPosition struct {
	Before  int64
	After   int64
	Through *int64
	Limit   int
}

// Activity is a display projection. Original event bodies remain unchanged at
// the explicit event/diagnostic reader, including events omitted from this view.
type RunActivityItem struct {
	Seq       int64  `json:"seq"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"createdAt"`
}

type RunActivityWindow struct {
	Items    []RunActivityItem `json:"items"`
	Through  int64             `json:"through"`
	HasOlder bool              `json:"hasOlder"`
	HasNewer bool              `json:"hasNewer"`
}

func activityText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return value
}

func activityItem(row sqlgen.RunEvent) RunActivityItem {
	item := RunActivityItem{Seq: row.Seq, Type: row.Type, CreatedAt: row.CreatedAt}
	switch row.Type {
	case "status":
		item.Name = activityText(row.Text, 80)
	case "error":
		item.Detail = activityText(row.Text, 160)
	case "tool_call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal([]byte(row.Text), &call) != nil {
			return item
		}
		item.Name = activityText(call.Name, 80)
		args := call.Arguments
		var encoded string
		if json.Unmarshal(args, &encoded) == nil {
			args = json.RawMessage(encoded)
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(args, &values) == nil {
			for _, name := range []string{"command", "path", "file_path"} {
				var value string
				if json.Unmarshal(values[name], &value) == nil {
					item.Detail = activityText(value, 160)
					break
				}
			}
		}
	}
	return item
}

func (s *Store) RunActivity(ctx context.Context, id string, position RunActivityPosition) (out RunActivityWindow, err error) {
	out.Items = []RunActivityItem{}
	if position.Limit == 0 {
		position.Limit = RunActivityLimit
	}
	if position.Limit < 1 || position.Limit > RunActivityLimit || position.Before < 0 || position.After < 0 || (position.Before > 0 && position.After > 0) {
		return out, problem("invalid_page", "Choose one activity window and at most 50 entries.")
	}
	var selected []sqlgen.RunEvent
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, err := q.GetRunIdentity(ctx, id); err != nil {
			return missing(err, "The run")
		}
		frontier, err := q.RunEventFrontier(ctx, id)
		if err != nil {
			return err
		}
		out.Through = frontier
		if position.Through != nil {
			if *position.Through < 0 || *position.Through > frontier {
				return problem("invalid_page", "The activity frontier is outside this execution.")
			}
			out.Through = *position.Through
		}
		if position.After > out.Through || (position.Before > 0 && position.Before-1 > out.Through) {
			return problem("invalid_page", "The activity position is outside this window.")
		}
		var rows []sqlgen.RunEvent
		if position.After > 0 {
			rows, err = q.RunActivityAfter(ctx, sqlgen.RunActivityAfterParams{RunID: id, AfterSeq: position.After, ThroughSeq: out.Through, PageLimit: int64(position.Limit)})
		} else {
			upper := out.Through
			if position.Before > 0 {
				upper = position.Before - 1
			}
			rows, err = q.RunActivityBefore(ctx, sqlgen.RunActivityBeforeParams{RunID: id, ThroughSeq: upper, PageLimit: int64(position.Limit)})
			slices.Reverse(rows)
		}
		if err != nil {
			return err
		}
		first, last := out.Through+1, out.Through
		if position.Before > 0 {
			first, last = position.Before, position.Before-1
		}
		if position.After > 0 {
			first, last = position.After+1, position.After
		}
		selected = rows
		for i, row := range rows {
			if i == 0 {
				first = row.Seq
			}
			last = row.Seq
		}
		edges, err := q.RunActivityEdges(ctx, sqlgen.RunActivityEdgesParams{RunID: id, FirstSeq: first, LastSeq: last, ThroughSeq: out.Through})
		out.HasOlder, out.HasNewer = edges.HasOlder, edges.HasNewer
		return err
	})
	if err == nil {
		// Projection parses at most one bounded page after releasing the
		// database connection, so a large tool argument cannot stall writers.
		for _, row := range selected {
			out.Items = append(out.Items, activityItem(row))
		}
	}
	return out, err
}
