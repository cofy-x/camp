package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func assertCurrentWorkRecovery(t *testing.T, s *Store, actor context.Context, id string, kind orchestration.WorkRecoveryKind) orchestration.WorkItem {
	t.Helper()
	w, err := s.GetWorkItem(actor, id)
	if err != nil || w.Status != "needs_attention" || w.WorkRecovery == nil || w.WorkRecovery.Kind != kind || w.Question != "" {
		t.Fatalf("current recovery %q: %+v, %v", kind, w, err)
	}
	list, err := s.ListWorkItems(actor, WorkListOptions{View: "needs_you", RoomID: w.RoomID})
	if err != nil || len(list.Items) != 1 || list.Items[0].WorkRecovery == nil || *list.Items[0].WorkRecovery != *w.WorkRecovery {
		t.Fatalf("Work list did not project the same recovery: %+v, %v", list, err)
	}
	page := attentionFor(t, s, actor, AttentionOptions{RoomID: w.RoomID, Kind: "exception"})
	if page.Counts.Judgment != 0 || page.Counts.Exception != 1 || len(page.Items) != 1 || page.Items[0].WorkRecovery == nil ||
		*page.Items[0].WorkRecovery != *w.WorkRecovery || page.Items[0].Summary != w.WorkRecovery.Message {
		t.Fatalf("Needs me did not project the same recovery: %+v", page)
	}
	return w
}

func TestWorkRecoverySeparatesRuleAndAllowanceFailures(t *testing.T) {
	for _, kind := range []orchestration.WorkRecoveryKind{orchestration.WorkRecoveryReviewRule, orchestration.WorkRecoveryDailyLimit, orchestration.WorkRecoveryIncreaseAllowance} {
		t.Run(string(kind), func(t *testing.T) {
			s, actor, teammate, room, writer, _ := taskFixture(t)
			w, policy := reviewWaitingWork(t, s, actor, room, writer, true)
			switch kind {
			case orchestration.WorkRecoveryReviewRule, orchestration.WorkRecoveryDailyLimit:
				input := reviewPolicyInput(policy)
				if kind == orchestration.WorkRecoveryReviewRule {
					input.Enabled = false
				} else {
					input.DailyEvaluations = 1
				}
				if _, err := s.SaveAutomationPolicy(actor, room.ID, "room", input); err != nil {
					t.Fatal(err)
				}
			case orchestration.WorkRecoveryIncreaseAllowance:
				budget := w.Budget
				budget.MaxEvaluations = w.Usage.Evaluations
				w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "authorize", Budget: &budget})
			}
			reviewFireWorkTimer(t, s, w)
			if claim, err := s.ClaimAutomationEvaluation(ctx, "model", roomBudget); err != nil || claim != nil {
				t.Fatal("blocked follow-up admitted a model evaluation", claim, err)
			}
			w = assertCurrentWorkRecovery(t, s, actor, w.ID, kind)
			if (kind == orchestration.WorkRecoveryReviewRule || kind == orchestration.WorkRecoveryDailyLimit) && w.WorkRecovery.SourceID != "room" {
				t.Fatal("rule recovery did not identify the saved source", w.WorkRecovery)
			}
			if len(w.Decisions) == 0 || w.Decisions[0].ActorKind != "system" || w.Decisions[0].Reason == w.WorkRecovery.Message {
				t.Fatal("the immutable system reason was lost", w.Decisions)
			}
			for _, action := range []string{"answer", "answer_continue"} {
				_, err := s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: action, Content: "This is not a recovery action."}, roomBudget)
				requireCode(t, err, "question_answered")
			}
			if other := attentionFor(t, s, teammate); other.Total != 0 {
				t.Fatal("another member received the owner's recovery", other)
			}
			unrelated := w.Budget
			unrelated.MaxModelRequests++
			w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "authorize", Budget: &unrelated})
			if w.WorkRecovery == nil || w.WorkRecovery.Kind != kind || w.Status != "needs_attention" {
				t.Fatal("an unrelated authorization hid the unresolved follow-up", w)
			}
			if kind == orchestration.WorkRecoveryIncreaseAllowance {
				budget := w.Budget
				budget.MaxEvaluations++
				w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "authorize", Budget: &budget})
				if w.WorkRecovery != nil || w.Status != "open" {
					t.Fatal("adding the exhausted allowance did not settle its recovery", w)
				}
			}
			assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
			assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 1)
		})
	}
}

func TestWorkTimerBlockedByAllowanceDoesNotChargeFollowUp(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	w := createTestWork(t, s, actor, room)
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{
		Kind: "wait", WaitCondition: "Check the recorded review.",
		ResumeAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
	})
	budget := w.Budget
	budget.MaxFollowUps = 0
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "authorize", Budget: &budget})

	reviewFireWorkTimer(t, s, w)
	w = assertCurrentWorkRecovery(t, s, actor, w.ID, orchestration.WorkRecoveryIncreaseAllowance)
	if w.Usage.FollowUps != 0 || len(w.Timers) != 1 || w.Timers[0].Status != "fired" {
		t.Fatalf("blocked timer must keep its firing receipt without spending allowance: usage %+v, timers %+v", w.Usage, w.Timers)
	}
	if candidate, err := s.ClaimAutomationEvaluation(ctx, "model", roomBudget); candidate != nil || err != nil {
		t.Fatalf("blocked timer became executable: %v, %v", candidate, err)
	}

	budget.MaxFollowUps = 1
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "authorize", Budget: &budget})
	if w.Status != "open" || w.WorkRecovery != nil || w.Usage.FollowUps != 0 {
		t.Fatalf("one restored slot must settle recovery without replaying the timer: %+v", w)
	}
}

func TestRetainedWorkRecoveryUsesMatchingSystemDecisionAndKeepsReceipts(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, _ := reviewWaitingWork(t, s, actor, room, writer, true)
	reviewFireWorkTimer(t, s, w)
	w = decideTestWork(t, s, actor, wAfterRead(t, s, actor, w.ID), orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Should we proceed?"})
	answer := orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "answer", Content: "Proceed."}
	receipt, err := s.OperateWorkItem(actor, w.ID, answer, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	source := testGitHubSource(t, s, actor, room)
	policy, err := s.SaveAutomationPolicy(actor, room.ID, source.ID, orchestration.PolicyInput{
		RequestID: newID(), Enabled: false, Mode: "auto", Rules: "Review the original issue.",
		AllowedActorIDs: []string{"42"}, ParticipantAgentIDs: []string{writer.ID},
		FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		current, err := getWorkItem(ctx, q, w.ID)
		if err != nil {
			return err
		}
		var timerEventID string
		if err := tx.QueryRowContext(ctx, "SELECT event_id FROM work_item_timers WHERE work_item_id=? AND status='fired'", w.ID).Scan(&timerEventID); err != nil {
			return err
		}
		current.Status = "needs_attention"
		current.Question = "Review the follow-up before further autonomous work: policy_not_applicable"
		current.PolicyID = policy.ID
		if err := appendWorkDecision(ctx, q, current, orchestration.WorkDecision{Kind: "request_judgment", Reason: "policy_not_applicable", ActorID: "intake", ActorKind: "system", EvidenceIDs: []string{timerEventID}}); err != nil {
			return err
		}
		return saveWorkItem(ctx, q, &current)
	}); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := s.db.QueryRow("SELECT value FROM work_items WHERE id=?", w.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	current := assertCurrentWorkRecovery(t, s, actor, w.ID, orchestration.WorkRecoveryReviewRule)
	if current.WorkRecovery.SourceID != source.ID {
		t.Fatal("the retained system decision lost its saved GitHub rule", current.WorkRecovery)
	}
	_, err = s.OperateWorkItem(actor, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: current.Revision, Kind: "answer", Content: "Cannot clear a rule failure."}, roomBudget)
	requireCode(t, err, "question_answered")
	replayed, err := s.OperateWorkItem(actor, w.ID, answer, roomBudget)
	if err != nil || replayed.Revision != receipt.Revision || replayed.Question != receipt.Question {
		t.Fatal("original answer replay did not return its receipt", replayed, err)
	}
	if err := s.db.QueryRow("SELECT value FROM work_items WHERE id=?", w.ID).Scan(&after); err != nil || before != after {
		t.Fatal("reads or rejected answer rewrote the retained matter", err)
	}
	raw, err := json.Marshal(current)
	if err != nil || strings.Contains(string(raw), "recoveryLimit") {
		t.Fatal("private recovery metadata entered the public Work record", err)
	}
	// The same wording can be an authored question. Its latest human
	// decision always wins over a historical synthetic question.
	w = decideTestWork(t, s, actor, current, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Review the follow-up before further autonomous work: policy_not_applicable"})
	if w.WorkRecovery != nil || w.QuestionKind != orchestration.WorkQuestionJudgment || w.Question == "" {
		t.Fatal("the genuine human question was reclassified", w)
	}
}

func TestRetainedAllowanceRecoveryUsesKnownProducerAndSavedAssessment(t *testing.T) {
	for _, tc := range []struct {
		name, producer, outcome, limit string
		targeted                       bool
	}{
		{"intake evaluation", "intake", "", recoveryEvaluations, false},
		{"assessment evaluation", "assessment", "wait", recoveryEvaluations, false},
		{"targeted execution", "assessment", "accept", recoveryExecution, true},
		{"targeted follow-up", "assessment", "wait", recoveryFollowUps, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			w, policy := reviewWaitingWork(t, s, actor, room, writer, true)
			if err := s.transaction(ctx, func(tx *sql.Tx) error {
				q := sqlgen.New(tx)
				current, err := getWorkItem(ctx, q, w.ID)
				if err != nil {
					return err
				}
				d := orchestration.WorkDecision{
					Kind: "request_judgment", ActorKind: "system", ActorID: tc.producer,
					Reason: "work_budget_exhausted", EvidenceIDs: []string{w.Evidence[0].EventID},
				}
				current.Status = "needs_attention"
				current.Usage.Evaluations = current.Budget.MaxEvaluations
				if tc.producer == "intake" {
					current.Question = "Review the follow-up before further autonomous work: " + d.Reason
				} else {
					current.Question = "Review the recorded event assessment: " + d.Reason
					current.QuestionKind = orchestration.WorkQuestionJudgment
					d.EvaluationID = newID()
					event := orchestration.SourceEvent{ID: w.Evidence[0].EventID, RoomID: room.ID, SourceID: policy.SourceID}
					if tc.targeted {
						event.WorkItemID = w.ID
					}
					candidate, err := json.Marshal(orchestration.Candidate{ID: d.EvaluationID, Policy: policy, Events: []orchestration.SourceEvent{event}})
					if err != nil {
						return err
					}
					assessment, err := json.Marshal(orchestration.Assessment{Outcome: tc.outcome, WorkItemID: w.ID})
					if err != nil {
						return err
					}
					if err := q.InsertAutomationEvaluation(ctx, sqlgen.InsertAutomationEvaluationParams{ID: d.EvaluationID, SourceID: policy.SourceID, ThreadKey: "workitem:" + w.ID, Candidate: string(candidate), CreatedAt: now()}); err != nil {
						return err
					}
					if err := q.FinishAutomationEvaluation(ctx, sqlgen.FinishAutomationEvaluationParams{ID: d.EvaluationID, Status: "needs_attention", Assessment: string(assessment)}); err != nil {
						return err
					}
				}
				switch tc.limit {
				case recoveryExecution:
					current.Usage.ReservedModelRequests = current.Budget.MaxModelRequests
				case recoveryFollowUps:
					current.Usage.FollowUps = current.Budget.MaxFollowUps
				}
				if err := appendWorkDecision(ctx, q, current, d); err != nil {
					return err
				}
				return saveWorkItem(ctx, q, &current)
			}); err != nil {
				t.Fatal(err)
			}
			w = assertCurrentWorkRecovery(t, s, actor, w.ID, orchestration.WorkRecoveryIncreaseAllowance)
			budget := w.Budget
			budget.MaxActions++
			w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "authorize", Budget: &budget})
			if w.WorkRecovery == nil || w.Status != "needs_attention" {
				t.Fatal("an unrelated allowance closed retained recovery", w)
			}
			switch tc.limit {
			case recoveryEvaluations:
				budget.MaxEvaluations++
			case recoveryExecution:
				budget.MaxModelRequests++
			case recoveryFollowUps:
				budget.MaxFollowUps++
			}
			w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "authorize", Budget: &budget})
			if w.WorkRecovery != nil || w.Status != "open" {
				t.Fatal("restoring the exhausted allowance left retained recovery in Needs me", w)
			}
			assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
		})
	}
}

func wAfterRead(t *testing.T, s *Store, actor context.Context, id string) orchestration.WorkItem {
	t.Helper()
	w, err := s.GetWorkItem(actor, id)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
