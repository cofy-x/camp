package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// These public receipt fields deliberately exclude comment bodies and all
// credential references. The immutable original remains with its delivery.
type workActionReceipt struct {
	ActionID           string                          `json:"actionId"`
	DeliveryID         string                          `json:"deliveryId"`
	DeliveryRevision   int64                           `json:"deliveryRevision"`
	Kind               string                          `json:"kind"`
	TaskID             string                          `json:"taskId"`
	Status             string                          `json:"status"`
	Withdrawn          bool                            `json:"withdrawn,omitempty"`
	Uncertain          bool                            `json:"uncertain,omitempty"`
	Reason             string                          `json:"reason,omitempty"`
	CommentID          int64                           `json:"commentId,omitempty"`
	URL                string                          `json:"url,omitempty"`
	Attempts           []orchestration.DeliveryAttempt `json:"attempts,omitempty"`
	Receipts           []orchestration.DeliveryReceipt `json:"receipts,omitempty"`
	ReceiptScanLimited bool                            `json:"receiptScanLimited,omitempty"`
}

func workActionUncertain(d orchestration.Delivery) bool {
	if d.Status == "sent" || d.CommentID > 0 || len(d.Receipts) > 0 {
		return false
	}
	for _, attempt := range d.Attempts {
		if attempt.Outcome == "unknown" || attempt.Outcome == "sending" {
			return true
		}
	}
	return d.Status == "unknown"
}

func workActionReassessmentAllowed(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) bool {
	if workTerminal(w) || workExpired(w) || w.ActiveTaskID != "" || w.PolicyID == "" || w.Status == "waiting_for_person" || w.Status == "needs_attention" {
		return false
	}
	p, err := workPolicyForItem(ctx, q, w)
	return err == nil && p.Enabled && p.Mode == "auto" && p.OwnerUserID == w.OwnerUserID && automationAuthority(ctx, q, p) == nil
}

// A delivery outcome is a new immutable fact. Dispatch, receipts, fact and Inbox
// share the delivery transaction; a restart cannot lose or replay an effect.
// Operator lifecycle state stays intact: a late receipt cannot reopen a matter
// or erase a human judgment. Only a later authorized decision changes it.
func recordWorkActionResult(ctx context.Context, q *sqlgen.Queries, d orchestration.Delivery) error {
	switch d.Status {
	case "sent", "withdrawn", "unknown", "failed", "blocked":
	default:
		return nil
	}
	action, err := q.GetWorkActionForDelivery(ctx, d.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Older independent Tasks have no invented parent or Action.
	}
	if err != nil {
		return err
	}
	w, err := getWorkItem(ctx, q, action.WorkItemID)
	if err != nil {
		return err
	}
	receipt := workActionReceipt{ActionID: action.ID, DeliveryID: d.ID, Kind: action.Kind, TaskID: d.TaskID, Status: d.Status, Withdrawn: d.Withdrawn, Uncertain: workActionUncertain(d), Reason: truncateUTF8(d.Reason, 4096), CommentID: d.CommentID, URL: d.URL, Attempts: d.Attempts, Receipts: d.Receipts, ReceiptScanLimited: d.ReceiptScanLimited}
	semantic, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	hash := remoteHash(semantic)
	last, err := q.LatestWorkActionReceipt(ctx, d.ID)
	if err == nil && last.SemanticHash == hash {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	receipt.DeliveryRevision = d.Revision
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	status := "context"
	confirmed := (d.Status == "sent" || d.Status == "withdrawn") && !receipt.Uncertain
	if confirmed && workActionReassessmentAllowed(ctx, q, w) {
		status = "pending"
	}
	id := fmt.Sprintf("action:%s:%d", d.ID, d.Revision)
	ev := orchestration.SourceEvent{ID: id, RoomID: w.RoomID, SourceID: "action", ThreadKey: "workitem:" + w.ID, WorkItemID: w.ID, CausationID: d.ID, Kind: "action.result", ActorKind: "system", ActorID: "action", Content: string(raw), OccurredAt: now(), Status: status}
	if err = insertWorkFact(ctx, q, &w, ev); err != nil {
		return err
	}
	if err = q.InsertWorkActionReceipt(ctx, sqlgen.InsertWorkActionReceiptParams{WorkItemID: w.ID, DeliveryID: d.ID, DeliveryRevision: d.Revision, EventID: id, SemanticHash: hash, Value: string(raw), CreatedAt: ev.OccurredAt}); err != nil {
		return err
	}
	reason := "External action " + action.ID + " is " + d.Status + "."
	if err = appendWorkDecision(ctx, q, w, orchestration.WorkDecision{Kind: "action_result", Reason: reason, ActorKind: "system", ActorID: "action", TaskID: d.TaskID, EvidenceIDs: []string{id}}); err != nil {
		return err
	}
	if d.Status == "unknown" || d.Status == "failed" || d.Status == "blocked" || receipt.Uncertain {
		text := reason + " Review its delivery evidence and explicit recovery controls. No comment was replayed."
		if err = workNotice(ctx, q, &w, "work_item_attention", text); err != nil {
			return err
		}
	}
	return saveWorkItem(ctx, q, &w)
}

func candidateWorkSummary(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) (orchestration.WorkItemSummary, error) {
	summary := workSummary(w)
	frontier, err := workAssessmentFrontier(ctx, q, w)
	if err != nil {
		return summary, err
	}
	summary.Frontier = frontier
	rows, err := q.WorkActionSummaries(ctx, w.ID)
	if err != nil {
		return summary, err
	}
	summary.Actions = []orchestration.WorkAction{}
	summary.ActionsLimited = len(rows) > 20
	for index, action := range rows {
		if index == 20 {
			break
		}
		summary.Actions = append(summary.Actions, orchestration.WorkAction{ID: action.ID, Kind: action.Kind, Status: action.CurrentStatus, TaskID: action.TaskID, DeliveryID: action.DeliveryID, CreatedAt: action.CreatedAt})
	}
	// Timers and Action facts have their own source lane, so their assessment
	// cannot rely on a Thread-local task lookup to recover the latest result.
	tasks, err := q.WorkTasks(ctx, w.ID)
	if err != nil {
		return summary, err
	}
	summary.ContextLimited = len(tasks) > 3
	for index, task := range tasks {
		if index == 3 {
			break
		}
		text := truncateUTF8(task.Summary, 2048)
		if text != task.Summary {
			summary.ContextLimited = true
		}
		summary.RecentTasks = append(summary.RecentTasks, orchestration.WorkTask{ID: task.TaskID, Status: task.Status, Summary: text, CreatedAt: task.CreatedAt})
	}
	decisions, err := q.WorkDecisions(ctx, sqlgen.WorkDecisionsParams{WorkItemID: w.ID, Seq: 0, Limit: 4})
	if err != nil {
		return summary, err
	}
	if len(decisions) > 3 {
		summary.ContextLimited = true
	}
	for index, row := range decisions {
		if index == 3 {
			break
		}
		var decision orchestration.WorkDecision
		if err = json.Unmarshal([]byte(row.Value), &decision); err != nil {
			return summary, err
		}
		decision.Seq = row.Seq
		text := truncateUTF8(decision.Reason, 1024)
		if text != decision.Reason {
			summary.ContextLimited = true
		}
		decision.Reason = text
		summary.RecentDecisions = append(summary.RecentDecisions, decision)
	}
	return summary, nil
}

func workCompletionPending(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) (bool, error) {
	count, err := q.WorkPendingActions(ctx, w.ID)
	return count > 0, err
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
		w.Status, w.Question = "waiting_for_person", "Confirm whether this matter is complete: "+truncateUTF8(w.Goal, 512)
		w.QuestionKind = orchestration.WorkQuestionCompletion
		if err = q.CancelWorkTimers(ctx, w.ID); err != nil {
			return false, err
		}
		if err = workNotice(ctx, q, w, "work_item_attention", w.Question); err != nil {
			return false, err
		}
		return false, nil
	}
	pending, err := workCompletionPending(ctx, q, *w)
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
