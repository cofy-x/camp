package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func validAssessment(a orchestration.Assessment) bool { return orchestration.ValidAssessment(a) }

func selectAssessmentWorkItem(ctx context.Context, q *sqlgen.Queries, c orchestration.Candidate, a *orchestration.Assessment) (orchestration.WorkItem, error) {
	// Internal facts have a durable target; a model cannot redirect them into a
	// new matter with a fresh allowance.
	for _, ev := range c.Events {
		if ev.WorkItemID != "" {
			if a.WorkItemID != "" && a.WorkItemID != ev.WorkItemID {
				return orchestration.WorkItem{}, problem("invalid_work_item", "The decision cannot redirect a targeted follow-up.")
			}
			a.WorkItemID = ev.WorkItemID
		}
	}
	if a.WorkItemID != "" {
		found := false
		for _, w := range c.WorkItems {
			if w.ID == a.WorkItemID {
				found = true
			}
		}
		if !found {
			return orchestration.WorkItem{}, problem("invalid_work_item", "Choose a matter in the frozen decision evidence.")
		}
		w, e := getWorkItem(ctx, q, a.WorkItemID)
		if e != nil {
			return w, e
		}
		if workTerminal(w) || workExpired(w) || w.PolicyID != c.Policy.ID || w.OwnerUserID != c.Policy.OwnerUserID {
			return w, problem("work_item_finished", "The matter no longer accepts this delegation.")
		}
		if w.Status == "needs_attention" || w.Status == "waiting_for_person" {
			return w, problem("invalid_work_item", "This matter requires an explicit human judgment before autonomous work.")
		}
		targeted := false
		for _, ev := range c.Events {
			targeted = targeted || ev.WorkItemID == w.ID
		}
		if !targeted {
			if w.Usage.Evaluations >= w.Budget.MaxEvaluations {
				return w, problem("work_budget_exhausted", "The matter's assessment allowance is exhausted.")
			}
			w.Usage.Evaluations++
		}
		for _, event := range c.Events {
			if w.DelegationStartAfter > 0 && event.Seq <= w.DelegationStartAfter {
				return w, problem("invalid_work_item", "This evidence predates the matter's current automatic delegation.")
			}
		}
		return w, nil
	}
	if a.Outcome == "complete" || a.Outcome == "ignore" {
		return orchestration.WorkItem{}, problem("invalid_work_item", "Choose an existing matter for this decision.")
	}
	criteria := a.CompletionCriteria
	ids := []string{}
	for _, ev := range c.Events {
		ids = append(ids, ev.ID)
	}
	w, e := insertWorkItem(ctx, q, c.Policy.RoomID, c.Policy.OwnerUserID, orchestration.WorkItemInput{Goal: a.Goal, CompletionCriteria: criteria, PolicyID: c.Policy.ID, EvidenceIDs: ids, Budget: defaultWorkBudget(c.Policy.ItemBudget)})
	if e != nil {
		return w, e
	}
	w.Usage.Evaluations = 1
	a.WorkItemID = w.ID
	for _, ev := range c.Events {
		if ev.SourceID == c.Policy.SourceID {
			if e = q.SaveWorkWatch(ctx, sqlgen.SaveWorkWatchParams{WorkItemID: w.ID, SourceID: ev.SourceID, ThreadKey: ev.ThreadKey, Enabled: 1}); e != nil {
				return w, e
			}
		}
	}
	return w, nil
}

// A rejected proposal rolls back only its tentative effects. The assessment
// itself can then commit as needs attention without spending another model call.
func applyAutomationWorkDecision(ctx context.Context, tx *sql.Tx, c orchestration.Candidate, a *orchestration.Assessment) (taskID string, err error) {
	if _, err = tx.ExecContext(ctx, "SAVEPOINT work_decision"); err != nil {
		return
	}
	original := *a
	taskID, err = applyAutomationWorkDecisionTx(ctx, tx, c, a)
	if err != nil {
		*a = original
		_, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO work_decision")
		_, releaseErr := tx.ExecContext(ctx, "RELEASE work_decision")
		return "", workDecisionRejection(err, rollbackErr, releaseErr)
	}
	_, err = tx.ExecContext(ctx, "RELEASE work_decision")
	return
}
func applyAutomationWorkDecisionTx(ctx context.Context, tx *sql.Tx, c orchestration.Candidate, a *orchestration.Assessment) (string, error) {
	q := sqlgen.New(tx)
	w, e := selectAssessmentWorkItem(ctx, q, c, a)
	if e != nil {
		return "", e
	}
	ids := []string{}
	for _, ev := range c.Events {
		ids = append(ids, ev.ID)
	}
	if e = linkWorkEvidence(ctx, q, &w, ids); e != nil {
		return "", e
	}
	d := orchestration.WorkDecision{Kind: a.Outcome, Reason: a.Reason, ActorKind: "system", ActorID: "assessment", EvaluationID: c.ID, PolicyRevision: c.Policy.Revision, EvidenceIDs: ids}
	switch a.Outcome {
	case "accept":
		if w.ActiveTaskID != "" {
			return "", problem("task_busy", "An existing collaboration already owns this matter's execution.")
		}
		if w.Usage.Tasks >= w.Budget.MaxTasks || w.Usage.ReservedModelRequests >= w.Budget.MaxModelRequests {
			return "", problem("work_budget_exhausted", "The work item's cumulative allowance is exhausted.")
		}
		if a.Goal == "" {
			a.Goal = w.Goal
		}
		c.Policy.MaxModelRequests = min(c.Policy.MaxModelRequests, w.Budget.MaxModelRequests-w.Usage.ReservedModelRequests)
		t, e := createAutomationTask(ctx, tx, c, *a, &w)
		if e != nil {
			return "", e
		}
		d.TaskID = t.ID
	case "wait":
		if w.ActiveTaskID != "" {
			return "", problem("task_busy", "Wait for the current collaboration before scheduling a follow-up.")
		}
		w.Status = "waiting"
		w.WaitCondition = a.WaitCondition
		w.Question = ""
		w.QuestionKind = ""
		if e = q.CancelWorkTimers(ctx, w.ID); e != nil {
			return "", e
		}
		if a.ResumeAt != "" {
			due, e := workTime(a.ResumeAt)
			if e != nil {
				return "", e
			}
			t, _ := time.Parse(time.RFC3339Nano, due)
			deadline, _ := time.Parse(time.RFC3339Nano, w.Deadline)
			if !t.After(time.Now()) || t.After(deadline) {
				return "", problem("invalid_work_item", "Follow up after now and before the matter's deadline.")
			}
			if w.Usage.FollowUps >= w.Budget.MaxFollowUps {
				return "", problem("work_budget_exhausted", "The matter's follow-up allowance is exhausted.")
			}
			if e = q.InsertWorkTimer(ctx, sqlgen.InsertWorkTimerParams{ID: newID(), WorkItemID: w.ID, DueAt: due, Condition: a.WaitCondition, CreatedAt: now()}); e != nil {
				return "", e
			}
		}
	case "request_judgment":
		if w.ActiveTaskID != "" {
			return "", problem("task_busy", "The current collaboration still owns execution.")
		}
		w.Status = "waiting_for_person"
		w.Question = a.Question
		w.QuestionKind = orchestration.WorkQuestionJudgment
		if e = q.CancelWorkTimers(ctx, w.ID); e != nil {
			return "", e
		}
		if e = workNotice(ctx, q, &w, "work_item_attention", a.Question); e != nil {
			return "", e
		}
	case "complete":
		if w.ActiveTaskID != "" {
			return "", problem("task_busy", "An executing matter cannot be completed.")
		}
		completed, e := completeWorkItem(ctx, q, &w, a.Reason)
		if e != nil {
			return "", e
		}
		if !completed {
			if w.Status == "waiting_for_person" {
				d.Kind = "request_judgment"
				d.Reason = "Human confirmation is required before completing this matter. " + a.Reason
			} else {
				d.Kind = "wait"
				d.Reason = "Completion deferred until external action receipts are confirmed. " + a.Reason
			}
		}
	case "ignore":
	default:
		return "", problem("invalid_work_item", "Unsupported advisory decision.")
	}
	if e = appendWorkDecision(ctx, q, w, d); e != nil {
		return "", e
	}
	if e = saveWorkItem(ctx, q, &w); e != nil {
		return "", e
	}
	return d.TaskID, nil
}

func recordAssessmentAttention(ctx context.Context, q *sqlgen.Queries, c orchestration.Candidate, a orchestration.Assessment, reason string) error {
	ids := []string{}
	for _, ev := range c.Events {
		ids = append(ids, ev.ID)
	}
	items := c.WorkItems
	if len(items) == 0 {
		w, e := insertWorkItem(ctx, q, c.Policy.RoomID, c.Policy.OwnerUserID, orchestration.WorkItemInput{Goal: "Review incoming events", CompletionCriteria: "Record a judgment on the linked source events.", EvidenceIDs: ids})
		if e != nil {
			return e
		}
		items = []orchestration.WorkItemSummary{workSummary(w)}
	}
	for _, summary := range items {
		w, e := getWorkItem(ctx, q, summary.ID)
		if e != nil {
			return e
		}
		if workTerminal(w) || w.ActiveTaskID != "" {
			continue
		}
		if reason == "stale_assessment" {
			superseded, err := workAssessmentSuperseded(ctx, q, c, w)
			if err != nil {
				return err
			}
			if superseded {
				if e = appendWorkDecision(ctx, q, w, orchestration.WorkDecision{Kind: "superseded", Reason: "Newer targeted evidence is pending under the same current delegation. This assessment was retained without applying its proposal.", ActorKind: "system", ActorID: "assessment", EvaluationID: c.ID, PolicyRevision: c.Policy.Revision, EvidenceIDs: ids}); e != nil {
					return e
				}
				if e = event(ctx, q, "work_item.updated", w.RoomID, w.ID); e != nil {
					return e
				}
				continue
			}
		}
		if w.Status != "waiting_for_person" {
			w.Status = "needs_attention"
			w.Question = "Review the recorded event assessment: " + reason
			w.QuestionKind = orchestration.WorkQuestionJudgment
		}
		if e = appendWorkDecision(ctx, q, w, orchestration.WorkDecision{Kind: "request_judgment", Reason: reason, ActorKind: "system", ActorID: "assessment", EvaluationID: c.ID, PolicyRevision: c.Policy.Revision, EvidenceIDs: ids}); e != nil {
			return e
		}
		if e = workNotice(ctx, q, &w, "work_item_attention", w.Question); e != nil {
			return e
		}
		if e = saveWorkItem(ctx, q, &w); e != nil {
			return e
		}
	}
	return nil
}

// A changed matter frontier can reflect ordinary new source evidence. Its new
// targeted fact may spend a fresh assessment slot; the obsolete provider result
// remains audit evidence and is never replayed. Human/configuration changes and
// lost source or execution authority still require judgment.
func workAssessmentSuperseded(ctx context.Context, q *sqlgen.Queries, c orchestration.Candidate, w orchestration.WorkItem) (bool, error) {
	if c.EventFrontier < 1 || (w.Status != "open" && w.Status != "waiting") || workExpired(w) || w.PolicyID != c.Policy.ID || w.OwnerUserID != c.Policy.OwnerUserID {
		return false, nil
	}
	var decisionFrontier int64
	for _, summary := range c.WorkItems {
		if summary.ID != w.ID {
			continue
		}
		for _, decision := range summary.RecentDecisions {
			if decision.Seq > decisionFrontier {
				decisionFrontier = decision.Seq
			}
		}
	}
	if decisionFrontier == 0 {
		return false, nil
	}
	humanChanges, err := q.HasHumanWorkDecisionAfter(ctx, sqlgen.HasHumanWorkDecisionAfterParams{WorkItemID: w.ID, AfterSeq: decisionFrontier})
	if err != nil {
		return false, err
	}
	if humanChanges != 0 {
		return false, nil
	}
	row, err := q.GetAutomationPolicyID(ctx, c.Policy.ID)
	if err != nil {
		return false, err
	}
	p, err := automationPolicy(row)
	if err != nil {
		return false, err
	}
	if !p.Enabled || p.Mode != "auto" || p.Revision != c.Policy.Revision || automationAuthority(ctx, q, p) != nil {
		return false, nil
	}
	for _, ev := range c.Events {
		current, err := q.GetAutomationEvent(ctx, ev.ID)
		if err != nil {
			return false, err
		}
		if current.Status != "evaluating" || current.EvaluationID != c.ID {
			return false, nil
		}
		if ev.WorkItemID != "" {
			policy, err := workEventPolicy(ctx, q, ev)
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if policy.ID != p.ID {
				return false, nil
			}
		}
	}
	pending, err := q.HasPendingWorkFollowUp(ctx, sqlgen.HasPendingWorkFollowUpParams{AfterSeq: c.EventFrontier, WorkItemID: w.ID})
	if err != nil {
		return false, err
	}
	for _, r := range pending {
		ev, err := automationEvent(r)
		if err != nil {
			return false, err
		}
		policy, err := workEventPolicy(ctx, q, ev)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if policy.ID == p.ID {
			return true, nil
		}
	}
	return false, nil
}

// Recovery failure is an outer transaction failure. It must not retain the
// advisory rejection's *Error type, which completion treats as recoverable.
func workDecisionRejection(rejection, rollbackErr, releaseErr error) error {
	if recoveryErr := errors.Join(rollbackErr, releaseErr); recoveryErr != nil {
		return fmt.Errorf("restore automation decision savepoint: %w", recoveryErr)
	}
	return rejection
}
