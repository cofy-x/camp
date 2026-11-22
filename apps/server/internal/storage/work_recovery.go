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

const (
	recoveryEvaluations = "evaluations"
	recoveryExecution   = "execution"
	recoveryFollowUps   = "follow_ups"
)

func canShowWorkRecovery(w orchestration.WorkItem) bool {
	return w.Status == "needs_attention" && !workTerminal(w) && w.ActiveTaskID == ""
}

func workFailureRecovery(w orchestration.WorkItem, reason string) orchestration.WorkRecovery {
	if workExpired(w) || reason == "authorization_expired" {
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryRenewAuthorization, Message: "This Work’s authorization expired."}
	}
	switch reason {
	case "policy_not_applicable", "authorization_unavailable", "owning_policy_not_automatic":
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryReviewRule, Message: "The linked rule could not process this follow-up."}
	case "daily_evaluation_limit":
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryDailyLimit, Message: "The rule reached its daily assessment limit."}
	case "daily_task_limit":
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryDailyLimit, Message: "The rule reached its daily task limit."}
	case "work_budget_exhausted":
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryIncreaseAllowance, Message: "This Work reached its authorized allowance."}
	case "runner_unavailable":
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryReviewActivity, Message: "A Runner could not start the proposed work."}
	case "assessment_reference_limit", "assessment_context_limit", "assessment_related_work_limit":
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryReviewActivity, Message: "The assessment exceeded its allowed context."}
	case "stale_assessment":
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryReviewActivity, Message: "Work or its rule changed while the assessment was running."}
	default:
		return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryReviewActivity, Message: "The follow-up could not continue. Review its recorded assessment."}
	}
}

func markWorkRecovery(w *orchestration.WorkItem, recovery orchestration.WorkRecovery, limit string) {
	w.Status, w.Question, w.QuestionKind = "needs_attention", "", ""
	w.WorkRecovery = &recovery
	w.RecoveryLimit = limit
}

func markWorkFailure(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, reason, limit string) error {
	recovery := workFailureRecovery(*w, reason)
	if w.PolicyID != "" && (recovery.Kind == orchestration.WorkRecoveryReviewRule || recovery.Kind == orchestration.WorkRecoveryDailyLimit) {
		p, err := q.GetAutomationPolicyID(ctx, w.PolicyID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && p.RoomID == w.RoomID && p.OwnerUserID == w.OwnerUserID {
			recovery.SourceID = p.SourceID
		}
	}
	if recovery.Kind != orchestration.WorkRecoveryIncreaseAllowance {
		limit = ""
	}
	markWorkRecovery(w, recovery, limit)
	return nil
}

func workTaskRecovery(taskID string) orchestration.WorkRecovery {
	return orchestration.WorkRecovery{Kind: orchestration.WorkRecoveryReviewExecution, Message: "The last work session ended before completing this Work.", TaskID: taskID}
}

func recoveryAllowanceRestored(w orchestration.WorkItem, kind orchestration.WorkRecoveryKind, limit string, previous orchestration.WorkBudget, deadlineChanged bool) bool {
	if workExpired(w) {
		return false
	}
	if kind == orchestration.WorkRecoveryRenewAuthorization {
		return deadlineChanged
	}
	if kind != orchestration.WorkRecoveryIncreaseAllowance {
		return false
	}
	switch limit {
	case recoveryEvaluations:
		return w.Budget.MaxEvaluations > previous.MaxEvaluations && w.Budget.MaxEvaluations > w.Usage.Evaluations
	case recoveryExecution:
		return (w.Budget.MaxTasks > previous.MaxTasks || w.Budget.MaxModelRequests > previous.MaxModelRequests) &&
			w.Budget.MaxTasks > w.Usage.Tasks && w.Budget.MaxModelRequests > w.Usage.ReservedModelRequests
	case recoveryFollowUps:
		return w.Budget.MaxFollowUps > previous.MaxFollowUps && w.Budget.MaxFollowUps > w.Usage.FollowUps
	default:
		return false
	}
}

func assessmentRecoveryLimit(w orchestration.WorkItem, targeted bool, outcome string) string {
	if !targeted && w.Usage.Evaluations >= w.Budget.MaxEvaluations {
		return recoveryEvaluations
	}
	switch outcome {
	case "accept":
		return recoveryExecution
	case "wait":
		return recoveryFollowUps
	default:
		return ""
	}
}

// Retained synthetic questions are recognized only with the latest matching
// system decision. Preserve human text and avoid guessing from a prefix alone.
func legacyWorkRecovery(w orchestration.WorkItem, raw, taskStatus, sourceID string) (*orchestration.WorkRecovery, string, error) {
	if !canShowWorkRecovery(w) || raw == "" {
		return nil, "", nil
	}
	var d orchestration.WorkDecision
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, "", err
	}
	if d.ActorKind != "system" || len(d.EvidenceIDs) == 0 {
		return nil, "", nil
	}
	var recovery orchestration.WorkRecovery
	switch {
	case d.Kind == "result" && d.ActorID == "task" && w.Question == "" && d.TaskID != "" &&
		(taskStatus == "interrupted" || taskStatus == "cancelled"):
		recovery = workTaskRecovery(d.TaskID)
	case d.Kind == "request_judgment" && d.ActorID == "timer" &&
		w.Question == d.Reason && strings.HasPrefix(d.EvidenceIDs[0], "deadline:"+w.ID+":"):
		recovery = workFailureRecovery(w, "authorization_expired")
	case d.Kind == "request_judgment" && d.ActorID == "intake" &&
		w.Question == "Review the follow-up before further autonomous work: "+d.Reason:
		recovery = workFailureRecovery(w, d.Reason)
	case d.Kind == "request_judgment" && d.ActorID == "assessment" && d.EvaluationID != "" &&
		w.Question == "Review the recorded event assessment: "+d.Reason:
		recovery = workFailureRecovery(w, d.Reason)
	default:
		return nil, "", nil
	}
	if recovery.Kind == orchestration.WorkRecoveryReviewRule || recovery.Kind == orchestration.WorkRecoveryDailyLimit {
		recovery.SourceID = sourceID
	}
	return &recovery, d.ActorID, nil
}

func currentWorkRecovery(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) (*orchestration.WorkRecovery, string, error) {
	if !canShowWorkRecovery(w) {
		return nil, "", nil
	}
	if w.WorkRecovery != nil && w.Question == "" {
		return w.WorkRecovery, w.RecoveryLimit, nil
	}
	row, err := q.LegacyWorkRecovery(ctx, w.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	recovery, actor, err := legacyWorkRecovery(w, row.Decision, row.TaskStatus, row.SourceID)
	limit := ""
	if recovery != nil && recovery.Kind == orchestration.WorkRecoveryIncreaseAllowance {
		if actor == "intake" {
			limit = recoveryEvaluations
		} else if actor == "assessment" && row.AssessmentOutcome != "" &&
			(row.Targeted != 0 || row.AssessedWorkItemID == w.ID) {
			limit = assessmentRecoveryLimit(w, row.Targeted != 0, row.AssessmentOutcome)
		}
	}
	return recovery, limit, err
}

func populateWorkRecovery(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	// The exact active task and delivery continue to own their existing recovery.
	if delivery := w.GitHubDelivery; delivery != nil && (delivery.Status == "blocked" || delivery.Status == "unknown" || delivery.Reconcile) {
		w.WorkRecovery = nil
		return nil
	}
	recovery, _, err := currentWorkRecovery(ctx, q, *w)
	if err != nil {
		return err
	}
	w.WorkRecovery = recovery
	if recovery != nil {
		w.Question, w.QuestionKind = "", ""
	}
	return nil
}
