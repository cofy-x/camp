package storage

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestWorkDecisionRecoveryFailureCannotBecomeRecoverableRejection(t *testing.T) {
	rejection := problem("invalid_work_item", "The proposal is invalid")
	var known *Error
	if !errors.As(workDecisionRejection(rejection, nil, nil), &known) {
		t.Fatal("successful rollback lost advisory rejection")
	}
	rollback, release := errors.New("rollback failed"), errors.New("release failed")
	for _, pair := range [][2]error{{rollback, nil}, {nil, release}, {rollback, release}} {
		err := workDecisionRejection(rejection, pair[0], pair[1])
		if errors.As(err, &known) {
			t.Fatal("failed rollback was classified as recoverable", err)
		}
		if pair[0] != nil && !errors.Is(err, rollback) || pair[1] != nil && !errors.Is(err, release) {
			t.Fatal("recovery failure lost its cause", err)
		}
	}
}

func TestWorkActionBudgetChargesOnlyDispatchAndNeverReadOnlyRecovery(t *testing.T) {
	s, task, _ := automationDeliveryFixture(t)
	actor := WithUser(ctx, User{ID: task.RequestedByUserID})
	room, err := s.GetRoom(actor, task.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	w := historyWorkItem(t, s, actor, room)
	if err = s.transaction(ctx, func(tx *sql.Tx) error {
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
		return queueAutomationTaskNotice(ctx, q, task, "task_attention", "Check delivery")
	}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := s.TaskAutomationDeliveries(actor, task.ID)
	if err != nil || len(deliveries) != 2 {
		t.Fatal(deliveries, err)
	}
	for _, delivery := range deliveries {
		delivery.Attempts = make([]orchestration.DeliveryAttempt, maxAutomationDeliveryAttempts)
		for i := range delivery.Attempts {
			delivery.Attempts[i] = orchestration.DeliveryAttempt{ActorID: 9, CredentialRef: strings.Repeat("d", 48), Outcome: "failed"}
		}
		if err = saveAutomationDelivery(ctx, s.queries(), delivery); err != nil {
			t.Fatal(err)
		}
	}
	claim, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || claim != nil {
		t.Fatal("attempt cap allowed dispatch", claim, err)
	}
	current, err := s.GetWorkItem(actor, w.ID)
	if err != nil || current.Usage.Actions != 0 {
		t.Fatal("blocked dispatch consumed action budget", current.Usage, err)
	}
	row, err := s.queries().GetAutomationDelivery(ctx, deliveries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := automationDelivery(row)
	if err != nil {
		t.Fatal(err)
	}
	delivery.Attempts = nil
	delivery.Status = "pending"
	if err = saveAutomationDelivery(ctx, s.queries(), delivery); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimAutomationDelivery(ctx)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if err = s.CompleteAutomationDelivery(ctx, claim.ID, orchestration.DeliveryResult{ClaimRevision: claim.Revision, Status: "unknown", Reason: "Response lost"}); err != nil {
		t.Fatal(err)
	}
	row, err = s.queries().GetAutomationDelivery(ctx, claim.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = automationDelivery(row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OperateAutomationDelivery(actor, delivery.ID, "reconcile", AutomationDeliveryInput{RequestID: newID(), Revision: delivery.Revision}); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimAutomationDelivery(ctx)
	if err != nil || claim == nil || !claim.Reconcile {
		t.Fatal(claim, err)
	}
	current, err = s.GetWorkItem(actor, w.ID)
	if err != nil || current.Usage.Actions != 1 {
		t.Fatal("read-only reconciliation consumed external action budget", current.Usage, err)
	}
}

func TestWorkTimerHumanEscalationSurvivesExhaustedExternalActionBudget(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	w := historyWorkItem(t, s, actor, room)
	w.Status = "waiting"
	w.Usage.Actions = w.Budget.MaxActions
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := saveWorkItem(ctx, q, &w); err != nil {
			return err
		}
		return q.InsertWorkTimer(ctx, sqlgen.InsertWorkTimerParams{ID: newID(), WorkItemID: w.ID, DueAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), Condition: "Review the unanswered request", CreatedAt: now()})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.FireWorkItemTimers(ctx); err != nil {
		t.Fatal(err)
	}
	inbox, err := s.ListInbox(actor)
	if err != nil || len(inbox) != 1 || inbox[0].Kind != "work_item_attention" {
		t.Fatal("action limit prevented human escalation", inbox, err)
	}
	current, err := s.GetWorkItem(actor, w.ID)
	if err != nil || current.Usage.Actions != w.Budget.MaxActions || current.Status != "needs_attention" {
		t.Fatal(current, err)
	}
}
