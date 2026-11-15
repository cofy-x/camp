package storage

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestFinalWorkTaskReleasesOnlyUnspentReservationAndKeepsItsOriginalGrant(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, err := s.CreateWorkItemWithBudget(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Save a checked document", Start: true, ParticipantAgentIDs: []string{writer.ID}}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.GetTaskState(ctx, w.ActiveTaskID)
	if task.MaxTurns != 12 || task.MaxModelRequests != 96 || w.Usage.ReservedModelRequests != 96 {
		t.Fatal("the proposed finite grant was not frozen", task.MaxTurns, task.MaxModelRequests, w.Usage)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	run, _ := s.GetRun(ctx, task.ActiveRunID)
	run, attempt := prepareTaskTestAttempt(t, s, run)
	for range 3 {
		if err = s.BeginAttemptModel(actor, attempt.ID, run.RunnerID); err != nil {
			t.Fatal(err)
		}
		if err = s.EndAttemptModel(actor, attempt.ID); err != nil {
			t.Fatal(err)
		}
	}
	run, _ = s.GetRun(ctx, run.ID)
	run.Status, run.ResultCommit = "succeeded", strings.Repeat("b", 40)
	run.Intent = &CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "The checked document is ready."}
	if _, err = s.FinishRemoteRun(ctx, attempt.ID, run.RunnerID, newID(), strings.Repeat("f", 64), run, "Finished", true, true); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.ReconcileTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Usage.ModelRequests != 3 || w.Usage.ReservedModelRequests != 3 {
		t.Fatal("charged requests were reset or unused capacity remained reserved", w.Usage, err)
	}
	original, _ := s.GetTaskState(ctx, task.ID)
	if original.MaxModelRequests != 96 || original.ModelRequestsUsed != 3 || original.Status != "completed" {
		t.Fatal("release changed the original grant", original.MaxModelRequests, original.ModelRequestsUsed, original.Status)
	}
	_, err = s.OperateTask(actor, task.ID, "continue", TaskOperation{RequestID: newID(), Revision: original.Revision})
	requireCode(t, err, "task_finished")
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}})
	next, _ := s.GetTaskState(ctx, w.ActiveTaskID)
	if next.ID == task.ID || next.MaxModelRequests != 93 || w.Usage.ModelRequests != 3 || w.Usage.ReservedModelRequests != 96 {
		t.Fatal("a fresh finite execution did not use only the remaining matter allowance", next.MaxModelRequests, w.Usage)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_item_decisions WHERE json_extract(value,'$.kind')='allowance_released'", 1)
}

func TestWorkReservationMigrationPreservesChargedUsageTasksAndReceipts(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 35)); err != nil {
		t.Fatal(err)
	}
	actor, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, actor, "legacy")
	room, writer := remoteTestRoom(t, s, actor, runner)
	w, err := s.CreateWorkItemWithBudget(actor, room.ID, orchestration.WorkItemInput{RequestID: "retained-work", Goal: "A retained matter", Start: true, Budget: orchestration.WorkBudget{MaxEvaluations: 12, MaxTasks: 4, MaxModelRequests: 24, MaxActions: 16, MaxFollowUps: 8}, ParticipantAgentIDs: []string{writer.ID}}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	var task CollaborationTask
	var taskRaw string
	if err = s.db.QueryRow("SELECT snapshot FROM collaboration_tasks WHERE id=?", w.ActiveTaskID).Scan(&taskRaw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(taskRaw), &task); err != nil {
		t.Fatal(err)
	}
	task.Status, task.ModelRequestsUsed, task.MaxTurns = "completed", 5, 6
	beforeTask, _ := json.Marshal(task)
	w.ActiveTaskID, w.Status = "", "open"
	w.Usage.ModelRequests = 5
	beforeWork, _ := workItemSnapshot(w)
	if _, err = s.db.Exec("UPDATE collaboration_tasks SET status='completed',snapshot=? WHERE id=?", string(beforeTask), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE work_items SET status='open',value=? WHERE id=?", string(beforeWork), w.ID); err != nil {
		t.Fatal(err)
	}
	var beforeReceipt string
	if err = s.db.QueryRow("SELECT result FROM work_item_requests WHERE request_id='retained-work'").Scan(&beforeReceipt); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var afterTask, afterReceipt string
	_ = s.db.QueryRow("SELECT snapshot FROM collaboration_tasks WHERE id=?", task.ID).Scan(&afterTask)
	_ = s.db.QueryRow("SELECT result FROM work_item_requests WHERE request_id='retained-work'").Scan(&afterReceipt)
	if afterTask != string(beforeTask) || afterReceipt != beforeReceipt {
		t.Fatal("migration rewrote a task or original receipt")
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Usage.ModelRequests != 5 || w.Usage.ReservedModelRequests != 5 {
		t.Fatal("migration changed charged usage or retained unspendable capacity", w.Usage, err)
	}
	if err = s.transaction(ctx, func(tx *sql.Tx) error { return syncWorkItemTask(ctx, sqlgen.New(tx), task) }); err != nil {
		t.Fatal(err)
	}
	w, _ = s.GetWorkItem(actor, w.ID)
	if w.Usage.ReservedModelRequests != 5 {
		t.Fatal("the migrated reservation was released twice", w.Usage)
	}
}

func TestWorkReservationRetainsAnUnknownModelRequestAfterCancellation(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, err := s.CreateWorkItemWithBudget(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Keep uncertain requests accounted for", Start: true, ParticipantAgentIDs: []string{writer.ID}}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.GetTaskState(ctx, w.ActiveTaskID)
	run, _ := s.GetRun(ctx, task.ActiveRunID)
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	run, attempt := prepareTaskTestAttempt(t, s, run)
	if err = s.BeginAttemptModel(actor, attempt.ID, run.RunnerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InterruptAttempt(actor, attempt.ID, "The model response was not confirmed."); err != nil {
		t.Fatal(err)
	}
	task, _ = s.GetTaskState(ctx, task.ID)
	w, _ = s.GetWorkItem(actor, w.ID)
	if task.Status != "needs_attention" || w.Usage.ModelRequests != 1 || w.Usage.ReservedModelRequests != 96 {
		t.Fatal("recoverable execution lost its grant or unknown request", task.Status, w.Usage)
	}
	if _, err = s.OperateTask(actor, task.ID, "cancel", TaskOperation{RequestID: newID(), Revision: task.Revision}); err != nil {
		t.Fatal(err)
	}
	w, _ = s.GetWorkItem(actor, w.ID)
	if w.Usage.ModelRequests != 1 || w.Usage.ReservedModelRequests != 1 {
		t.Fatal("cancellation refunded an unknown request or retained unused authority", w.Usage)
	}
}
