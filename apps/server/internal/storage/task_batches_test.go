package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func enableTaskInputs(t *testing.T, s *Store, runner string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE runners SET capabilities=json_set(capabilities,'$.taskInputsVersion',1) WHERE id=?`, runner); err != nil {
		t.Fatal(err)
	}
}
func batchTask(t *testing.T, s *Store, actor context.Context, room Room, writer Agent) CollaborationTask {
	t.Helper()
	task, err := s.CreateTask(actor, room.ID, CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Goal: "Preserve the full original goal.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func appendBatch(t *testing.T, s *Store, actor context.Context, task CollaborationTask, n int, content string) CollaborationTask {
	t.Helper()
	for i := range n {
		var err error
		task, err = s.OperateTask(actor, task.ID, "inputs", TaskOperation{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Content: fmt.Sprintf("%d %s", i, content)})
		if err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func TestTaskBatchesCoverOnlySuccessfulContiguousRangesWithinFiniteGrant(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := batchTask(t, s, actor, room, writer)
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	task = appendBatch(t, s, actor, task, 141, "Keep every original.")
	for round := range 6 {
		run, _ := s.GetRun(ctx, task.ActiveRunID)
		run, a := prepareTaskTestAttempt(t, s, run)
		if run.TaskInputProtocol != 1 || run.TaskInputAfter != int64(round*20) || run.TaskInputRevision != int64((round+1)*20) {
			t.Fatal("unbounded or skipped batch", run)
		}
		inputs, err := s.TaskBatchInputs(ctx, task.ID, run.TaskInputAfter, run.TaskInputRevision)
		data, _ := json.Marshal(inputs)
		if err != nil || len(inputs) != 20 || len(data) > TaskInputPageBytes {
			t.Fatal("invalid batch", err, len(data))
		}
		if round == 0 {
			task = appendBatch(t, s, actor, task, 1, "Arrived after claim.")
			stored, _ := s.GetRun(ctx, run.ID)
			if stored.TaskInputRevision != 20 {
				t.Fatal("late input changed frozen batch")
			}
		}
		run.Status = "succeeded"
		run.Intent = &CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "Ready"}
		run.ResultCommit = task.CheckpointCommit
		if _, err = s.FinishRemoteRun(ctx, a.ID, run.RunnerID, "complete-"+a.ID, strings.Repeat("f", 64), run, "Done", true, true); err != nil {
			t.Fatal(err)
		}
		task, err = s.GetTaskState(ctx, task.ID)
		if err != nil || task.CoveredInputRevision != run.TaskInputRevision {
			t.Fatal(task, err)
		}
		if round < 5 && task.Status != "queued" {
			t.Fatal(task)
		}
	}
	if task.Status != "needs_attention" || task.ActiveRunID != "" || task.TurnsUsed != 6 || task.MaxTurns != 6 || task.InputRevision != 142 || task.CoveredInputRevision != 120 || task.PublishedCommit != "" {
		t.Fatal("pending inputs silently finished or acquired allowance", task)
	}
	// Explicit continuation can consume the remaining two batches, without replaying covered originals.
	var err error
	task, err = s.OperateTask(actor, task.ID, "continue", TaskOperation{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Revision: task.Revision, AddTurns: 2, Content: "Continue with the remaining originals."})
	if err != nil {
		t.Fatal(err)
	}
	for task.ActiveRunID != "" {
		finishTaskTestRound(t, s, task, task.CheckpointCommit, CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "All batches complete."})
		task, err = s.GetTaskState(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if task.Status != "publishing" || task.CoveredInputRevision != 143 || task.TurnsUsed != 8 {
		t.Fatal(task)
	}
	if err = s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	task, _ = s.GetTaskState(ctx, task.ID)
	if task.Status != "completed" {
		t.Fatal(task)
	}
}

func TestTaskBatchEscapingFiltersPendingHistoryBeforeLimitAndGatesOldRunner(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := batchTask(t, s, actor, room, writer)
	task = appendBatch(t, s, actor, task, 130, strings.Repeat("<", 8000))
	run, _ := s.GetRun(ctx, task.ActiveRunID)
	if _, _, err := s.ClaimRemoteRun(ctx, run.ID, run.RunnerID, time.Minute); err == nil {
		t.Fatal("old Runner reserved new protocol")
	}
	state, _ := s.GetTaskState(ctx, task.ID)
	if state.TurnsUsed != 0 || state.ActiveAttemptID != "" || state.DraftFence != 0 {
		t.Fatal("incompatible Runner consumed authority", state)
	}
	enableTaskInputs(t, s, run.RunnerID)
	run, _, err := s.ClaimRemoteRun(ctx, run.ID, run.RunnerID, time.Minute)
	if err != nil || run.TaskInputAfter != 0 || run.TaskInputRevision != 1 {
		t.Fatal("escaped JSON exceeded batch budget", run, err)
	}
	inputs, err := s.TaskBatchInputs(ctx, task.ID, 0, 1)
	data, _ := json.Marshal(inputs)
	if err != nil || len(data) > TaskInputPageBytes || len(inputs) != 1 {
		t.Fatal(err, len(data))
	}
	messages, err := s.TaskContextMessages(ctx, room.ID, task.ThreadRootID, 0, run.ContextThroughSeq, task.ID, run.TaskInputRevision)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range messages {
		if m.ID == inputs[0].ID {
			found = true
		}
		if m.TaskID == task.ID && strings.HasPrefix(m.Content, "1 ") {
			t.Fatal("backlog leaked through Room context")
		}
	}
	if !found {
		t.Fatal("pending inputs displaced the eligible original before LIMIT")
	}
	for _, thread := range []string{"", task.ThreadRootID} {
		all, err := s.TaskContextMessages(ctx, room.ID, thread, 0, run.ContextThroughSeq, task.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range all {
			if strings.Contains(m.Content, strings.Repeat("<", 32)) {
				t.Fatal("future input survived filtering")
			}
		}
	}
}

func TestTaskReadCapabilityIsPrivateStableFrozenAndRevoked(t *testing.T) {
	for _, end := range []string{"cancel", "upload", "expiry", "deadline", "member", "runner", "agent", "downgrade"} {
		t.Run(end, func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			task := batchTask(t, s, actor, room, writer)
			enableTaskInputs(t, s, task.Participants[0].RunnerID)
			task = appendBatch(t, s, actor, task, 25, "Original")
			run, _ := s.GetRun(ctx, task.ActiveRunID)
			if _, _, err := s.TaskReadCapability(ctx, run.ID, run.RunnerID); err == nil {
				t.Fatal("Run identity was accepted as Attempt")
			}
			run, a := prepareTaskTestAttempt(t, s, run)
			if _, _, err := s.TaskReadCapability(ctx, a.ID, "other-runner"); err == nil {
				t.Fatal("foreign machine received capability")
			}
			token, frozen, err := s.TaskReadCapability(ctx, a.ID, run.RunnerID)
			if err != nil || len(token) != 64 || frozen.TaskInputRevision != 20 {
				t.Fatal(err, frozen)
			}
			again, _, err := s.TaskReadCapability(ctx, a.ID, run.RunnerID)
			if err != nil || token != again {
				t.Fatal("grant retry changed identity", err)
			}
			task = appendBatch(t, s, actor, task, 1, "Future input")
			for _, bad := range []struct {
				id, token string
				after     int64
				limit     int
			}{{a.ID, "wrong", 0, 20}, {"other", token, 0, 20}, {a.ID, token, 21, 20}, {a.ID, token, -1, 20}, {a.ID, token, 0, 21}, {a.ID, token, 0, 0}} {
				if _, err := s.ReadAttemptTaskInputs(ctx, bad.id, bad.token, bad.after, bad.limit); err == nil {
					t.Fatal("invalid read accepted", bad)
				}
			}
			page, err := s.ReadAttemptTaskInputs(ctx, a.ID, token, 0, 20)
			if err != nil || len(page.Items) != 20 || page.Through != 20 || page.HasMore {
				t.Fatal(page, err)
			}
			state, _ := s.GetTaskState(ctx, task.ID)
			if state.CoveredInputRevision != 0 || state.TurnsUsed != 1 || state.ModelRequestsUsed != 0 {
				t.Fatal("read changed execution accounting", state)
			}
			stored, _ := s.GetAttempt(ctx, a.ID)
			visible, _ := json.Marshal(stored)
			if strings.Contains(string(visible), token) || strings.Contains(string(stored.Command), token) {
				t.Fatal("private token leaked")
			}
			var mutationErr error
			switch end {
			case "cancel":
				_, mutationErr = s.CancelRemoteRun(actor, run.ID, "Cancelled")
			case "upload":
				_, mutationErr = s.BeginFinalizing(ctx, a.ID, run.RunnerID)
			case "expiry":
				_, mutationErr = s.db.Exec(`UPDATE run_attempts SET lease_expires_at='2000-01-01T00:00:00Z' WHERE id=?`, a.ID)
			case "deadline":
				_, mutationErr = s.db.Exec(`UPDATE run_attempts SET deadline='2000-01-01T00:00:00Z' WHERE id=?`, a.ID)
			case "member":
				_, mutationErr = s.db.Exec(`UPDATE users SET enabled=0 WHERE id=?`, actorID(actor))
			case "runner":
				_, mutationErr = s.db.Exec(`UPDATE runners SET enabled=0 WHERE id=?`, run.RunnerID)
			case "agent":
				_, mutationErr = s.db.Exec(`UPDATE agents SET enabled=0 WHERE id=?`, run.AgentID)
			case "downgrade":
				_, mutationErr = s.db.Exec(`UPDATE runners SET capabilities=json_set(capabilities,'$.taskInputsVersion',0) WHERE id=?`, run.RunnerID)
			}
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			if _, err = s.ReadAttemptTaskInputs(ctx, a.ID, token, 0, 1); err == nil {
				t.Fatal("ended or revoked Attempt read originals")
			}
		})
	}
}

func TestLegacyPendingBodiesAndActiveRoundsSurvivePromotion(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	request := TaskOperation{RequestID: "old-receipt", Content: "Legacy original"}
	receipt, err := s.OperateTask(actor, task.ID, "inputs", request)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetRun(ctx, task.ActiveRunID)
	run, a := prepareTaskTestAttempt(t, s, run)
	task = appendBatch(t, s, actor, task, 130, "New original")
	again, err := s.OperateTask(actor, task.ID, "inputs", request)
	if err != nil || again.Revision != receipt.Revision || len(again.Inputs) != 1 || again.InputProtocol != 0 {
		t.Fatal("old receipt was reinterpreted", again, err)
	}
	request.InputProtocol = 1
	request.PagedResponse = true
	if _, err = s.OperateTask(actor, task.ID, "inputs", request); err == nil {
		t.Fatal("changed protocol reused accepted request")
	}
	before := task.InputRevision
	if _, err = s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: newID(), Content: "old client new write", PagedResponse: true}); err == nil {
		t.Fatal("legacy body bypassed old limit")
	}
	state, _ := s.GetTaskState(ctx, task.ID)
	if state.InputRevision != before {
		t.Fatal("legacy refusal committed an input")
	}
	stored, _ := s.GetRun(ctx, run.ID)
	if stored.TaskInputProtocol != 0 || stored.TaskInputRevision != 1 {
		t.Fatal("promotion rewrote active legacy command")
	}
	run.Status = "failed"
	run.Error = "Uncertain old request"
	if _, err = s.FinishRemoteRun(ctx, a.ID, run.RunnerID, newID(), strings.Repeat("e", 64), run, "Failed", false, true); err != nil {
		t.Fatal(err)
	}
	state, _ = s.GetTaskState(ctx, task.ID)
	if state.CoveredInputRevision != 0 {
		t.Fatal("failure acknowledged inputs")
	}
	enableTaskInputs(t, s, run.RunnerID)
	state, err = s.OperateTask(actor, task.ID, "continue", TaskOperation{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Revision: state.Revision})
	if err != nil {
		t.Fatal(err)
	}
	next, _ := s.GetRun(ctx, state.ActiveRunID)
	next, attempt, err := s.ClaimRemoteRun(ctx, next.ID, next.RunnerID, time.Minute)
	if err != nil || next.TaskInputAfter != 0 || next.TaskInputRevision != 20 || attempt.Preparation == nil || attempt.Preparation.Resumable {
		t.Fatal("promotion resumed potentially broader legacy context", next, err)
	}
}
