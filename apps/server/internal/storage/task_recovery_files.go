package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// TaskRecoveryRun describes retained partial work, never a successful checkpoint
// or permission to execute. The continuation transaction rechecks its provenance.
type TaskRecoveryRun struct {
	RunID     string `json:"runId"`
	Commit    string `json:"commit"`
	AgentName string `json:"agentName"`
	FileCount int    `json:"fileCount"`
}

func recoverableTaskRun(ctx context.Context, q *sqlgen.Queries, task CollaborationTask, id string) (Run, error) {
	invalid := func() (Run, error) {
		return Run{}, problem("invalid_task_recovery", "The captured files no longer match this task draft. Refresh and choose starting files again.")
	}
	if task.Status != "needs_attention" || task.ActiveRunID != "" || task.ActiveAttemptID != "" || task.DraftFence < 1 || !validText(id, 128, false) {
		return invalid()
	}
	run, err := getRun(ctx, q, id)
	if err != nil {
		return Run{}, err
	}
	if run.TaskID != task.ID || run.RoomID != task.RoomID || run.ResourceID != task.ResourceID || run.AgentSnapshot.Access != "write" ||
		(run.Status != "failed" && run.Status != "interrupted") || run.Published || !commitValid(run.ResultCommit) || run.ResultCommit == task.CheckpointCommit ||
		run.InputCommit != task.CheckpointCommit || run.BaselineCommit != run.InputCommit || run.WriterFence != task.DraftFence || run.AttemptID == "" {
		return invalid()
	}
	attempt, err := getAttempt(ctx, q, run.AttemptID)
	if err != nil {
		return Run{}, err
	}
	var receipt struct {
		Disposition      string `json:"disposition"`
		RunID            string `json:"runId"`
		Status           string `json:"status"`
		CleanupConfirmed bool   `json:"cleanupConfirmed"`
	}
	if attempt.RunID != run.ID || attempt.RunnerID != run.RunnerID || attempt.WriterFence != task.DraftFence || attempt.Phase != "finished" ||
		attempt.CompletionID == "" || len(attempt.ResultDigest) != 64 || json.Unmarshal(attempt.Receipt, &receipt) != nil ||
		receipt.Disposition != "committed" || receipt.RunID != run.ID || receipt.Status != run.Status || !receipt.CleanupConfirmed {
		return invalid()
	}
	return run, nil
}

// TaskRecoveryCandidate reads one current-fence result, regardless of history
// length. It does not expose an older failed result after a later turn started.
func (s *Store) TaskRecoveryCandidate(ctx context.Context, id string) (*TaskRecoveryRun, error) {
	task, err := getTask(ctx, s.queries(), id)
	if err != nil {
		return nil, err
	}
	if task.Status != "needs_attention" || task.ActiveRunID != "" || task.ActiveAttemptID != "" || task.DraftFence < 1 {
		return nil, nil
	}
	runID, err := s.queries().GetTaskDraftRun(ctx, sqlgen.GetTaskDraftRunParams{TaskID: task.ID, DraftFence: task.DraftFence})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run, err := recoverableTaskRun(ctx, s.queries(), task, runID)
	var problem *Error
	if errors.As(err, &problem) && (problem.Code == "invalid_task_recovery" || problem.Code == "not_found") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &TaskRecoveryRun{RunID: run.ID, Commit: run.ResultCommit, AgentName: run.AgentSnapshot.Name, FileCount: len(run.Files)}, nil
}

// RecoveryRunFiles is a preflight for immutable file verification. The owning
// transaction still checks the exact revision, authority, draft fence and receipt.
func (s *Store) RecoveryRunFiles(ctx context.Context, id, runID string) (Run, error) {
	if err := requireActor(ctx, s.queries(), false); err != nil {
		return Run{}, err
	}
	task, err := getTask(ctx, s.queries(), id)
	if err != nil {
		return Run{}, err
	}
	if err = taskManage(ctx, s.queries(), task); err != nil {
		return Run{}, err
	}
	return recoverableTaskRun(ctx, s.queries(), task, runID)
}

func (s *Store) FindTaskOperation(ctx context.Context, id, action string, in TaskOperation) (CollaborationTask, bool, error) {
	if err := requireActor(ctx, s.queries(), false); err != nil {
		return CollaborationTask{}, false, err
	}
	task, found, _, err := taskReceipt(ctx, s.queries(), action+":"+id, in.RequestID, in)
	if found && err == nil {
		task, err = taskOutput(ctx, s.queries(), task, nil, in.PagedResponse)
	}
	return task, found, err
}
