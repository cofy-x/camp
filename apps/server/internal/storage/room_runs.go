package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func runFromSnapshot(snapshot string) (Run, error) {
	var r Run
	e := json.Unmarshal([]byte(snapshot), &r)
	if r.References == nil {
		r.References = []Reference{}
	}
	return r, e
}
func runsFromSnapshots(rows []string) ([]Run, error) {
	out := make([]Run, 0, len(rows))
	for _, s := range rows {
		r, e := runFromSnapshot(s)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func getRun(ctx context.Context, q *sqlgen.Queries, id string) (Run, error) {
	b, e := q.GetRun(ctx, id)
	if e != nil {
		return Run{}, missing(e, "The run")
	}
	return runFromSnapshot(b)
}
func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	return getRun(ctx, s.queries(), id)
}
func (s *Store) ListQueuedRuns(ctx context.Context) ([]Run, error) {
	rows, e := s.queries().ListQueuedRuns(ctx)
	if e != nil {
		return nil, e
	}
	return runsFromSnapshots(rows)
}
func (s *Store) ListUnfinishedRuns(ctx context.Context) ([]Run, error) {
	rows, e := s.queries().ListUnfinishedRuns(ctx)
	if e != nil {
		return nil, e
	}
	return runsFromSnapshots(rows)
}
func (s *Store) ListRoomRuns(ctx context.Context, room string) ([]Run, error) {
	if _, e := s.GetRoom(ctx, room); e != nil {
		return nil, e
	}
	rows, e := s.queries().ListRoomRuns(ctx, room)
	if e != nil {
		return nil, e
	}
	return runsFromSnapshots(rows)
}
func runTerminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "interrupted"
}
func validateRun(r Run) error {
	if !validText(r.Output, MaxRunOutputSize, true) || !validText(r.Error, MaxRunErrorSize, true) {
		return problem("invalid_run", "Run output exceeds the storage limit.")
	}
	switch r.Status {
	case "queued", "preparing", "running", "checking", "cancelling", "succeeded", "failed", "cancelled", "interrupted":
	default:
		return problem("invalid_run", "The run status is invalid.")
	}
	if r.Files == nil {
		r.Files = []RunFile{}
	}
	b, e := json.Marshal(r.Files)
	if e != nil || len(b) > MaxRunFilesSize {
		return problem("invalid_run", "The result files exceed the storage limit.")
	}
	for _, f := range r.Files {
		if f.Path == "" || path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || f.Path == ".." || strings.HasPrefix(f.Path, "../") || strings.ContainsAny(f.Path, "\\\x00") {
			return problem("invalid_run", "The result file path is invalid.")
		}
	}
	if len(r.Checks) > MaxRunOutputSize || len(r.Checks) > 0 && !json.Valid(r.Checks) {
		return problem("invalid_run", "The check results are invalid.")
	}
	return nil
}
func saveRun(ctx context.Context, q *sqlgen.Queries, r Run) error {
	if e := validateRun(r); e != nil {
		return e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	return q.SaveRun(ctx, sqlgen.SaveRunParams{ID: r.ID, Status: r.Status, Snapshot: string(b), UpdatedAt: r.UpdatedAt})
}
func (s *Store) ClaimRun(ctx context.Context, id string) (out Run, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		out, err = claimRun(ctx, sqlgen.New(tx), id)
		return err
	})
	return
}
func claimRun(ctx context.Context, q *sqlgen.Queries, id string) (out Run, err error) {
	err = func() error {
		r, e := getRun(ctx, q, id)
		if e != nil {
			return e
		}
		if r.Status != "queued" {
			return problem("run_busy", "The run is no longer queued.")
		}
		if r.CancellationReason != "" {
			return problem("agent_unavailable", r.CancellationReason)
		}
		if r.RequestedByUserID != "" {
			user, err := q.GetUser(ctx, r.RequestedByUserID)
			if err != nil || !user.Enabled {
				return problem("agent_unavailable", "The requesting member is disabled.")
			}
		}
		a, e := q.GetAgent(ctx, r.AgentID)
		if e != nil {
			return e
		}
		if !a.Enabled {
			return problem("agent_unavailable", "The agent is disabled.")
		}
		if _, e := q.GetMembership(ctx, sqlgen.GetMembershipParams{RoomID: r.RoomID, AgentID: r.AgentID}); e != nil {
			return problem("agent_unavailable", "The agent has left this room.")
		}
		if _, e := q.ActiveAgentRun(ctx, r.AgentID); e == nil {
			return problem("run_busy", "This agent is already working.")
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		first, e := q.FirstQueuedAgentRun(ctx, r.AgentID)
		if e != nil {
			return e
		}
		if first != r.ID {
			return problem("run_busy", "This agent has earlier queued work.")
		}
		resource, e := q.GetResource(ctx, r.ResourceID)
		if e != nil {
			return e
		}
		r.BaselineCommit = resource.CurrentCommit
		if r.TaskID != "" {
			t, e := getTask(ctx, q, r.TaskID)
			if e != nil {
				return e
			}
			r.BaselineCommit = t.CheckpointCommit
			r.InputCommit = t.CheckpointCommit
			r.TaskInputRevision = int64(len(t.Inputs))
			r.PreviousDraftCommit = t.PreviousDraftCommit
			seq, e := q.NextMessageSequence(ctx, r.RoomID)
			if e != nil {
				return e
			}
			r.ContextThroughSeq = seq - 1
		}
		r.Status = "preparing"
		r.UpdatedAt = now()
		if e := saveRun(ctx, q, r); e != nil {
			return e
		}
		out = r
		return event(ctx, q, "run.updated", r.RoomID, r.ID)
	}()
	return
}
func immutableRun(previous Run, r *Run) {
	r.Executor = previous.Executor
	r.ContextThroughSeq = previous.ContextThroughSeq
	r.PreviousDraftCommit = previous.PreviousDraftCommit
	r.TaskID = previous.TaskID
	r.InputCommit = previous.InputCommit
	r.TaskInputRevision = previous.TaskInputRevision
	r.RunnerID = previous.RunnerID
	r.RunnerName = previous.RunnerName
	r.AttemptID = previous.AttemptID
	r.WriterFence = previous.WriterFence
	r.CancellationReason = previous.CancellationReason
	r.RequestedByUserID = previous.RequestedByUserID
	r.References = previous.References
	r.ID = previous.ID
	r.RoomID = previous.RoomID
	r.ThreadRootID = previous.ThreadRootID
	r.OriginMessageID = previous.OriginMessageID
	r.AgentID = previous.AgentID
	r.AgentSnapshot = previous.AgentSnapshot
	r.ResourceID = previous.ResourceID
	r.Environment = previous.Environment
	r.Prompt = previous.Prompt
	r.Model = previous.Model
	r.RuntimeKey = previous.RuntimeKey
	r.CreatedAt = previous.CreatedAt
	r.MaxTokens = previous.MaxTokens
	r.MaxSteps = previous.MaxSteps
	r.TimeoutSeconds = previous.TimeoutSeconds
	r.BaselineCommit = previous.BaselineCommit
	r.Published = previous.Published
}
func (s *Store) UpdateRun(ctx context.Context, r Run) (Run, error) {
	return s.UpdateRunWithEvent(ctx, r, "", "")
}
func (s *Store) UpdateRunWithEvent(ctx context.Context, r Run, kind, text string) (Run, error) {
	if runTerminal(r.Status) {
		return Run{}, problem("invalid_run", "Use finalization to save a completed run.")
	}
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		old, e := getRun(ctx, q, r.ID)
		if e != nil {
			return e
		}
		if runTerminal(old.Status) {
			return problem("run_finished", "This run has already finished.")
		}
		ranks := map[string]int{"queued": 0, "preparing": 1, "running": 2, "checking": 3, "cancelling": 4}
		if ranks[r.Status] < ranks[old.Status] {
			return problem("run_busy", "A stale update cannot reverse execution progress.")
		}
		immutableRun(old, &r)
		r.UpdatedAt = now()
		if e := saveRun(ctx, q, r); e != nil {
			return e
		}
		if kind != "" {
			if _, e := appendRunEvent(ctx, q, r.ID, kind, text); e != nil {
				return e
			}
		}
		return event(ctx, q, "run.updated", r.RoomID, r.ID)
	})
	return r, e
}
func appendRunEvent(ctx context.Context, q *sqlgen.Queries, id, kind, text string) (RunEvent, error) {
	if !validText(kind, 48, false) || !validText(text, MaxRunEventSize, true) {
		return RunEvent{}, problem("invalid_event", "The execution event is invalid.")
	}
	seq, e := q.NextRunEventSequence(ctx, id)
	if e != nil {
		return RunEvent{}, e
	}
	r := RunEvent{RunID: id, Seq: seq, Type: kind, Text: text, CreatedAt: now()}
	e = q.CreateRunEvent(ctx, sqlgen.CreateRunEventParams{RunID: id, Seq: seq, Type: kind, Text: text, CreatedAt: r.CreatedAt})
	return r, e
}
func (s *Store) AppendRunEvent(ctx context.Context, id, kind, text string) (out RunEvent, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := getRun(ctx, q, id)
		if e != nil {
			return e
		}
		out, e = appendRunEvent(ctx, q, id, kind, text)
		if e != nil {
			return e
		}
		return event(ctx, q, "run.updated", r.RoomID, id)
	})
	return
}
func (s *Store) ListRunEvents(ctx context.Context, id string, after int64) ([]RunEvent, error) {
	if _, e := s.GetRun(ctx, id); e != nil {
		return nil, e
	}
	rows, e := s.queries().ListRunEvents(ctx, sqlgen.ListRunEventsParams{RunID: id, Seq: after})
	out := make([]RunEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, RunEvent{RunID: r.RunID, Seq: r.Seq, Type: r.Type, Text: r.Text, CreatedAt: r.CreatedAt})
	}
	return out, e
}

// FinishRun is the sole terminal write. A resource revision, terminal snapshot,
// final chat message, and session eligibility become visible together.
func (s *Store) FinishRun(ctx context.Context, r Run, summary string, resumable bool) (Run, error) {
	var out Run
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = finishRun(ctx, sqlgen.New(tx), r, summary, resumable)
		return err
	})
	return out, err
}

func finishRun(ctx context.Context, q *sqlgen.Queries, r Run, summary string, resumable bool) (Run, error) {
	if !runTerminal(r.Status) {
		return Run{}, problem("invalid_run", "A terminal status is required.")
	}
	e := func() error {
		old, e := getRun(ctx, q, r.ID)
		if e != nil {
			return e
		}
		if runTerminal(old.Status) {
			r = old
			return nil
		}
		immutableRun(old, &r)
		r.UpdatedAt = now()
		r.FinishedAt = r.UpdatedAt
		if r.Status == "succeeded" {
			reason, err := completionCancellation(ctx, q, old)
			if err != nil {
				return err
			}
			if reason != "" {
				r.Status = "cancelled"
				r.Error = reason + " The result was retained separately."
				resumable = false
			}
		}
		if r.TaskID == "" && r.Status == "succeeded" && r.AgentSnapshot.Access == "write" && r.ResultCommit != "" {
			if !commitValid(r.ResultCommit) {
				return problem("invalid_run", "The result commit is invalid.")
			}
			n, e := q.PublishResource(ctx, sqlgen.PublishResourceParams{ID: r.ResourceID, CurrentCommit: r.ResultCommit, CurrentCommit_2: r.BaselineCommit, UpdatedAt: r.UpdatedAt})
			if e != nil {
				return e
			}
			if n != 1 {
				r.Status = "failed"
				r.Error = "The shared files changed before publication. This result was retained separately."
				resumable = false
			} else {
				r.Published = true
				checks := "not_configured"
				if len(r.Checks) > 2 {
					checks = "passed"
				}
				if e := q.CreateResourcePublication(ctx, sqlgen.CreateResourcePublicationParams{ID: newID(), ResourceID: r.ResourceID, PreviousCommit: r.BaselineCommit, CommitID: r.ResultCommit, AuthorUserID: nullableParent(r.RequestedByUserID), RunID: nullableParent(r.ID), Kind: "agent", ChecksStatus: checks, CreatedAt: r.UpdatedAt}); e != nil {
					return e
				}
			}
		}
		if r.Files == nil {
			r.Files = []RunFile{}
		}
		if len(r.Checks) == 0 {
			r.Checks = json.RawMessage(`[]`)
		}
		if e := saveRun(ctx, q, r); e != nil {
			return e
		}
		outcome := "Execution completed."
		if r.Status != "succeeded" {
			outcome = fmt.Sprintf("Execution %s.", r.Status)
		}
		if r.Error != "" {
			outcome += " " + r.Error
		}
		if strings.TrimSpace(summary) == "" {
			summary = outcome
		} else if r.Status != "succeeded" {
			summary = outcome + "\n\nAgent output:\n" + summary
		}
		summary = truncateUTF8(summary, MaxRunOutputSize)
		seq, e := q.NextMessageSequence(ctx, r.RoomID)
		if e != nil {
			return e
		}
		m := Message{TaskID: r.TaskID, ID: newID(), RoomID: r.RoomID, ThreadRootID: r.ThreadRootID, Seq: seq, AuthorKind: "agent", AgentID: r.AgentID, RunID: r.ID, Content: summary, CreatedAt: r.UpdatedAt, UpdatedAt: r.UpdatedAt}
		if e := insertMessage(ctx, q, m, ""); e != nil {
			return e
		}
		if e := notifyRunFinished(ctx, q, r, m); e != nil {
			return e
		}
		origin, e := q.GetMessage(ctx, r.OriginMessageID)
		if e != nil {
			return e
		}
		if e := q.FinishConversation(ctx, sqlgen.FinishConversationParams{AgentID: r.AgentID, RoomID: r.RoomID, ThreadRootID: conversationScope(r), Generation: r.SessionGeneration, SessionID: r.SessionID, ContextSeq: max(origin.Seq, r.ContextThroughSeq), Resumable: resumable && r.Status == "succeeded"}); e != nil {
			return e
		}
		if _, e := appendRunEvent(ctx, q, r.ID, "status", r.Status); e != nil {
			return e
		}
		if e := finishTaskRound(ctx, q, r); e != nil {
			return e
		}
		return event(ctx, q, "run.finished", r.RoomID, r.ID)
	}()
	return r, e
}

// Recheck cancellation and access under the same transaction as publication.
// Process cleanup follows revocation, so a late native completion must not
// publish in the interval between revocation and the supervisor's cancellation.
func completionCancellation(ctx context.Context, q *sqlgen.Queries, r Run) (string, error) {
	if r.RunnerID != "" {
		if e := authorizeRunExecution(ctx, q, r); e != nil {
			return e.Error(), nil
		}
	}
	if r.CancellationReason != "" {
		return r.CancellationReason, nil
	}
	if r.Status == "cancelling" {
		return "Cancellation was requested before completion.", nil
	}
	if r.RequestedByUserID != "" {
		u, err := q.GetUser(ctx, r.RequestedByUserID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if err != nil || !u.Enabled {
			return "The requesting member was disabled before completion.", nil
		}
	}
	a, err := q.GetAgent(ctx, r.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err != nil || !a.Enabled {
		return "The agent was disabled before completion.", nil
	}
	if _, err = q.GetMembership(ctx, sqlgen.GetMembershipParams{RoomID: r.RoomID, AgentID: r.AgentID}); errors.Is(err, sql.ErrNoRows) {
		return "The agent left this room before completion.", nil
	}
	return "", err
}

// Revocation records durable intent before the supervisor stops processes. A
// later re-enable or rejoin cannot revive requests that were already revoked.
func revokeRuns(ctx context.Context, q *sqlgen.Queries, reason string, matches func(Run) bool) error {
	queued, err := q.ListQueuedRuns(ctx)
	if err != nil {
		return err
	}
	active, err := q.ListUnfinishedRuns(ctx)
	if err != nil {
		return err
	}
	for _, snapshot := range append(queued, active...) {
		run, err := runFromSnapshot(snapshot)
		if err != nil {
			return err
		}
		if run.CancellationReason != "" || !matches(run) {
			continue
		}
		if run.RunnerID != "" {
			if _, err = cancelRemoteRun(ctx, q, run, reason); err != nil {
				return err
			}
			continue
		}
		run.CancellationReason = reason
		run.UpdatedAt = now()
		if err = saveRun(ctx, q, run); err != nil {
			return err
		}
		if err = event(ctx, q, "run.updated", run.RoomID, run.ID); err != nil {
			return err
		}
	}
	return revokeTasks(ctx, q, reason, matches)
}

func (s *Store) GetConversation(ctx context.Context, r Run) (out Conversation, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		old, e := q.GetConversation(ctx, sqlgen.GetConversationParams{AgentID: r.AgentID, RoomID: r.RoomID, ThreadRootID: conversationScope(r)})
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		runnerKey := ""
		if r.RunnerID != "" {
			runner, err := q.GetRunner(ctx, r.RunnerID)
			if err != nil {
				return err
			}
			var capability struct {
				Fingerprint string `json:"fingerprint"`
			}
			if err := json.Unmarshal([]byte(runner.Capabilities), &capability); err != nil {
				return err
			}
			runnerKey = "\x00" + r.RunnerID + "\x00" + runner.StorageID + "\x00" + runner.Runtime + "\x00" + capability.Fingerprint
		}
		runtimeHash := sha256.Sum256([]byte(r.RuntimeKey + "\x00" + string(r.Environment) + runnerKey))
		runtimeKey := hex.EncodeToString(runtimeHash[:])
		out = Conversation{RuntimeKey: runtimeKey, ID: old.ID, AgentID: r.AgentID, RoomID: r.RoomID, ThreadRootID: conversationScope(r), ResourceID: r.ResourceID, AgentRevision: r.AgentSnapshot.Revision, Generation: old.Generation, ContextSeq: old.ContextSeq, SessionID: old.SessionID, Resumable: old.Resumable}
		if out.ID == "" {
			out.ID = newID()
		}
		if !old.Resumable || old.AgentRevision != out.AgentRevision || old.ResourceID != r.ResourceID || old.RuntimeKey != runtimeKey {
			out.Generation++
			out.ContextSeq = 0
			out.SessionID = "camp-" + newID()
			out.Resumable = false
		}
		return q.SaveConversation(ctx, sqlgen.SaveConversationParams{RuntimeKey: runtimeKey, ID: out.ID, AgentID: out.AgentID, RoomID: out.RoomID, ThreadRootID: out.ThreadRootID, ResourceID: out.ResourceID, AgentRevision: out.AgentRevision, Generation: out.Generation, ContextSeq: out.ContextSeq, SessionID: out.SessionID, Resumable: false})
	})
	return
}

func conversationScope(r Run) string {
	if r.TaskID != "" {
		return "task:" + r.TaskID
	}
	return r.ThreadRootID
}
