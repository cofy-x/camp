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
			pRow, e := q.GetAutomationPolicy(ctx, sqlgen.GetAutomationPolicyParams{RoomID: row.RoomID, SourceID: row.SourceID})
			var p orchestration.Policy
			if e == nil {
				p, e = automationPolicy(pRow)
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			evt, e2 := automationEvent(row)
			if e2 != nil {
				return e2
			}
			if e != nil || !p.Enabled || evt.Seq <= p.StartAfter || !slices.Contains(p.AllowedActorIDs, evt.ActorID) || evt.ActorKind != "user" {
				evt.Status = "context"
				evt.Reason = "policy_not_applicable"
				if e = updateAutomationEvent(ctx, q, evt); e != nil {
					return e
				}
				continue
			}
			if e = automationAuthority(ctx, q, p); e != nil {
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
				if v.Seq > p.StartAfter && v.ActorKind == "user" && slices.Contains(p.AllowedActorIDs, v.ActorID) {
					c.Events = append(c.Events, v)
				}
			}
			c.Tasks, c.TaskFrontier, e = automationTasks(ctx, q, p.RoomID, p.SourceID, evt.ThreadKey, c.Events)
			if e != nil {
				return e
			}
			raw, e := json.Marshal(c)
			if e != nil {
				return e
			}
			oversized := len(raw) > automationAssessmentContextBytes
			if oversized {
				c, raw, e = unavailableAutomationCandidate(c)
				if e != nil {
					return e
				}
			}
			if e = q.InsertAutomationEvaluation(ctx, sqlgen.InsertAutomationEvaluationParams{ID: c.ID, SourceID: evt.SourceID, ThreadKey: evt.ThreadKey, Candidate: string(raw), CreatedAt: now()}); e != nil {
				return e
			}
			if oversized {
				assessment := orchestration.Assessment{Outcome: "unavailable", Reason: "The frozen assessment context exceeds its bounded input limit. No model request was sent; the source originals remain available."}
				result, _ := json.Marshal(assessment)
				if e = q.FinishAutomationEvaluation(ctx, sqlgen.FinishAutomationEvaluationParams{ID: c.ID, Status: "needs_attention", Assessment: string(result)}); e != nil {
					return e
				}
				for _, v := range c.Events {
					v.Status, v.Reason, v.EvaluationID, v.PolicyRevision = "needs_attention", "assessment_context_limit", c.ID, p.Revision
					if e = updateAutomationEvent(ctx, q, v); e != nil {
						return e
					}
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
		if a.Outcome != "accept" && a.Outcome != "ignore" && a.Outcome != "unavailable" {
			a = orchestration.Assessment{Outcome: "unavailable", Reason: "invalid_assessment"}
		}
		if !validText(a.Reason, 4096, false) || (a.Outcome == "accept" && !validText(a.Goal, 8192, false)) {
			a = orchestration.Assessment{Outcome: "unavailable", Reason: "invalid_assessment"}
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
			t, e := createAutomationTask(ctx, tx, c, a)
			if e != nil {
				return e
			}
			taskID = t.ID
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
				}
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
