package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func automationTasks(ctx context.Context, q *sqlgen.Queries, room, source, thread string, events []orchestration.SourceEvent) ([]orchestration.TaskSummary, string, error) {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	eventIDs, err := json.Marshal(ids)
	if err != nil {
		return nil, "", err
	}
	// Keep scope selection and routing facts in one snapshot projection. Loading
	// full Room task snapshots here also loaded every unrelated grant/history.
	rows, err := q.AutomationRelatedTaskRouting(ctx, sqlgen.AutomationRelatedTaskRoutingParams{RoomID: room, SourceID: source, ThreadKey: thread, EventIds: string(eventIDs)})
	if err != nil {
		return nil, "", err
	}
	tasks := []orchestration.TaskSummary{}
	type routingFact struct {
		ID                   string
		Goal                 string
		Status               string
		ReceivesInputs       bool
		SubscriptionID       string
		SubscriptionRevision int64
		SubscriptionEnabled  bool
		ReceivedEventIDs     []string
	}
	facts := []routingFact{}
	for _, row := range rows {
		if row.MissingPolicy != 0 {
			return nil, "", sql.ErrNoRows
		}
		received := []string{}
		if err = json.Unmarshal([]byte(row.ReceivedEventIds), &received); err != nil {
			return nil, "", err
		}
		fact := routingFact{ID: row.ID, Goal: row.Goal, Status: row.Status, ReceivesInputs: row.ReceivesInputs != 0, SubscriptionID: row.SubscriptionID, SubscriptionRevision: row.SubscriptionRevision, SubscriptionEnabled: row.SubscriptionEnabled != 0, ReceivedEventIDs: received}
		tasks = append(tasks, orchestration.TaskSummary{ID: row.ID, Goal: row.Goal, Status: row.Status, Revision: row.Revision, ReceivesInputs: fact.ReceivesInputs, ReceivedEventIDs: received})
		facts = append(facts, fact)
	}
	raw, err := json.Marshal(facts)
	return tasks, remoteHash(raw), err
}
func automationDay() string { return time.Now().UTC().Format("2006-01-02") }

const automationAssessmentContextBytes = 128 << 10

// Oversized context is an unavailable assessment, never a shortened model
// request. Retain bounded references and whole task summaries for its audit view.
func unavailableAutomationCandidate(c orchestration.Candidate) (orchestration.Candidate, []byte, error) {
	originalTasks := c.Tasks
	c.ContextLimited, c.TotalTaskCount = true, len(originalTasks)
	c.Events = slices.Clone(c.Events)
	c.Memories = nil
	c.WorkItems = slices.Clone(c.WorkItems)
	for i := range c.WorkItems {
		c.WorkItems[i].Goal = ""
		c.WorkItems[i].CompletionCriteria = ""
		c.WorkItems[i].WaitCondition = ""
		c.WorkItems[i].Question = ""
		if len(c.WorkItems[i].Actions) > 0 {
			c.WorkItems[i].ActionsLimited = true
		}
		c.WorkItems[i].Actions = nil
		c.WorkItems[i].RecentTasks = nil
		c.WorkItems[i].RecentDecisions = nil
		c.WorkItems[i].ContextLimited = true
	}
	for index := range c.Events {
		c.Events[index].Content = ""
	}
	c.Tasks = []orchestration.TaskSummary{}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > automationAssessmentContextBytes {
		return c, nil, problem("invalid_source", "The assessment reference metadata exceeds its bounded context.")
	}
	for _, task := range originalTasks {
		c.Tasks = append(c.Tasks, task)
		next, err := json.Marshal(c)
		if err != nil {
			return c, nil, err
		}
		if len(next) > automationAssessmentContextBytes {
			c.Tasks = c.Tasks[:len(c.Tasks)-1]
			break
		}
		raw = next
	}
	return c, raw, nil
}

// Claim reserves the charge and records the frozen candidate before a provider
// call. A running lane is never reclaimed by a timer that could duplicate it.
func (s *Store) ClaimAutomationEvaluation(ctx context.Context, modelRevision string, b Budget) (out *orchestration.Candidate, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		rows, e := q.PendingAutomationEvents(ctx)
		if e != nil {
			return e
		}
		for _, row := range rows {
			evt, e2 := automationEvent(row)
			if e2 != nil {
				return e2
			}
			pRow, e := workEventPolicy(ctx, q, evt)
			var p orchestration.Policy
			if e == nil {
				p, e = automationPolicy(pRow)
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e != nil || !p.Enabled || evt.Seq <= p.StartAfter || !workEventEligible(ctx, q, evt, p) {
				if e = recordWorkIntakeAttention(ctx, q, evt, "policy_not_applicable"); e != nil {
					return e
				}
				evt.Status = "context"
				evt.Reason = "policy_not_applicable"
				if e = updateAutomationEvent(ctx, q, evt); e != nil {
					return e
				}
				continue
			}
			if e = automationAuthority(ctx, q, p); e != nil {
				if e = recordWorkIntakeAttention(ctx, q, evt, "authorization_unavailable"); e != nil {
					return e
				}
				evt.Status = "needs_attention"
				evt.Reason = "authorization_unavailable"
				if e = updateAutomationEvent(ctx, q, evt); e != nil {
					return e
				}
				continue
			}
			n, e := q.RunningAutomationThread(ctx, sqlgen.RunningAutomationThreadParams{SourceID: evt.SourceID, ThreadKey: evt.ThreadKey})
			if e != nil {
				return e
			}
			if n > 0 {
				continue
			}
			day := automationDay()
			if e = q.EnsureAutomationUsage(ctx, sqlgen.EnsureAutomationUsageParams{PolicyID: p.ID, Day: day}); e != nil {
				return e
			}
			usage, e := q.GetAutomationUsage(ctx, sqlgen.GetAutomationUsageParams{PolicyID: p.ID, Day: day})
			if e != nil {
				return e
			}
			if usage.Evaluations >= int64(p.DailyEvaluations) {
				if e = recordWorkIntakeAttention(ctx, q, evt, "daily_evaluation_limit"); e != nil {
					return e
				}
				evt.Status = "needs_attention"
				evt.Reason = "daily_evaluation_limit"
				if e = updateAutomationEvent(ctx, q, evt); e != nil {
					return e
				}
				continue
			}
			batch, e := q.AutomationThreadBatch(ctx, sqlgen.AutomationThreadBatchParams{RoomID: evt.RoomID, SourceID: evt.SourceID, ThreadKey: evt.ThreadKey})
			if e != nil {
				return e
			}
			c := orchestration.Candidate{ID: newID(), Policy: p, Events: []orchestration.SourceEvent{}, ModelRevision: modelRevision, Budget: orchestration.ExecutionBudget{Model: b.Model, RuntimeKey: b.RuntimeKey, MaxTokens: b.MaxTokens, MaxSteps: b.MaxSteps, TimeoutSeconds: b.TimeoutSeconds}}
			for _, r := range batch {
				v, e := automationEvent(r)
				if e != nil {
					return e
				}
				if v.Seq > p.StartAfter && workEventEligible(ctx, q, v, p) {
					c.Events = append(c.Events, v)
				}
			}
			c.Tasks, c.TaskFrontier, e = automationTasks(ctx, q, p.RoomID, p.SourceID, evt.ThreadKey, c.Events)
			if e != nil {
				return e
			}
			c.WorkItems, e = candidateWorkItems(ctx, q, c.Events)
			relatedLimit := false
			if e != nil {
				var known *Error
				if !errors.As(e, &known) || known.Code != "invalid_work_item" {
					return e
				}
				relatedLimit = true
			}
			c.Memories, e = SnapshotMemories(ctx, q, p.RoomID, "", 5)
			if e != nil {
				return e
			}
			c.EventFrontier, e = q.AutomationFrontier(ctx, p.RoomID)
			if e != nil {
				return e
			}
			if !relatedLimit {
				if e = chargeWorkEvaluation(ctx, q, &c); e != nil {
					for _, v := range c.Events {
						if e = recordWorkIntakeAttention(ctx, q, v, "work_budget_exhausted"); e != nil {
							return e
						}
						v.Status, v.Reason = "needs_attention", "work_budget_exhausted"
						if e = updateAutomationEvent(ctx, q, v); e != nil {
							return e
						}
					}
					continue
				}
			}
			raw, e := json.Marshal(c)
			if e != nil {
				return e
			}
			oversized := relatedLimit || len(raw) > automationAssessmentContextBytes
			if oversized {
				c, raw, e = unavailableAutomationCandidate(c)
				if e != nil {
					var known *Error
					if !errors.As(e, &known) || known.Code != "invalid_source" {
						return e
					}
					// Even a valid policy's escaped reference metadata can exceed
					// the audit envelope. Retire this lane rather than poisoning
					// the global queue or issuing a shortened model request.
					for _, v := range c.Events {
						v.Status, v.Reason = "needs_attention", "assessment_reference_limit"
						if e = updateAutomationEvent(ctx, q, v); e != nil {
							return e
						}
					}
					attention := c
					attention.ID = ""
					attention.WorkItems = nil
					if e = recordAssessmentAttention(ctx, q, attention, orchestration.Assessment{Outcome: "unavailable", Reason: "The policy and reference metadata exceed the bounded assessment envelope. No model request was sent."}, "assessment_reference_limit"); e != nil {
						return e
					}
					continue
				}
			}
			if e = q.InsertAutomationEvaluation(ctx, sqlgen.InsertAutomationEvaluationParams{ID: c.ID, SourceID: evt.SourceID, ThreadKey: evt.ThreadKey, Candidate: string(raw), CreatedAt: now()}); e != nil {
				return e
			}
			if oversized {
				assessment := orchestration.Assessment{Outcome: "unavailable", Reason: "The frozen assessment context exceeds its bounded input limit. No model request was sent; the source originals remain available."}
				if relatedLimit {
					assessment.Reason = "Too many related matters for one bounded assessment. No model request was sent; evidence links remain available in Work Item history."
				}
				result, _ := json.Marshal(assessment)
				if e = q.FinishAutomationEvaluation(ctx, sqlgen.FinishAutomationEvaluationParams{ID: c.ID, Status: "needs_attention", Assessment: string(result)}); e != nil {
					return e
				}
				for _, v := range c.Events {
					v.Status, v.Reason, v.EvaluationID, v.PolicyRevision = "needs_attention", "assessment_context_limit", c.ID, p.Revision
					if relatedLimit {
						v.Reason = "assessment_related_work_limit"
					}
					if e = updateAutomationEvent(ctx, q, v); e != nil {
						return e
					}
				}
				attention := c
				attention.WorkItems = nil
				if e = recordAssessmentAttention(ctx, q, attention, assessment, "assessment_context_limit"); e != nil {
					return e
				}
				return event(ctx, q, "automation.evaluation.updated", c.Policy.RoomID, c.ID)
			}
			if e = q.ChargeAutomationEvaluation(ctx, sqlgen.ChargeAutomationEvaluationParams{PolicyID: p.ID, Day: day}); e != nil {
				return e
			}
			for _, v := range c.Events {
				v.Status = "evaluating"
				v.EvaluationID = c.ID
				v.PolicyRevision = p.Revision
				if e = updateAutomationEvent(ctx, q, v); e != nil {
					return e
				}
			}
			out = &c
			return nil
		}
		return nil
	})
	return
}

func (s *Store) CompleteAutomationEvaluation(ctx context.Context, id string, a orchestration.Assessment) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		row, e := q.GetAutomationEvaluation(ctx, id)
		if e != nil {
			return e
		}
		if row.Status != "running" {
			return nil
		}
		var c orchestration.Candidate
		if e = json.Unmarshal([]byte(row.Candidate), &c); e != nil {
			return e
		}
		if !validAssessment(a) {
			a = orchestration.Assessment{Outcome: "unavailable", Reason: "invalid_assessment"}
		}
		if a.Outcome == "execute" {
			a.Outcome = "accept"
		}
		status, reason, taskID := "ignored", a.Reason, ""
		pRow, pErr := q.GetAutomationPolicyID(ctx, c.Policy.ID)
		var p orchestration.Policy
		if pErr == nil {
			p, pErr = automationPolicy(pRow)
		}
		if len(c.Events) == 0 {
			return problem("invalid_source", "The frozen assessment has no source evidence.")
		}
		_, frontier, e := automationTasks(ctx, q, c.Policy.RoomID, c.Policy.SourceID, c.Events[0].ThreadKey, c.Events)
		if e != nil {
			return e
		}
		stale := pErr != nil || !p.Enabled || p.Revision != c.Policy.Revision || frontier != c.TaskFrontier
		workStale, e := workCandidateStale(ctx, q, c)
		if e != nil {
			return e
		}
		stale = stale || workStale
		if !stale {
			stale = automationAuthority(ctx, q, p) != nil
		}
		for _, v := range c.Events {
			current, e := q.GetAutomationEvent(ctx, v.ID)
			if e != nil {
				return e
			}
			if current.Status != "evaluating" || current.EvaluationID != id {
				stale = true
			}
			if v.ExternalEventID != "" {
				g, e := githubEvent(ctx, q, v.ExternalEventID)
				if e != nil {
					return e
				}
				if g.Status != "pending" {
					stale = true
				}
			}
		}
		switch {
		case stale:
			status, reason = "needs_attention", "stale_assessment"
		case a.Outcome == "unavailable":
			status = "needs_attention"
		case p.Mode == "observe":
			status = "observed"
		case a.Outcome == "accept":
			day := automationDay()
			if e = q.EnsureAutomationUsage(ctx, sqlgen.EnsureAutomationUsageParams{PolicyID: p.ID, Day: day}); e != nil {
				return e
			}
			usage, e := q.GetAutomationUsage(ctx, sqlgen.GetAutomationUsageParams{PolicyID: p.ID, Day: day})
			if e != nil {
				return e
			}
			if usage.Tasks >= int64(p.DailyTasks) {
				status, reason = "needs_attention", "daily_task_limit"
				break
			}
			var applyErr error
			taskID, applyErr = applyAutomationWorkDecision(ctx, tx, c, &a)
			if applyErr != nil {
				var known *Error
				if errors.As(applyErr, &known) {
					status, reason = "needs_attention", known.Code
					break
				}
				return applyErr
			}
			status = "accepted"
			if e = q.ChargeAutomationTask(ctx, sqlgen.ChargeAutomationTaskParams{PolicyID: p.ID, Day: day}); e != nil {
				return e
			}
			// The old GitHub endpoint is a compatibility projection of this decision.
			for _, v := range c.Events {
				if v.ExternalEventID != "" {
					if e = q.DecideGitHubEvent(ctx, sqlgen.DecideGitHubEventParams{EventID: v.ExternalEventID, Outcome: "accepted", ActorID: p.OwnerUserID, TaskID: sql.NullString{String: taskID, Valid: true}, Reason: "Automation policy " + p.ID + ": " + a.Reason, CreatedAt: now()}); e != nil {
						return e
					}
					if e = event(ctx, q, "github.event.decided", c.Policy.RoomID, v.ExternalEventID); e != nil {
						return e
					}
				}
			}
		case a.Outcome == "wait" || a.Outcome == "request_judgment" || a.Outcome == "complete" || a.Outcome == "ignore":
			targeted := false
			for _, ev := range c.Events {
				targeted = targeted || ev.WorkItemID != ""
			}
			if a.Outcome == "ignore" && a.WorkItemID == "" && !targeted {
				break
			}
			if _, applyErr := applyAutomationWorkDecision(ctx, tx, c, &a); applyErr != nil {
				var known *Error
				if errors.As(applyErr, &known) {
					status, reason = "needs_attention", known.Code
				} else {
					return applyErr
				}
			} else {
				status = a.Outcome
			}
		}
		if status == "needs_attention" {
			if e = recordAssessmentAttention(ctx, q, c, a, reason); e != nil {
				return e
			}
		}
		raw, _ := json.Marshal(a)
		if e = q.FinishAutomationEvaluation(ctx, sqlgen.FinishAutomationEvaluationParams{ID: id, Status: status, Assessment: string(raw)}); e != nil {
			return e
		}
		for _, v := range c.Events {
			current, e := q.GetAutomationEvent(ctx, v.ID)
			if e != nil {
				return e
			}
			if current.Status != "evaluating" || current.EvaluationID != id {
				continue
			}
			v.Status = status
			v.Reason = reason
			v.TaskID = taskID
			v.EvaluationID = id
			v.PolicyRevision = c.Policy.Revision
			if e = updateAutomationEvent(ctx, q, v); e != nil {
				return e
			}
		}
		return event(ctx, q, "automation.evaluation.updated", c.Policy.RoomID, id)
	})
}

func (s *Store) RecoverAutomationWork(ctx context.Context) error {
	rows, e := s.queries().RunningAutomationEvaluations(ctx)
	if e != nil {
		return e
	}
	for _, r := range rows {
		if e = s.CompleteAutomationEvaluation(ctx, r.ID, orchestration.Assessment{Outcome: "unavailable", Reason: "The Server stopped before the assessment outcome was committed. The provider request was not retried."}); e != nil {
			return e
		}
	}
	return s.queries().RecoverAutomationDeliveries(ctx)
}
