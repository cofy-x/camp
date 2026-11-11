package storage

import (
	"context"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// Migration fixtures write the historical representation explicitly. Today's
// streaming owner must never operate against an unmigrated production schema.
func enqueueLegacyTaskRun(ctx context.Context, q *sqlgen.Queries, task *CollaborationTask, agentID, instruction string) error {
	participant, _ := taskParticipant(*task, agentID)
	budget := task.Budget
	run := Run{ID: newID(), TaskID: task.ID, RoomID: task.RoomID, ThreadRootID: task.ThreadRootID, OriginMessageID: task.OriginMessageID, ResourceID: task.ResourceID, InputCommit: task.CheckpointCommit, TaskInputRevision: task.InputRevision, RequestedByUserID: task.RequestedByUserID, RunnerID: participant.RunnerID, RunnerName: participant.RunnerName, AgentID: agentID, AgentSnapshot: participant.Agent, Environment: task.Environment, Prompt: instruction, Status: "queued", CreatedAt: now(), UpdatedAt: now(), Model: budget.Model, RuntimeKey: budget.RuntimeKey, MaxTokens: budget.MaxTokens, MaxSteps: budget.MaxSteps, TimeoutSeconds: budget.TimeoutSeconds, References: task.References, Files: []RunFile{}, Checks: json.RawMessage(`[]`)}
	raw, _ := json.Marshal(run)
	if err := q.CreateRun(ctx, sqlgen.CreateRunParams{ID: run.ID, RoomID: run.RoomID, ThreadRootID: nullableParent(run.ThreadRootID), OriginMessageID: run.OriginMessageID, ResourceID: run.ResourceID, AgentID: run.AgentID, Status: run.Status, Snapshot: string(raw), RequestedByUserID: run.RequestedByUserID, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}); err != nil {
		return err
	}
	task.ActiveRunID = run.ID
	task.CurrentAgentID = agentID
	task.Handoff = instruction
	return event(ctx, q, "run.queued", run.RoomID, run.ID)
}
func getLegacyRun(ctx context.Context, s *Store, id string) (Run, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT snapshot FROM runs WHERE id=?", id).Scan(&raw)
	if err != nil {
		return Run{}, err
	}
	return runFromSnapshot(raw)
}
func saveLegacyRun(ctx context.Context, q *sqlgen.Queries, run Run) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	return q.SaveRun(ctx, sqlgen.SaveRunParams{ID: run.ID, Status: run.Status, Snapshot: string(raw), UpdatedAt: run.UpdatedAt})
}
