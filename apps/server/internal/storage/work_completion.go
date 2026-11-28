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
	if err := prepareGitHubDelivery(ctx, q, w, task); err != nil {
		return err
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
	if err := populateGitHubDelivery(ctx, q, w); err != nil {
		return err
	}
	if w.GitHubDelivery != nil && w.GitHubDelivery.Status == "blocked" {
		w.Status = "needs_attention"
		w.WaitCondition = w.GitHubDelivery.Reason
		if err := workNotice(ctx, q, w, "work_item_attention", w.WaitCondition); err != nil {
			return err
		}
	}
	return retainWorkCompletion(ctx, q, w, task.ID, judgment.Reason)
}

// Retain the accepted judgment after its audit decision has committed to the
// transaction. The receipt owner can then finish it against this frontier.
func retainWorkCompletion(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, taskID, reason string) error {
	w.PendingCompletion = &orchestration.PendingWorkCompletion{TaskID: taskID, Reason: reason}
	return advanceWorkCompletionFrontier(ctx, q, w)
}

func advanceWorkCompletionFrontier(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	frontier, err := q.WorkCompletionFrontier(ctx, sqlgen.WorkCompletionFrontierParams{WorkItemID: w.ID, OwnerUserID: w.OwnerUserID})
	if err != nil {
		return err
	}
	w.PendingCompletion.EvidenceThrough = frontier.EvidenceThrough
	w.PendingCompletion.DecisionsThrough = frontier.DecisionsThrough
	return nil
}

// Only receipt facts may advance a pending judgment. Human commands, new
// context, expiry and stop intent prevent a late receipt from closing the work.
// Discard a superseded judgment when a writer persists the checked state.
func validatePendingWorkCompletion(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) (bool, error) {
	if w.PendingCompletion == nil || w.Status != "waiting" || w.StopRequested || w.Question != "" || w.ActiveTaskID != "" || workExpired(*w) {
		return false, nil
	}
	frontier, err := q.WorkCompletionFrontier(ctx, sqlgen.WorkCompletionFrontierParams{WorkItemID: w.ID, OwnerUserID: w.OwnerUserID})
	if err != nil || !frontier.OwnerEnabled {
		return false, err
	}
	current := frontier.EvidenceThrough == w.PendingCompletion.EvidenceThrough && frontier.DecisionsThrough == w.PendingCompletion.DecisionsThrough
	if !current {
		w.PendingCompletion = nil
	}
	return current, nil
}

// Apply a receipt to the judgment that was current before the receipt was
// recorded. Remaining deliveries keep that judgment at the updated frontier.
func settleWorkCompletionReceipt(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, evidenceID string, confirmed bool) error {
	if confirmed {
		proposal := w.PendingCompletion
		completed, err := closeWorkWhenDelivered(ctx, q, w, proposal.Reason)
		if err != nil {
			return err
		}
		if completed {
			w.PendingCompletion = nil
			return appendWorkDecision(ctx, q, *w, orchestration.WorkDecision{Kind: "complete", Reason: proposal.Reason, ActorKind: "system", ActorID: "confirmed-receipts", TaskID: proposal.TaskID, EvidenceIDs: []string{evidenceID}})
		}
	}
	return advanceWorkCompletionFrontier(ctx, q, w)
}

func hasUnsettledWorkDeliveries(ctx context.Context, q *sqlgen.Queries, workID string) (bool, error) {
	count, err := q.WorkPendingActions(ctx, workID)
	if err != nil {
		return false, err
	}
	deliveries, err := q.GitHubDeliveryUnsettled(ctx, workID)
	return count > 0 || deliveries > 0, err
}

// Completion remains a decision about the matter, with confirmed effects as a
// deterministic precondition. Human and model callers share this rule. Callers
// persist w and record whether completion succeeded or waits for action receipts.
func completeWorkItem(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, reason string) (bool, error) {
	current, err := getWorkItem(ctx, q, w.ID)
	if err != nil {
		return false, err
	}
	if current.Revision != w.Revision {
		return false, problem("revision_conflict", "The matter changed before completion.")
	}
	if workTerminal(current) {
		return false, problem("work_item_finished", "This matter has already finished.")
	}
	if w.ActiveTaskID != "" {
		return false, problem("task_busy", "Finish or cancel the active collaboration before completing its matter.")
	}
	if _, human := UserFromContext(ctx); !human && (w.CompletionMode == "human" || w.CompletionCriteria == "") {
		return false, requestWorkCompletionConfirmation(ctx, q, w)
	}
	return closeWorkWhenDelivered(ctx, q, w, reason)
}

// Both callers have checked the judgment and the current Work in this
// transaction. The final Work save also checks its expected revision.
func closeWorkWhenDelivered(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, reason string) (bool, error) {
	pending, err := hasUnsettledWorkDeliveries(ctx, q, w.ID)
	if err != nil {
		return false, err
	}
	w.Question = ""
	w.QuestionKind = ""
	if err = q.CancelWorkTimers(ctx, w.ID); err != nil {
		return false, err
	}
	if pending {
		w.Status, w.WaitCondition = "waiting", "Waiting for confirmed external action delivery receipts."
		return false, nil
	}
	w.Status, w.WaitCondition = "completed", ""
	if err = workNotice(ctx, q, w, "work_item_finished", reason); err != nil {
		return false, err
	}
	return true, nil
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
