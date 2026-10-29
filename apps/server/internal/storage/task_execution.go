package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func reserveTaskAttempt(ctx context.Context, q *sqlgen.Queries, r Run, id string) (int64, error) {
	t, e := getTask(ctx, q, r.TaskID)
	if e != nil {
		return 0, e
	}
	if t.Status != "queued" || t.ActiveRunID != r.ID || t.ActiveAttemptID != "" {
		return 0, problem("stale_attempt", "The collaboration is not waiting for this round.")
	}
	if t.TurnsUsed >= t.MaxTurns || t.ModelRequestsUsed >= t.MaxModelRequests {
		return 0, problem("task_budget_exhausted", "The collaboration requires additional authorization.")
	}
	if e = authorizeTask(ctx, q, t); e != nil {
		return 0, e
	}
	t.TurnsUsed++
	t.DraftFence++
	t.ActiveAttemptID = id
	t.Status = "running"
	return t.DraftFence, saveTask(ctx, q, &t)
}
func authorizeTask(ctx context.Context, q *sqlgen.Queries, t CollaborationTask) error {
	for _, p := range t.Participants {
		r := Run{RequestedByUserID: t.RequestedByUserID, AgentID: p.Agent.ID, AgentSnapshot: p.Agent, RoomID: t.RoomID, RunnerID: p.RunnerID}
		if e := authorizeRunExecution(ctx, q, r); e != nil {
			return e
		}
	}
	return nil
}
func validateTaskAttempt(ctx context.Context, q *sqlgen.Queries, r Run, a Attempt, allowCancelled bool) error {
	t, e := getTask(ctx, q, r.TaskID)
	if e != nil {
		return e
	}
	if t.ActiveRunID != r.ID || t.ActiveAttemptID != a.ID || t.DraftFence != a.WriterFence {
		return problem("stale_attempt", "The collaboration execution fence is no longer current.")
	}
	if t.CheckpointCommit != r.InputCommit || r.BaselineCommit != r.InputCommit {
		return problem("stale_attempt", "The collaboration draft changed before this result.")
	}
	if !allowCancelled {
		if t.Status != "running" {
			return problem("run_cancelled", "This collaboration no longer authorizes execution.")
		}
		if e = authorizeTask(ctx, q, t); e != nil {
			return problem("run_cancelled", e.Error())
		}
	}
	return nil
}
func chargeTaskModel(ctx context.Context, q *sqlgen.Queries, runID string) error {
	r, e := getRun(ctx, q, runID)
	if e != nil || r.TaskID == "" {
		return e
	}
	t, e := getTask(ctx, q, r.TaskID)
	if e != nil {
		return e
	}
	if t.ModelRequestsUsed >= t.MaxModelRequests {
		return problem("task_budget_exhausted", "The collaboration model request allowance is exhausted.")
	}
	t.ModelRequestsUsed++
	return saveTask(ctx, q, &t)
}
func finishTaskRound(ctx context.Context, q *sqlgen.Queries, r Run) error {
	if r.TaskID == "" {
		return nil
	}
	t, e := getTask(ctx, q, r.TaskID)
	if e != nil {
		return e
	}
	if t.ActiveRunID != r.ID {
		return problem("stale_attempt", "This collaboration has a different active round.")
	}
	t.ActiveRunID = ""
	t.ActiveAttemptID = ""
	t.ProcessedInputRevision = max(t.ProcessedInputRevision, r.TaskInputRevision)
	if t.Status == "cancelling" {
		if r.Status == "interrupted" {
			t.Status = "interrupted"
			t.Error = r.Error
		} else {
			t.Status = "cancelled"
			t.Error = ""
		}
	} else if r.Status != "succeeded" {
		t.Status = "needs_attention"
		t.Error = r.Error
		if t.Error == "" {
			t.Error = "The last execution did not complete. Review its details before explicitly continuing."
		}
	} else {
		if e = validateIntent(t, r, r.Intent); e != nil {
			return e
		}
		if r.ResultCommit != "" {
			t.CheckpointCommit = r.ResultCommit
		}
		t.CheckpointRevision++
		switch r.Intent.Kind {
		case "handoff":
			e = enqueueTaskRound(ctx, q, &t, r.Intent.TargetAgentID, r.Intent.Instruction)
		case "ask_person":
			t.Status = "waiting_for_person"
			t.Question = &TaskQuestion{ID: newID(), Text: r.Intent.Question, Options: r.Intent.Options}
			if t.Question.Options == nil {
				t.Question.Options = []string{}
			}
		case "finish":
			t.Summary = r.Intent.Summary
			if int64(len(t.Inputs)) > r.TaskInputRevision {
				e = enqueueTaskRound(ctx, q, &t, r.AgentID, "New task inputs arrived during execution. Process all pending inputs before finishing.")
			} else {
				t.Status = "publishing"
				t.PublicationRunID = r.ID
			}
		}
		if e != nil {
			var known *Error
			if !errors.As(e, &known) {
				return e
			}
			t.Status = "needs_attention"
			t.Error = e.Error()
			t.ActiveRunID = ""
		}
	}
	if e = saveTask(ctx, q, &t); e != nil {
		return e
	}
	switch t.Status {
	case "needs_attention", "interrupted":
		return taskNotice(ctx, q, t, "task_attention", t.Error)
	case "waiting_for_person":
		return taskNotice(ctx, q, t, "task_question", t.Question.Text)
	}
	return nil
}

// ReconcileTask is safe after a server restart. Publication requires only a
// durable checkpoint; it never requires the runner to reconnect or reexecute.
func (s *Store) ReconcileTask(ctx context.Context, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) (txErr error) {
		q := sqlgen.New(tx)
		t, e := getTask(ctx, q, id)
		if e != nil {
			return e
		}
		if t.Status == "completed" || t.Status == "cancelled" || t.Status == "interrupted" || t.Status == "cancelling" {
			return nil
		}
		authority := authorizeTask(ctx, q, t)
		if authority != nil {
			// Revocation is sticky: a later regrant never silently resumes this task.
			if t.ActiveRunID != "" {
				r, e := getRun(ctx, q, t.ActiveRunID)
				if e != nil {
					return e
				}
				_, e = cancelRemoteRun(ctx, q, r, authority.Error())
				return e
			}
			if t.Status != "needs_attention" || t.Error == "" {
				t.Status = "needs_attention"
				t.Error = authority.Error()
				if e = saveTask(ctx, q, &t); e != nil {
					return e
				}
				return taskNotice(ctx, q, t, "task_attention", t.Error)
			}
			return nil
		}
		if t.Status != "publishing" {
			return nil
		}
		if int64(len(t.Inputs)) > t.ProcessedInputRevision {
			if e = enqueueTaskRound(ctx, q, &t, t.CurrentAgentID, "Process the task inputs received before publication."); e != nil {
				return e
			}
			return saveTask(ctx, q, &t)
		}
		r, e := getRun(ctx, q, t.PublicationRunID)
		if e != nil {
			return e
		}
		if r.Status != "succeeded" || r.Intent == nil || r.Intent.Kind != "finish" {
			return problem("invalid_task", "The final checkpoint has no successful finish intent.")
		}
		if t.CheckpointCommit != t.BaseSharedCommit {
			lease, e := acquireResourceLease(ctx, q, t.ResourceID, "task:"+t.ID, time.Now().Add(time.Minute))
			if e != nil {
				var p *Error
				if errors.As(e, &p) && p.Code == "resource_busy" {
					return nil
				}
				return e
			}
			defer func() {
				_, releaseErr := q.ReleaseResourceLease(ctx, sqlgen.ReleaseResourceLeaseParams{ResourceID: t.ResourceID, Owner: lease.Owner, Fence: lease.Fence})
				txErr = errors.Join(txErr, releaseErr)
			}()
			resource, e := q.GetResource(ctx, t.ResourceID)
			if e != nil {
				return e
			}
			if resource.CurrentCommit != t.BaseSharedCommit {
				t.Status = "needs_attention"
				t.Error = "Room files changed while this collaboration was working. The task draft is saved. Continue from latest files to reconcile the changes."
				if e = saveTask(ctx, q, &t); e != nil {
					return e
				}
				return taskNotice(ctx, q, t, "task_attention", t.Error)
			}
			n, e := q.PublishResource(ctx, sqlgen.PublishResourceParams{ID: t.ResourceID, CurrentCommit: t.CheckpointCommit, CurrentCommit_2: t.BaseSharedCommit, UpdatedAt: now()})
			if e != nil {
				return e
			}
			if n != 1 {
				return problem("revision_conflict", "Room files changed before publication.")
			}
			checks := "not_configured"
			if len(r.Checks) > 2 {
				checks = "passed"
			}
			publicationID := newID()
			if e = q.CreateResourcePublication(ctx, sqlgen.CreateResourcePublicationParams{ID: publicationID, ResourceID: t.ResourceID, PreviousCommit: t.BaseSharedCommit, CommitID: t.CheckpointCommit, AuthorUserID: nullableParent(t.RequestedByUserID), RunID: nullableParent(r.ID), Kind: "agent", ChecksStatus: checks, CreatedAt: now()}); e != nil {
				return e
			}
			if e = q.SetPublicationTask(ctx, sqlgen.SetPublicationTaskParams{ID: publicationID, TaskID: t.ID}); e != nil {
				return e
			}
			r.Published = true
			r.UpdatedAt = now()
			if e = saveRun(ctx, q, r); e != nil {
				return e
			}
		}
		t.Status = "completed"
		t.PublishedCommit = t.CheckpointCommit
		t.Error = ""
		if e = saveTask(ctx, q, &t); e != nil {
			return e
		}
		return taskNotice(ctx, q, t, "task_finished", "Collaboration completed. "+t.Summary)
	})
}

// Revoke waiting collaborations in the same transaction as the authority change.
// Observing a later regrant must never erase an intervening revocation.
func revokeTasks(ctx context.Context, q *sqlgen.Queries, reason string, matches func(Run) bool) error {
	rows, e := q.ListOpenTasks(ctx)
	if e != nil {
		return e
	}
	for _, raw := range rows {
		t, e := taskSnapshot(raw)
		if e != nil {
			return e
		}
		affected := false
		for _, p := range t.Participants {
			if matches(Run{TaskID: t.ID, RoomID: t.RoomID, ResourceID: t.ResourceID, RequestedByUserID: t.RequestedByUserID, AgentID: p.Agent.ID, AgentSnapshot: p.Agent, RunnerID: p.RunnerID}) {
				affected = true
				break
			}
		}
		if !affected || t.Status == "cancelling" {
			continue
		}
		if t.ActiveRunID != "" {
			r, e := getRun(ctx, q, t.ActiveRunID)
			if e != nil {
				return e
			}
			if _, e = cancelRemoteRun(ctx, q, r, reason); e != nil {
				return e
			}
			continue
		}
		if t.Status == "needs_attention" && t.Error == reason {
			continue
		}
		t.Status = "needs_attention"
		t.Error = reason
		if e = saveTask(ctx, q, &t); e != nil {
			return e
		}
		if e = taskNotice(ctx, q, t, "task_attention", reason); e != nil {
			return e
		}
	}
	return nil
}
