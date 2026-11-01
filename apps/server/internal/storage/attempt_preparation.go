package storage

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func freezeAttemptPreparation(ctx context.Context, q *sqlgen.Queries, r Run, a Attempt) (Attempt, error) {
	if a.Preparation != nil {
		return a, nil
	}
	var conv Conversation
	var err error
	// Older versions could save session metadata before committing the start
	// command. Preserve that reservation when upgrading an unsent Attempt.
	if r.SessionID != "" && r.SessionGeneration > 0 {
		old, e := q.GetConversation(ctx, sqlgen.GetConversationParams{AgentID: r.AgentID, RoomID: r.RoomID, ThreadRootID: conversationScope(r)})
		if e != nil {
			return a, e
		}
		key, e := conversationRuntimeKey(ctx, q, r)
		if e != nil {
			return a, e
		}
		if old.SessionID != r.SessionID || old.Generation != r.SessionGeneration || old.RuntimeKey != key || old.ResourceID != r.ResourceID || old.AgentRevision != r.AgentSnapshot.Revision {
			return a, problem("stale_attempt", "The previously prepared session is no longer current.")
		}
		conv = Conversation{ID: old.ID, RuntimeKey: key, AgentID: r.AgentID, RoomID: r.RoomID, ThreadRootID: conversationScope(r), ResourceID: r.ResourceID, AgentRevision: r.AgentSnapshot.Revision, Generation: old.Generation, ContextSeq: old.ContextSeq, SessionID: old.SessionID, Resumable: r.SessionMode == "resume"}
	} else {
		conv, err = prepareConversation(ctx, q, r)
		if err != nil {
			return a, err
		}
	}
	raw, err := json.Marshal(conv)
	if err != nil {
		return a, err
	}
	n, err := q.FreezeAttemptPreparation(ctx, sqlgen.FreezeAttemptPreparationParams{ID: a.ID, PreparationContext: string(raw)})
	if err != nil {
		return a, err
	}
	if n != 1 {
		return a, problem("attempt_conflict", "The preparation identity was already reserved.")
	}
	a.Preparation = &conv
	return a, nil
}

// FreezeAttemptPreparation upgrades an eligible legacy unsent Attempt once.
// New claims reserve this context inside their original admission transaction.
func (s *Store) FreezeAttemptPreparation(ctx context.Context, id string) (out Attempt, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if e = validateAttempt(ctx, q, a, a.RunnerID, false); e != nil {
			return e
		}
		if a.Phase != "dispatching" || len(a.Command) > 0 || remoteExpired(a.Deadline) {
			return problem("attempt_conflict", "Only an unsent authorized Attempt can prepare inputs.")
		}
		r, e := getRun(ctx, q, a.RunID)
		if e != nil {
			return e
		}
		out, e = freezeAttemptPreparation(ctx, q, r, a)
		return e
	})
	return
}

// PreparedAttempt is built outside a business transaction. Activation is the
// only production writer of the manifests, session metadata and start command.
type PreparedAttempt struct {
	Conversation   Conversation
	InputDigest    string
	InputPath      string
	Command        json.RawMessage
	ModelTokenHash string
}

func (s *Store) ActivateAttempt(ctx context.Context, id string, in PreparedAttempt) (out Attempt, err error) {
	if len(in.InputDigest) != 64 || !validText(in.InputPath, 4096, false) || len(in.ModelTokenHash) != 64 {
		return out, problem("invalid_artifact", "Prepared input requires an immutable manifest and capability hash.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if len(a.Command) > 0 {
			if a.ModelTokenHash != in.ModelTokenHash || string(a.Command) != string(in.Command) || a.InputDigest != in.InputDigest || a.InputPath != in.InputPath || a.Preparation == nil || *a.Preparation != in.Conversation {
				return problem("request_conflict", "This Attempt already has a different prepared command.")
			}
			out = a
			return nil
		}
		if e = validateAttempt(ctx, q, a, a.RunnerID, false); e != nil {
			return e
		}
		if a.Phase != "dispatching" || remoteExpired(a.Deadline) {
			return problem("attempt_expired", "This Attempt can no longer activate execution.")
		}
		if a.Preparation == nil || *a.Preparation != in.Conversation {
			return problem("stale_attempt", "The prepared context does not match this Attempt's frozen identity.")
		}
		r, e := getRun(ctx, q, a.RunID)
		if e != nil {
			return e
		}
		key, e := conversationRuntimeKey(ctx, q, r)
		if e != nil {
			return e
		}
		conv := in.Conversation
		current, e := q.GetConversation(ctx, sqlgen.GetConversationParams{AgentID: r.AgentID, RoomID: r.RoomID, ThreadRootID: conversationScope(r)})
		if e != nil {
			return e
		}
		if key != conv.RuntimeKey || current.ID != conv.ID || current.SessionID != conv.SessionID || current.Generation != conv.Generation || current.RuntimeKey != conv.RuntimeKey || current.ResourceID != r.ResourceID || current.AgentRevision != r.AgentSnapshot.Revision || current.ContextSeq != conv.ContextSeq {
			return problem("stale_attempt", "The assigned Runner or native context changed during preparation.")
		}
		if r.TaskID == "" && r.AgentSnapshot.Access == "write" {
			resource, e := q.GetResource(ctx, r.ResourceID)
			if e != nil {
				return e
			}
			if resource.CurrentCommit != r.BaselineCommit {
				return problem("resource_conflict", "Room files changed during writer preparation.")
			}
		}
		if a.InputDigest != "" && (a.InputDigest != in.InputDigest || a.InputPath != in.InputPath) {
			return problem("request_conflict", "This Attempt already froze different input bytes.")
		}
		r.SessionID, r.SessionGeneration, r.SessionMode = conv.SessionID, conv.Generation, "new"
		if conv.Resumable {
			r.SessionMode = "resume"
		}
		r.UpdatedAt = now()
		if e = saveRun(ctx, q, r); e != nil {
			return e
		}
		if !conv.Resumable && conv.Generation > 1 {
			if e = addRunNotice(ctx, q, r, "Rebuilding context on the assigned runner from visible messages. Previous requests are not replayed."); e != nil {
				return e
			}
		}
		if e = q.SaveAttemptInput(ctx, sqlgen.SaveAttemptInputParams{ID: id, InputDigest: in.InputDigest, InputPath: in.InputPath, UpdatedAt: now()}); e != nil {
			return e
		}
		if e = createRunnerCommand(ctx, q, a.CommandID, a.RunnerID, a.ID, "run.start", in.Command); e != nil {
			return e
		}
		if e = q.SetAttemptCapability(ctx, sqlgen.SetAttemptCapabilityParams{ID: id, CapabilityHash: in.ModelTokenHash}); e != nil {
			return e
		}
		if e = event(ctx, q, "run.updated", r.RoomID, r.ID); e != nil {
			return e
		}
		out, e = getAttempt(ctx, q, id)
		return e
	})
	return
}
