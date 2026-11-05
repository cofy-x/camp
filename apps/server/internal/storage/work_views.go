package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func populateWorkView(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	w.Execution, w.Result = nil, nil
	var task CollaborationTask
	var err error
	if w.ActiveTaskID != "" {
		task, err = getTask(ctx, q, w.ActiveTaskID)
	} else {
		var raw string
		raw, err = q.LatestWorkTask(ctx, w.ID)
		if err == nil {
			task, err = taskSnapshot(raw)
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && task.ID != "" {
		execution := &orchestration.WorkExecution{TaskID: task.ID, Status: task.Status, Revision: task.Revision, Summary: task.Summary, Error: task.Error, ActiveRunID: task.ActiveRunID, UpdatedAt: task.UpdatedAt}
		for _, participant := range task.Participants {
			if participant.Agent.ID == task.CurrentAgentID {
				execution.CurrentAgentName = participant.Agent.Name
				break
			}
		}
		if task.Question != nil {
			execution.Question = &orchestration.WorkTaskQuestion{ID: task.Question.ID, Text: task.Question.Text, Options: append([]string{}, task.Question.Options...), Answer: task.Question.Answer}
		}
		w.Execution = execution
	}
	raw, err := q.LatestWorkResultTask(ctx, w.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	task, err = taskSnapshot(raw)
	if err != nil {
		return err
	}
	result := &orchestration.WorkResult{TaskID: task.ID, Summary: task.Summary, ResourceID: task.ResourceID, Commit: task.CheckpointCommit, Files: []orchestration.WorkResultFile{}}
	if task.PublishedCommit != "" {
		result.Commit, result.Published = task.PublishedCommit, true
	}
	runRaw, err := q.WorkResultRun(ctx, sqlgen.WorkResultRunParams{TaskID: task.ID, CommitID: result.Commit})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var run Run
		if err = json.Unmarshal([]byte(runRaw), &run); err != nil {
			return err
		}
		for index, file := range run.Files {
			if index == 20 {
				result.FilesLimited = true
				break
			}
			result.Files = append(result.Files, orchestration.WorkResultFile{Path: file.Path, Status: file.Status})
		}
	}
	w.Result = result
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
