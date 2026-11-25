package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func currentWorkItemDetails(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) (orchestration.WorkItem, error) {
	w.Evidence, w.Decisions, w.Tasks, w.Timers, w.Actions, w.Watches = []orchestration.WorkEvidence{}, []orchestration.WorkDecision{}, []orchestration.WorkTask{}, []orchestration.WorkTimer{}, []orchestration.WorkAction{}, []orchestration.WorkWatch{}
	if w.WaitCondition != "" {
		timer, err := q.PendingWorkTimer(ctx, w.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return w, err
		}
		if err == nil {
			w.Timers = append(w.Timers, orchestration.WorkTimer{ID: timer.ID, DueAt: timer.DueAt, Condition: timer.Condition, Status: timer.Status, EventID: timer.EventID})
		}
	}
	if w.Status == "completed" || w.Status == "cancelled" {
		row, err := q.WorkClosingDecision(ctx, sqlgen.WorkClosingDecisionParams{WorkItemID: w.ID, Status: w.Status})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return w, err
		}
		if err == nil {
			var decision orchestration.WorkDecision
			if err = json.Unmarshal([]byte(row.Value), &decision); err != nil {
				return w, err
			}
			decision.Seq = row.Seq
			w.Decisions = append(w.Decisions, decision)
		}
	}
	if err := populateWorkWatches(ctx, q, &w); err != nil {
		return w, err
	}
	err := populateWorkView(ctx, q, &w)
	return w, err
}

// populateWorkView reads only display facts from related Tasks and the exact
// saved-result Run. Original inputs, instructions, output and file bytes remain
// behind their explicit detail readers.
func populateWorkView(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	w.Result = nil
	if err := populateGitHubDelivery(ctx, q, w); err != nil {
		return err
	}
	if err := populateWorkRecovery(ctx, q, w); err != nil {
		return err
	}
	execution, err := readWorkExecution(ctx, q, *w)
	if err != nil {
		return err
	}
	w.Execution = execution
	w.CurrentDecision = currentWorkDecision(*w)
	raw, err := q.WorkResultView(ctx, w.ID)
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

func readWorkExecution(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) (*orchestration.WorkExecution, error) {
	raw, err := q.WorkExecutionView(ctx, sqlgen.WorkExecutionViewParams{ActiveTaskID: w.ActiveTaskID, WorkItemID: w.ID})
	if errors.Is(err, sql.ErrNoRows) && w.ActiveTaskID == "" {
		return nil, nil
	}
	if err != nil {
		return nil, missing(err, "The collaboration")
	}
	var execution orchestration.WorkExecution
	if err = json.Unmarshal([]byte(raw), &execution); err != nil {
		return nil, err
	}
	return &execution, nil
}

func currentWorkDecision(w orchestration.WorkItem) *orchestration.WorkCurrentDecision {
	if workTerminal(w) || w.WorkRecovery != nil {
		return nil
	}
	if delivery := w.GitHubDelivery; delivery != nil && (delivery.Status == "blocked" || delivery.Status == "unknown" || delivery.Reconcile) {
		return nil
	}
	if w.ActiveTaskID != "" {
		execution := w.Execution
		if execution == nil || execution.TaskID != w.ActiveTaskID || execution.Status != "waiting_for_person" || execution.ActiveRunID != "" {
			return nil
		}
		question := execution.Question
		if question == nil || question.Text == "" || question.Answer != "" {
			return nil
		}
		return &orchestration.WorkCurrentDecision{
			Kind: "task", Text: question.Text, ID: question.ID, Options: append([]string{}, question.Options...), TaskID: execution.TaskID, TaskRevision: execution.Revision,
		}
	}
	if w.Question == "" || w.Status == "needs_attention" {
		return nil
	}
	kind := orchestration.WorkQuestionJudgment
	if w.QuestionKind == orchestration.WorkQuestionCompletion {
		kind = orchestration.WorkQuestionCompletion
	}
	return &orchestration.WorkCurrentDecision{Kind: string(kind), Text: w.Question, Options: []string{}}
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
