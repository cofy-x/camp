package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func workActionFixture(t *testing.T, criteria ...string) (*Store, context.Context, orchestration.WorkItem, CollaborationTask, []orchestration.Delivery) {
	t.Helper()
	return workActionFixtureWithMode(t, "", criteria...)
}

func workActionFixtureWithMode(t *testing.T, mode string, criteria ...string) (*Store, context.Context, orchestration.WorkItem, CollaborationTask, []orchestration.Delivery) {
	t.Helper()
	s, task, policy := automationDeliveryFixture(t)
	if mode == "automatic" {
		if _, err := s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',1)"); err != nil {
			t.Fatal(err)
		}
	}
	actor := WithUser(ctx, User{ID: policy.OwnerUserID})
	room, err := s.GetRoom(ctx, task.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	message := sendAutomationDiscussion(t, s, actor, room, "Follow the document through confirmed external delivery.").Message
	completionCriteria := "Both configured destinations have confirmed receipts."
	if len(criteria) > 0 {
		completionCriteria = criteria[0]
	}
	w, err := s.CreateWorkItem(actor, room.ID, orchestration.WorkItemInput{CompletionMode: mode, RequestID: newID(), Goal: "Deliver the document", CompletionCriteria: completionCriteria, PolicyID: policy.ID, EvidenceIDs: []string{"room:" + message.ID}})
	if err != nil {
		t.Fatal(err)
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := attachWorkTask(ctx, q, &w, &task, 24); err != nil {
			return err
		}
		if err := saveTask(ctx, q, &task); err != nil {
			return err
		}
		if err := saveWorkItem(ctx, q, &w); err != nil {
			return err
		}
		// Exercise the durable terminal-task owner without a native/model call.
		task.Status, task.ActiveRunID, task.ActiveAttemptID, task.Summary = "completed", "", "", "The document is ready."
		if mode == "automatic" {
			task.WorkItemCompletion = &WorkItemCompletion{CriteriaMet: true, Reason: "The saved document meets the criteria; its configured delivery receipts must settle."}
		}
		if err := queueAutomationTaskNotice(ctx, q, task, "task_finished", task.Summary); err != nil {
			return err
		}
		if err := saveTask(ctx, q, &task); err != nil {
			return err
		}
		// This test begins after the task-result assessment and follows Actions.
		r, err := q.GetAutomationEvent(ctx, "task-result:"+task.ID)
		if err != nil {
			return err
		}
		if err = q.UpdateAutomationEvent(ctx, sqlgen.UpdateAutomationEventParams{ID: r.ID, Status: "context", Reason: "task_result_reviewed"}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Retire unrelated setup observations so the next assessment is the Action
	// lane under test, rather than a second fixture GitHub conversation.
	if err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		rows, e := q.PendingAutomationEvents(ctx)
		if e != nil {
			return e
		}
		for _, row := range rows {
			if e = q.UpdateAutomationEvent(ctx, sqlgen.UpdateAutomationEventParams{ID: row.ID, Status: "context", Reason: "setup_reviewed"}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := s.TaskAutomationDeliveries(actor, task.ID)
	if err != nil || len(deliveries) != 2 {
		t.Fatal(deliveries, err)
	}
	return s, actor, w, task, deliveries
}

func completeTestWorkAction(t *testing.T, s *Store, d *orchestration.Delivery, status string, comment int64) {
	t.Helper()
	result := orchestration.DeliveryResult{ClaimRevision: d.Revision, Status: status, Reason: "Recorded delivery outcome"}
	if status == "sent" {
		result.CommentID, result.URL = comment, fmt.Sprintf("https://github.com/acme/project/issues/3#issuecomment-%d", comment)
	}
	if err := s.CompleteAutomationDelivery(ctx, d.ID, result); err != nil {
		t.Fatal(err)
	}
}

func TestWorkActionResultsFreezeReceiptsAndReassessBeforeCompletion(t *testing.T) {
	s, actor, w, _, _ := workActionFixture(t)
	first, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	completeTestWorkAction(t, s, first, "sent", 301)
	var pending bool
	if err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		var e error
		pending, e = workCompletionPending(ctx, sqlgen.New(tx), w)
		return e
	}); err != nil || !pending {
		t.Fatal("partial delivery allowed completion", pending, err)
	}
	second, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || second == nil || second.ID == first.ID {
		t.Fatal(second, err)
	}
	completeTestWorkAction(t, s, second, "sent", 302)
	completeTestWorkAction(t, s, second, "sent", 302)
	assertScalar(t, s, "SELECT count(*) FROM work_action_receipts", 2)
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE kind='action.result'", 2)
	c := claimAutomation(t, s)
	if len(c.Events) != 2 || len(c.WorkItems) != 1 || len(c.WorkItems[0].Actions) != 2 {
		t.Fatal("missing Action outcome context", c)
	}
	for _, action := range c.WorkItems[0].Actions {
		if action.Status != "sent" || action.Body != "" || action.DeliveryID == "" {
			t.Fatal("unbounded or incomplete frozen Action summary", action)
		}
	}
	for _, event := range c.Events {
		if event.SourceID != "action" || event.WorkItemID != w.ID || strings.Contains(event.Content, "credential") || strings.Contains(event.Content, strings.Repeat("d", 48)) {
			t.Fatal("receipt identity or private fields changed", event)
		}
		proof, e := s.queries().WorkInternalEvent(ctx, event.ID)
		if e != nil || proof != 1 {
			t.Fatal("unproven action follow-up", proof, e)
		}
		var receipt workActionReceipt
		if e = json.Unmarshal([]byte(event.Content), &receipt); e != nil || receipt.DeliveryRevision <= 1 || len(receipt.Receipts) != 1 {
			t.Fatal(receipt, e)
		}
	}
	if err = s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "complete", WorkItemID: w.ID, Reason: "Both external destinations have confirmed receipts."}); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetWorkItem(actor, w.ID)
	if err != nil || current.Status != "completed" {
		t.Fatal(current, err)
	}
	if _, err = s.db.Exec("UPDATE work_action_receipts SET value='{}'"); err == nil {
		t.Fatal("immutable Action receipt permitted UPDATE")
	}
	if _, err = s.db.Exec("DELETE FROM work_action_receipts"); err == nil {
		t.Fatal("immutable Action receipt permitted DELETE")
	}
}

func TestWorkActionUnknownRequiresAttentionAndReadOnlyRecovery(t *testing.T) {
	s, actor, w, _, _ := workActionFixture(t)
	first, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	completeTestWorkAction(t, s, first, "unknown", 0)
	inbox, err := s.ListInbox(actor)
	if err != nil || len(inbox) != 1 || inbox[0].Kind != "work_item_attention" {
		t.Fatal("uncertain effect lost Inbox attention", inbox, err)
	}
	current, err := s.GetWorkItem(actor, w.ID)
	if err != nil || current.Status != "open" {
		t.Fatal("delivery overrode lifecycle state", current, err)
	}
	current = decideTestWork(t, s, actor, current, orchestration.WorkItemOperation{Kind: "complete", Reason: "Try to finish after the execution result."})
	if current.Status != "waiting" || !strings.Contains(current.WaitCondition, "action") {
		t.Fatal("uncertain effect completed matter", current)
	}
	row, err := s.queries().GetAutomationDelivery(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := automationDelivery(row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OperateAutomationDelivery(actor, unknown.ID, "reconcile", AutomationDeliveryInput{RequestID: newID(), Revision: unknown.Revision}); err != nil {
		t.Fatal(err)
	}
	recovery, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || recovery == nil || !recovery.Reconcile {
		t.Fatal(recovery, err)
	}
	completeTestWorkAction(t, s, recovery, "sent", 303)
	current, err = s.GetWorkItem(actor, w.ID)
	if err != nil || current.Usage.Actions != 1 || current.Status != "waiting" {
		t.Fatal("reconciliation dispatched or overrode wait", current, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_action_receipts", 2)
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE kind='action.result' AND status='pending'", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE kind='action.result' AND status='context'", 2)
	// The accepted completion is waiting for the other configured destination;
	// reconciling this receipt must not ask a model to approve the same result.
	remaining, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || remaining == nil || remaining.Reconcile {
		t.Fatal(remaining, err)
	}
	completeTestWorkAction(t, s, remaining, "sent", 304)
	current, err = s.GetWorkItem(actor, w.ID)
	if err != nil || current.Status != "completed" || current.Usage.Actions != 2 {
		t.Fatal("confirmed effects did not settle the accepted completion", current, err)
	}
}

func TestWorkActionLateReceiptPreservesHumanJudgmentAndTerminalState(t *testing.T) {
	for _, kind := range []string{"request_judgment", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			s, actor, w, _, _ := workActionFixture(t)
			claim, err := s.ClaimAutomationDelivery(ctx)
			if err != nil || claim == nil {
				t.Fatal(claim, err)
			}
			current, err := s.GetWorkItem(actor, w.ID)
			if err != nil {
				t.Fatal(err)
			}
			current = decideTestWork(t, s, actor, current, orchestration.WorkItemOperation{Kind: kind, Reason: "A current operator decision", Question: "Wait for my judgment."})
			completeTestWorkAction(t, s, claim, "sent", 304)
			after, err := s.GetWorkItem(actor, w.ID)
			if err != nil || after.Status != current.Status || after.Question != current.Question {
				t.Fatal("late delivery resurrected operator state", current, after, err)
			}
			assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE kind='action.result' AND status='pending'", 0)
		})
	}
}

func TestWorkActionPersistenceFailureRollsBackReceiptAndSourceFact(t *testing.T) {
	s, _, _, _, _ := workActionFixture(t)
	claim, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if _, err = s.db.Exec("CREATE TRIGGER reject_work_action_receipt BEFORE INSERT ON work_action_receipts BEGIN SELECT RAISE(ABORT,'reject receipt'); END"); err != nil {
		t.Fatal(err)
	}
	result := orchestration.DeliveryResult{ClaimRevision: claim.Revision, Status: "sent", CommentID: 305, URL: "https://github.com/acme/project/issues/3#issuecomment-305"}
	if err = s.CompleteAutomationDelivery(ctx, claim.ID, result); err == nil {
		t.Fatal("delivery committed without immutable receipt")
	}
	row, err := s.queries().GetAutomationDelivery(ctx, claim.ID)
	if err != nil || row.Status != "sending" {
		t.Fatal("failed receipt claimed success", row, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_action_receipts", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE kind='action.result'", 0)
	if _, err = s.db.Exec("DROP TRIGGER reject_work_action_receipt"); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteAutomationDelivery(ctx, claim.ID, result); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_action_receipts", 1)
}

func TestWorkActionWithdrawnUncertaintyBlocksCompletionUntilConfirmed(t *testing.T) {
	s, actor, w, task, _ := workActionFixture(t)
	claim, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	completeTestWorkAction(t, s, claim, "unknown", 0)
	row, err := s.queries().GetAutomationDelivery(ctx, claim.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := automationDelivery(row)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.transaction(ctx, func(tx *sql.Tx) error { return withdrawAutomationDelivery(ctx, sqlgen.New(tx), delivery, task) }); err != nil {
		t.Fatal(err)
	}
	count, err := s.queries().WorkPendingActions(ctx, w.ID)
	if err != nil || count != 2 {
		t.Fatal("withdrawal erased an earlier uncertain effect", count, err)
	}
	row, err = s.queries().GetAutomationDelivery(ctx, claim.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = automationDelivery(row)
	if err != nil || !delivery.Withdrawn {
		t.Fatal(delivery, err)
	}
	if _, err = s.OperateAutomationDelivery(actor, delivery.ID, "reconcile", AutomationDeliveryInput{RequestID: newID(), Revision: delivery.Revision}); err != nil {
		t.Fatal(err)
	}
	recovery, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || recovery == nil || !recovery.Reconcile || !recovery.Withdrawn {
		t.Fatal(recovery, err)
	}
	completeTestWorkAction(t, s, recovery, "sent", 306)
	count, err = s.queries().WorkPendingActions(ctx, w.ID)
	if err != nil || count != 1 {
		t.Fatal("confirmed withdrawn effect stayed unresolved", count, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_action_receipts", 3)
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE kind='action.result' AND status='pending'", 1)
}

func TestWorkCandidateRecentContextIsBoundedAndExplicit(t *testing.T) {
	s, _, w, task, _ := workActionFixture(t)
	task.Summary = strings.Repeat("界", 1000)
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := saveTask(ctx, q, &task); err != nil {
			return err
		}
		for range 4 {
			if err := appendWorkDecision(ctx, q, w, orchestration.WorkDecision{Kind: "ignore", Reason: strings.Repeat("文", 1000), ActorKind: "system", ActorID: "assessment"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var summary orchestration.WorkItemSummary
	err := s.readTransaction(ctx, func(tx *sql.Tx) error {
		var e error
		summary, e = candidateWorkSummary(ctx, sqlgen.New(tx), w)
		return e
	})
	if err != nil || !summary.ContextLimited || len(summary.RecentTasks) != 1 || len(summary.RecentDecisions) != 3 {
		t.Fatal(summary, err)
	}
	if len(summary.RecentTasks[0].Summary) > 2048 || len(summary.RecentTasks[0].Summary) == 0 {
		t.Fatal(summary.RecentTasks)
	}
	for _, decision := range summary.RecentDecisions {
		if len(decision.Reason) > 1024 || decision.ID == "" || decision.Seq < 1 {
			t.Fatal(decision)
		}
	}
}
