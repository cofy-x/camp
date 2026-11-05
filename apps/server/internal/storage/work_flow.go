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

func attachWorkTask(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, t *CollaborationTask, reserve int) error {
	if workTerminal(*w) || workExpired(*w) || w.ActiveTaskID != "" || w.Usage.Tasks >= w.Budget.MaxTasks || reserve < 1 || w.Usage.ReservedModelRequests+reserve > w.Budget.MaxModelRequests {
		return problem("work_budget_exhausted", "The work item cannot authorize another finite collaboration.")
	}
	if e := q.InsertWorkTask(ctx, sqlgen.InsertWorkTaskParams{WorkItemID: w.ID, TaskID: t.ID, ReservedRequests: int64(reserve), CreatedAt: now()}); e != nil {
		return e
	}
	t.WorkItemID = w.ID
	t.WorkItemCompletionCriteria = w.CompletionCriteria
	evs, e := q.WorkExecutionEvidence(ctx, w.ID)
	if e != nil {
		return e
	}
	if len(evs) > 1000 {
		return problem("invalid_work_item", "The work item evidence exceeds this execution's bounded input. Review it before authorizing another collaboration.")
	}
	bytes := 0
	for _, ref := range evs {
		r, e := q.GetAutomationEvent(ctx, ref.EventID)
		if e != nil {
			return e
		}
		ev, e := workEvidenceEvent(ctx, q, r)
		if e != nil {
			return e
		}
		bytes += len(ev.Content)
		if bytes > 8<<20 {
			return problem("invalid_work_item", "The work item's evidence exceeds the 8 MiB execution intake bound. Originals remain available in history.")
		}
		if _, e = appendAutomationTaskEvent(ctx, q, t, ev); e != nil {
			return e
		}
	}
	w.Usage.Tasks++
	w.Usage.ReservedModelRequests += reserve
	w.ActiveTaskID = t.ID
	w.Status = "running"
	w.Question = ""
	w.QuestionKind = ""
	w.WaitCondition = ""
	if e := inheritWorkWatches(ctx, q, *w, *t); e != nil {
		return e
	}
	return q.CancelWorkTimers(ctx, w.ID)
}

func authorizeWorkTask(ctx context.Context, q *sqlgen.Queries, t CollaborationTask) error {
	if t.WorkItemID == "" {
		return nil
	}
	w, e := getWorkItem(ctx, q, t.WorkItemID)
	if e != nil {
		return e
	}
	if workTerminal(w) || workExpired(w) || w.ActiveTaskID != t.ID {
		return problem("work_budget_exhausted", "This work item no longer authorizes execution.")
	}
	return nil
}

func chargeWorkTaskModel(ctx context.Context, q *sqlgen.Queries, t CollaborationTask) error {
	if t.WorkItemID == "" {
		return nil
	}
	w, e := getWorkItem(ctx, q, t.WorkItemID)
	if e != nil {
		return e
	}
	if workTerminal(w) || workExpired(w) || w.ActiveTaskID != t.ID || w.Usage.ModelRequests >= w.Budget.MaxModelRequests {
		return problem("work_budget_exhausted", "The work item's cumulative model allowance is exhausted.")
	}
	w.Usage.ModelRequests++
	return saveWorkItem(ctx, q, &w)
}

func chargeWorkAction(ctx context.Context, q *sqlgen.Queries, t CollaborationTask) error {
	if t.WorkItemID == "" {
		return nil
	}
	w, e := getWorkItem(ctx, q, t.WorkItemID)
	if e != nil {
		return e
	}
	if w.Status == "cancelled" || workExpired(w) || w.Usage.Actions >= w.Budget.MaxActions {
		return problem("work_budget_exhausted", "The matter no longer permits this external action.")
	}
	w.Usage.Actions++
	return saveWorkItem(ctx, q, &w)
}

func reserveWorkContinuation(ctx context.Context, q *sqlgen.Queries, t CollaborationTask, extra int) error {
	if t.WorkItemID == "" || extra == 0 {
		return nil
	}
	w, e := getWorkItem(ctx, q, t.WorkItemID)
	if e != nil {
		return e
	}
	if workTerminal(w) || workExpired(w) || w.ActiveTaskID != t.ID || w.Usage.ReservedModelRequests+extra > w.Budget.MaxModelRequests {
		return problem("work_budget_exhausted", "The continuation exceeds the work item's cumulative allowance.")
	}
	w.Usage.ReservedModelRequests += extra
	return saveWorkItem(ctx, q, &w)
}

// A task result is new evidence. It never closes a matter or restores a grant.
func syncWorkItemTask(ctx context.Context, q *sqlgen.Queries, t CollaborationTask) error {
	if t.WorkItemID == "" || (t.Status != "completed" && t.Status != "cancelled" && t.Status != "interrupted") {
		return nil
	}
	link, e := q.WorkItemForTask(ctx, t.ID)
	if e != nil {
		return e
	}
	if link.ResultEventID != "" {
		return nil
	}
	w, e := getWorkItem(ctx, q, link.WorkItemID)
	if e != nil {
		return e
	}
	stopping := w.StopRequested || w.Status == "stopping"
	terminal := w.Status == "completed" || w.Status == "cancelled"
	w.ActiveTaskID = ""
	eligible := !stopping && !terminal && t.Status == "completed" && !workExpired(w) && w.PolicyID != ""
	if stopping {
		w.Status = "cancelled"
		w.Question, w.WaitCondition, w.QuestionKind = "", "", ""
	} else if !terminal {
		w.Status = "open"
		if t.Status != "completed" {
			w.Status = "needs_attention"
		}
	}
	id := "task-result:" + t.ID
	content := fmt.Sprintf("Collaboration %s ended with status %s.\n%s\n%s\nPublished version: %s", t.ID, t.Status, t.Summary, t.Error, t.PublishedCommit)
	ev := orchestration.SourceEvent{ID: id, RoomID: w.RoomID, SourceID: "task", ThreadKey: "workitem:" + w.ID, WorkItemID: w.ID, CausationID: t.ID, Kind: "task.result", ActorKind: "system", ActorID: "task", Content: content, OccurredAt: now(), Status: "context"}
	if eligible {
		ev.Status = "pending"
	}
	if e = insertWorkFact(ctx, q, &w, ev); e != nil {
		return e
	}
	if e = q.WorkTaskResult(ctx, sqlgen.WorkTaskResultParams{TaskID: t.ID, ResultEventID: id}); e != nil {
		return e
	}
	if e = appendWorkDecision(ctx, q, w, orchestration.WorkDecision{Kind: "result", Reason: content, ActorKind: "system", ActorID: "task", TaskID: t.ID, EvidenceIDs: []string{id}}); e != nil {
		return e
	}
	if stopping {
		if e = workNotice(ctx, q, &w, "work_item_finished", "Further follow-ups are stopped. Collaboration ended with status "+t.Status+"; its recorded cleanup outcome remains available."); e != nil {
			return e
		}
	}
	return saveWorkItem(ctx, q, &w)
}

func insertWorkFact(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, ev orchestration.SourceEvent) error {
	raw, e := json.Marshal(ev)
	if e != nil {
		return e
	}
	_, e = q.InsertAutomationEvent(ctx, sqlgen.InsertAutomationEventParams{ID: ev.ID, RoomID: ev.RoomID, SourceID: ev.SourceID, ThreadKey: ev.ThreadKey, Kind: ev.Kind, ActorID: ev.ActorID, ActorKind: ev.ActorKind, Status: ev.Status, Value: string(raw)})
	if e != nil {
		return e
	}
	if e = linkWorkEvidence(ctx, q, w, []string{ev.ID}); e != nil {
		return e
	}
	return event(ctx, q, "automation.event.updated", w.RoomID, ev.ID)
}
func recordWorkHumanAnswer(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, answer string) error {
	ev := orchestration.SourceEvent{ID: "work-answer:" + newID(), RoomID: w.RoomID, SourceID: "workitem", ThreadKey: "workitem:" + w.ID, WorkItemID: w.ID, Kind: "workitem.answer", ActorKind: "user", ActorID: actorID(ctx), Content: answer, OccurredAt: now(), Status: "context"}
	if w.PolicyID != "" {
		policy, e := q.GetAutomationPolicyID(ctx, w.PolicyID)
		if e != nil {
			return e
		}
		if policy.Enabled != 0 && policy.Mode == "auto" {
			ev.Status = "pending"
		}
	}
	if e := insertWorkFact(ctx, q, w, ev); e != nil {
		return e
	}
	// This immutable relation proves that the authenticated judgment created the
	// fact. The outer decision separately records the human state transition.
	return appendWorkDecision(ctx, q, *w, orchestration.WorkDecision{Kind: "evidence", Reason: "A member supplied a judgment for the matter.", ActorKind: "user", ActorID: actorID(ctx), EvidenceIDs: []string{ev.ID}})
}

// Explicit watches associate future evidence; they never turn a Thread into a
// unique work identity or deliver an execution grant.
func recordWorkItemSourceEvent(ctx context.Context, tx *sql.Tx, ev orchestration.SourceEvent, eligible bool) error {
	if !eligible {
		return nil
	}
	q := sqlgen.New(tx)
	rows, e := q.WatchingWorkItems(ctx, sqlgen.WatchingWorkItemsParams{SourceID: ev.SourceID, ThreadKey: ev.ThreadKey})
	if e != nil {
		return e
	}
	for _, row := range rows {
		w, e := workItemRow(row)
		if e != nil {
			return e
		}
		if w.RoomID != ev.RoomID {
			continue
		}
		if e = linkWorkEvidence(ctx, q, &w, []string{ev.ID}); e != nil {
			return e
		}
		if e = saveWorkItem(ctx, q, &w); e != nil {
			return e
		}
		if ev.Reason == "explicit_run" || w.ActiveTaskID != "" || w.Status == "needs_attention" || w.Status == "waiting_for_person" || workTerminal(w) || workExpired(w) {
			continue
		}
		if w.PolicyID == "" {
			if w.Status == "waiting" {
				if e = workNotice(ctx, q, &w, "work_item_attention", "New evidence arrived for "+truncateUTF8(w.Goal, 512)+". The recorded wait and timer remain in effect until another decision."); e != nil {
					return e
				}
			}
			continue
		}
		policy, e := q.GetAutomationPolicyID(ctx, w.PolicyID)
		if e != nil {
			return e
		}
		fact := orchestration.SourceEvent{
			ID: "work-evidence:" + w.ID + ":" + ev.ID, RoomID: w.RoomID, SourceID: "workitem", ThreadKey: "workitem:" + w.ID,
			WorkItemID: w.ID, CausationID: ev.ID, Kind: "workitem.evidence", ActorKind: "system", ActorID: "workitem", OccurredAt: now(), Status: "pending",
			Content: fmt.Sprintf("A followed conversation supplied new evidence for this matter. This fact grants no additional authority.\nSource event: %s\nSource: %s\nOriginal actor: %s (%s)\n\n%s", ev.ID, ev.SourceID, ev.ActorID, ev.ActorKind, ev.Content),
		}
		if policy.Enabled == 0 || policy.Mode != "auto" {
			fact.Status = "context"
		}
		if e = insertWorkFact(ctx, q, &w, fact); e != nil {
			return e
		}
		if fact.Status == "context" {
			if e = recordWorkIntakeAttention(ctx, q, fact, "owning_policy_not_automatic"); e != nil {
				return e
			}
		}
	}
	return nil
}

func workSummary(w orchestration.WorkItem) orchestration.WorkItemSummary {
	return orchestration.WorkItemSummary{QuestionKind: w.QuestionKind, CompletionMode: w.CompletionMode, DueAt: w.DueAt, ExpiresAt: w.ExpiresAt, ID: w.ID, Goal: w.Goal, CompletionCriteria: w.CompletionCriteria, Status: w.Status, Revision: w.Revision, ActiveTaskID: w.ActiveTaskID, WaitCondition: w.WaitCondition, Question: w.Question, Budget: w.Budget, Usage: w.Usage, Deadline: w.Deadline}
}
func candidateWorkItems(ctx context.Context, q *sqlgen.Queries, events []orchestration.SourceEvent) ([]orchestration.WorkItemSummary, error) {
	out := []orchestration.WorkItemSummary{}
	seen := map[string]bool{}
	for _, ev := range events {
		rows, e := q.WorkItemForEvent(ctx, ev.ID)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			if seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			w, e := workItemRow(row)
			if e != nil {
				return nil, e
			}
			summary, err := candidateWorkSummary(ctx, q, w)
			if err != nil {
				return nil, err
			}
			out = append(out, summary)
			if len(out) > 20 {
				return out, problem("invalid_work_item", "Too many related matters for a bounded decision.")
			}
		}
	}
	return out, nil
}

func workEventPolicy(ctx context.Context, q *sqlgen.Queries, ev orchestration.SourceEvent) (sqlgen.AutomationPolicy, error) {
	if ev.WorkItemID == "" {
		return q.GetAutomationPolicy(ctx, sqlgen.GetAutomationPolicyParams{RoomID: ev.RoomID, SourceID: ev.SourceID})
	}
	n, e := q.WorkInternalEvent(ctx, ev.ID)
	if e != nil {
		return sqlgen.AutomationPolicy{}, e
	}
	if n != 1 {
		return sqlgen.AutomationPolicy{}, sql.ErrNoRows
	}
	if ev.Kind == "workitem.evidence" {
		original, e := q.GetAutomationEvent(ctx, ev.CausationID)
		if e != nil {
			return sqlgen.AutomationPolicy{}, e
		}
		if _, e = automationSourceKind(ctx, q, ev.RoomID, original.SourceID, true); e != nil {
			return sqlgen.AutomationPolicy{}, sql.ErrNoRows
		}
	}
	w, e := getWorkItem(ctx, q, ev.WorkItemID)
	if e != nil {
		return sqlgen.AutomationPolicy{}, e
	}
	if workTerminal(w) || ev.Seq <= w.DelegationStartAfter || workExpired(w) || w.PolicyID == "" || w.ActiveTaskID != "" || w.Status == "waiting_for_person" || w.Status == "needs_attention" {
		return sqlgen.AutomationPolicy{}, sql.ErrNoRows
	}
	return q.GetAutomationPolicyID(ctx, w.PolicyID)
}
func workEventEligible(ctx context.Context, q *sqlgen.Queries, ev orchestration.SourceEvent, p orchestration.Policy) bool {
	if ev.WorkItemID == "" {
		for _, id := range p.AllowedActorIDs {
			if id == ev.ActorID && ev.ActorKind == "user" {
				return true
			}
		}
		return false
	}
	r, e := workEventPolicy(ctx, q, ev)
	return e == nil && r.ID == p.ID
}

// Timer expiry commits a unique firing fact and receipt in the same transaction.
// Restart can rescan safely; changing the wait cancels its prior timer first.
func (s *Store) FireWorkItemTimers(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := expireWorkItems(ctx, q); e != nil {
			return e
		}
		rows, e := q.DueWorkTimers(ctx, now())
		if e != nil {
			return e
		}
		for _, timer := range rows {
			w, e := getWorkItem(ctx, q, timer.WorkItemID)
			if e != nil {
				return e
			}
			if workTerminal(w) || workExpired(w) || w.Status != "waiting" {
				if e = q.CancelWorkTimers(ctx, w.ID); e != nil {
					return e
				}
				continue
			}
			w.Status = "open"
			w.WaitCondition = ""
			status := "pending"
			if w.Usage.FollowUps >= w.Budget.MaxFollowUps || w.PolicyID == "" {
				w.Status = "needs_attention"
				status = "context"
			}
			w.Usage.FollowUps++
			ev := orchestration.SourceEvent{ID: "timer:" + timer.ID, RoomID: w.RoomID, SourceID: "timer", ThreadKey: "workitem:" + w.ID, WorkItemID: w.ID, CausationID: timer.ID, Kind: "timer.fired", ActorKind: "system", ActorID: "timer", Content: "Follow-up is due. Waiting condition: " + timer.Condition, OccurredAt: now(), Status: status}
			if e = insertWorkFact(ctx, q, &w, ev); e != nil {
				return e
			}
			if e = q.FireWorkTimer(ctx, sqlgen.FireWorkTimerParams{ID: timer.ID, EventID: ev.ID}); e != nil {
				return e
			}
			if status == "context" {
				if e = workNotice(ctx, q, &w, "work_item_attention", ev.Content); e != nil {
					return e
				}
			}
			if e = saveWorkItem(ctx, q, &w); e != nil {
				return e
			}
		}
		return nil
	})
}

func chargeWorkEvaluation(ctx context.Context, q *sqlgen.Queries, c *orchestration.Candidate) error {
	targets := map[string]bool{}
	for _, ev := range c.Events {
		if ev.WorkItemID != "" {
			targets[ev.WorkItemID] = true
		}
	}
	items := make([]orchestration.WorkItem, 0, len(c.WorkItems))
	for _, summary := range c.WorkItems {
		if !targets[summary.ID] {
			continue
		}
		w, e := getWorkItem(ctx, q, summary.ID)
		if e != nil {
			return e
		}
		if workTerminal(w) || workExpired(w) || w.Usage.Evaluations >= w.Budget.MaxEvaluations {
			return problem("work_budget_exhausted", "The matter's assessment allowance or deadline is exhausted.")
		}
		items = append(items, w)
	}
	for _, w := range items {
		w.Usage.Evaluations++
		if e := saveWorkItem(ctx, q, &w); e != nil {
			return e
		}
		for i := range c.WorkItems {
			if c.WorkItems[i].ID == w.ID {
				summary, err := candidateWorkSummary(ctx, q, w)
				if err != nil {
					return err
				}
				c.WorkItems[i] = summary
			}
		}
	}
	return nil
}
func workCandidateStale(ctx context.Context, q *sqlgen.Queries, c orchestration.Candidate) (bool, error) {
	for _, ev := range c.Events {
		if ev.WorkItemID == "" {
			continue
		}
		row, e := workEventPolicy(ctx, q, ev)
		if errors.Is(e, sql.ErrNoRows) {
			return true, nil
		}
		if e != nil {
			return true, e
		}
		if row.ID != c.Policy.ID {
			return true, nil
		}
	}
	for _, summary := range c.WorkItems {
		w, e := getWorkItem(ctx, q, summary.ID)
		if e != nil {
			return true, e
		}
		changed := w.Revision != summary.Revision
		if summary.Frontier != "" {
			frontier, e := workAssessmentFrontier(ctx, q, w)
			if e != nil {
				return true, e
			}
			changed = frontier != summary.Frontier
		}
		if changed || workTerminal(w) || workExpired(w) {
			return true, nil
		}
	}
	return false, nil
}

// Intake failures for a targeted follow-up must leave the matter actionable even
// when no provider request can be authorized. Human judgment itself has no model
// or external-action charge.
func recordWorkIntakeAttention(ctx context.Context, q *sqlgen.Queries, ev orchestration.SourceEvent, reason string) error {
	if ev.WorkItemID == "" {
		return nil
	}
	w, e := getWorkItem(ctx, q, ev.WorkItemID)
	if e != nil {
		return e
	}
	if workTerminal(w) || w.ActiveTaskID != "" || w.Status == "waiting_for_person" || w.Status == "needs_attention" {
		return nil
	}
	w.Status = "needs_attention"
	w.Question = "Review the follow-up before further autonomous work: " + reason
	if e = q.CancelWorkTimers(ctx, w.ID); e != nil {
		return e
	}
	if e = appendWorkDecision(ctx, q, w, orchestration.WorkDecision{Kind: "request_judgment", Reason: reason, ActorKind: "system", ActorID: "intake", EvidenceIDs: []string{ev.ID}}); e != nil {
		return e
	}
	if e = workNotice(ctx, q, &w, "work_item_attention", w.Question); e != nil {
		return e
	}
	return saveWorkItem(ctx, q, &w)
}

func workPolicyForItem(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) (orchestration.Policy, error) {
	row, e := q.GetAutomationPolicyID(ctx, w.PolicyID)
	if e != nil {
		return orchestration.Policy{}, e
	}
	return automationPolicy(row)
}

// An exact replay preserves its original receipt; cancellation wins atomically
// against a late write when no receipt has committed.
func (s *Store) CancelWorkItemRequest(ctx context.Context, id string) (cancelled bool, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		if !validText(id, 128, false) {
			return problem("invalid_work_item", "A stable request ID is required.")
		}
		r, e := q.GetWorkRequest(ctx, sqlgen.GetWorkRequestParams{UserID: actorID(ctx), RequestID: id})
		if e == nil {
			cancelled = r.Cancelled != 0
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		cancelled = true
		return q.SaveWorkRequest(ctx, sqlgen.SaveWorkRequestParams{UserID: actorID(ctx), RequestID: id, RequestHash: "cancelled", Result: "{}", Cancelled: 1})
	})
	return
}
