package storage

import (
	"context"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// Publication accepts the finite task's result before this judgment can affect
// its parent. A successful Run alone grants no completion authority.
func applyTaskWorkCompletion(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, task CollaborationTask, evidenceID string) error {
	judgment := task.WorkItemCompletion
	if !judgment.CriteriaMet {
		if w.PolicyID != "" {
			return nil
		}
		w.Status = "waiting_for_person"
		w.QuestionKind = orchestration.WorkQuestionJudgment
		w.Question = "How should this work continue? " + judgment.Reason
		return workNotice(ctx, q, w, "work_item_attention", w.Question)
	}
	completed, err := completeWorkItem(ctx, q, w, judgment.Reason)
	if err != nil {
		return err
	}
	kind := "complete"
	if !completed {
		kind = "completion_pending"
	}
	if err = appendWorkDecision(ctx, q, *w, orchestration.WorkDecision{Kind: kind, Reason: judgment.Reason, ActorKind: "agent", ActorID: task.CurrentAgentID, TaskID: task.ID, EvidenceIDs: []string{evidenceID}}); err != nil {
		return err
	}
	if completed {
		w.PendingCompletion = nil
		return nil
	}
	w.PendingCompletion = &orchestration.PendingWorkCompletion{TaskID: task.ID, Reason: judgment.Reason}
	return advanceWorkCompletionFrontier(ctx, q, w)
}

func advanceWorkCompletionFrontier(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	evidence, err := q.WorkHistoryEvidenceFrontier(ctx, w.ID)
	if err != nil {
		return err
	}
	decisions, err := q.WorkHistoryDecisionsFrontier(ctx, w.ID)
	if err != nil {
		return err
	}
	w.PendingCompletion.EvidenceThrough = evidence
	w.PendingCompletion.DecisionsThrough = decisions
	return nil
}

// Only receipt facts may advance a pending judgment. Human commands, new
// context, expiry and stop intent prevent a late receipt from closing the work.
func pendingWorkCompletionCurrent(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) (bool, error) {
	if w.PendingCompletion == nil || w.Status != "waiting" || w.StopRequested || w.Question != "" || w.ActiveTaskID != "" || workExpired(*w) {
		return false, nil
	}
	owner, err := q.GetUser(ctx, w.OwnerUserID)
	if err != nil {
		return false, err
	}
	if !owner.Enabled {
		return false, nil
	}
	evidence, err := q.WorkHistoryEvidenceFrontier(ctx, w.ID)
	if err != nil {
		return false, err
	}
	decisions, err := q.WorkHistoryDecisionsFrontier(ctx, w.ID)
	if err != nil {
		return false, err
	}
	current := evidence == w.PendingCompletion.EvidenceThrough && decisions == w.PendingCompletion.DecisionsThrough
	if !current {
		w.PendingCompletion = nil
	}
	return current, nil
}

func requestWorkCompletionConfirmation(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	w.Status, w.Question = "waiting_for_person", "Is this work complete?"
	w.QuestionKind = orchestration.WorkQuestionCompletion
	if err := q.CancelWorkTimers(ctx, w.ID); err != nil {
		return err
	}
	return workNotice(ctx, q, w, "work_item_attention", w.Question)
}

func workCompletionRunnerCompatible(raw string, authorized bool) error {
	if !authorized {
		return nil
	}
	var capabilities struct {
		Version int `json:"workItemCompletionVersion"`
	}
	if json.Unmarshal([]byte(raw), &capabilities) != nil || capabilities.Version != 1 {
		return problem("runner_unavailable", "Update the selected Runner to support automatic Work completion, or choose human confirmation.")
	}
	return nil
}
