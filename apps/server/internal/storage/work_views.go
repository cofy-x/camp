package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// populateWorkView reads only display facts from related Tasks and the exact
// saved-result Run. Original inputs, instructions, output and file bytes remain
// behind their explicit detail readers.
func populateWorkView(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	w.Execution, w.Result = nil, nil
	raw, err := q.WorkExecutionView(ctx, sqlgen.WorkExecutionViewParams{ActiveTaskID: w.ActiveTaskID, WorkItemID: w.ID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) && w.ActiveTaskID != "" {
		return missing(err, "The collaboration")
	}
	if err == nil {
		var execution orchestration.WorkExecution
		if err = json.Unmarshal([]byte(raw), &execution); err != nil {
			return err
		}
		w.Execution = &execution
	}
	raw, err = q.WorkResultView(ctx, w.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var result orchestration.WorkResult
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return err
	}
	w.Result = &result
	return nil
}

// WorkItemsFiltered applies state before pagination. Its cursor walks newest
// recorded matters first; background refresh can start a new traversal.
func (s *Store) WorkItemsFiltered(ctx context.Context, room string, after int64, limit int, state string) (out orchestration.WorkItemPage, err error) {
	if state == "" {
		return s.WorkItems(ctx, room, after, limit)
	}
	if after < 0 || limit < 0 || limit > 20 || (state != "all" && state != "needs_attention" && state != "active" && state != "done") {
		return out, problem("invalid_page", "Choose a work state and at most 20 matters per page.")
	}
	if limit == 0 {
		limit = 20
	}
	out.Items = []orchestration.WorkItem{}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if _, err := q.GetRoom(ctx, room); err != nil {
			return missing(err, "The Room")
		}
		rows, err := q.WorkItemsStatePage(ctx, sqlgen.WorkItemsStatePageParams{RoomID: room, BeforeSeq: after, State: state, PageLimit: int64(limit + 1)})
		if err != nil {
			return err
		}
		out.HasMore = len(rows) > limit
		for index, row := range rows {
			if index == limit {
				break
			}
			item, err := workItemRow(row)
			if err != nil {
				return err
			}
			if err := populateWorkView(ctx, q, &item); err != nil {
				return err
			}
			out.Items = append(out.Items, item)
			out.NextAfter = item.Seq
		}
		return nil
	})
	return
}
