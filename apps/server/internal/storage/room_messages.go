package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func newID() string { return id() }
func messageRow(m sqlgen.Message) (Message, error) {
	out := Message{TaskID: m.TaskID, ReviewID: m.ReviewID, AuthorUserID: m.AuthorUserID, AuthorName: m.AuthorName, ID: m.ID, RoomID: m.RoomID, ThreadRootID: m.ThreadRootID.String, Seq: m.Seq, AuthorKind: m.AuthorKind, AgentID: m.AgentID.String, RunID: m.RunID, Content: m.Content, RequestID: m.RequestID, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, References: []Reference{}}
	err := json.Unmarshal([]byte(m.ReferencesJson), &out.References)
	return out, err
}
func (s *Store) GetMessage(ctx context.Context, id string) (Message, error) {
	m, e := s.queries().GetMessage(ctx, id)
	if e != nil {
		return Message{}, missing(e, "The message")
	}
	return messageRow(m)
}
func (s *Store) GetRoomMessage(ctx context.Context, room, id string) (Message, error) {
	m, err := s.GetMessage(ctx, id)
	if err != nil {
		return Message{}, err
	}
	if m.RoomID != room {
		return Message{}, problem("not_found", "The message does not exist in this room.")
	}
	return m, nil
}
func (s *Store) ListMessages(ctx context.Context, room, thread string, after int64) ([]Message, error) {
	if _, e := s.GetRoom(ctx, room); e != nil {
		return nil, e
	}
	rows, e := s.queries().ListMessages(ctx, sqlgen.ListMessagesParams{RoomID: room, ThreadRootID: sql.NullString{String: thread, Valid: true}, AfterSeq: after})
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		message, err := messageRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, e
}
func (s *Store) ContextMessages(ctx context.Context, room, thread string, after, through int64) ([]Message, error) {
	rows, e := s.queries().ContextMessages(ctx, sqlgen.ContextMessagesParams{RoomID: room, ThreadRootID: sql.NullString{String: thread, Valid: true}, AfterSeq: after, ThroughSeq: through})
	out := make([]Message, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		message, err := messageRow(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, e
}
func normalizedSend(in SendMessageInput) (SendMessageInput, string, error) {
	in.Content = strings.TrimSpace(in.Content)
	in.RequestID = strings.TrimSpace(in.RequestID)
	if !validText(in.Content, MaxRunPromptSize, false) || !validText(in.RequestID, 128, false) || len(in.RecipientAgentIDs) > 16 {
		return in, "", problem("invalid_message", "Provide a message, a request ID, and at most 16 recipients.")
	}
	if in.PostOnly && len(in.RecipientAgentIDs) > 0 {
		return in, "", problem("invalid_message", "A post-only message cannot address an agent.")
	}
	if err := validateReferencesInput(in.References); err != nil {
		return in, "", err
	}
	if in.RecipientAgentIDs != nil {
		in.RecipientAgentIDs = slices.Clone(in.RecipientAgentIDs)
		slices.Sort(in.RecipientAgentIDs)
		in.RecipientAgentIDs = slices.Compact(in.RecipientAgentIDs)
	}
	b, _ := json.Marshal(in)
	sum := sha256.Sum256(b)
	return in, hex.EncodeToString(sum[:]), nil
}
func findSend(ctx context.Context, q *sqlgen.Queries, room string, in SendMessageInput, fingerprint string) (SendResult, bool, error) {
	m, e := q.FindMessageRequest(ctx, sqlgen.FindMessageRequestParams{RoomID: room, AuthorUserID: actorID(ctx), RequestID: in.RequestID})
	if errors.Is(e, sql.ErrNoRows) {
		return SendResult{}, false, nil
	}
	if e != nil {
		return SendResult{}, false, e
	}
	if m.Fingerprint != fingerprint {
		return SendResult{}, false, problem("request_conflict", "This request ID belongs to a different message.")
	}
	snapshots, e := q.ListMessageRuns(ctx, m.ID)
	if e != nil {
		return SendResult{}, false, e
	}
	runs, e := runsFromSnapshots(snapshots)
	if e != nil {
		return SendResult{}, false, e
	}
	message, e := messageRow(m)
	return SendResult{Message: message, Runs: runs, Replayed: true}, true, e
}
func (s *Store) FindSendMessage(ctx context.Context, room string, input SendMessageInput) (SendResult, bool, error) {
	in, f, e := normalizedSend(input)
	if e != nil {
		return SendResult{}, false, e
	}
	return findSend(ctx, s.queries(), room, in, f)
}
func (s *Store) SendMessage(ctx context.Context, room string, input SendMessageInput, b Budget) (result SendResult, err error) {
	in, f, e := normalizedSend(input)
	if e != nil {
		return result, e
	}
	if b.MaxTokens < 1 || b.MaxTokens > 65536 || b.MaxSteps < 1 || b.MaxSteps > 100 || b.TimeoutSeconds < 1 || b.TimeoutSeconds > 3600 {
		return result, problem("invalid_run", "The execution budget is invalid.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		actor, authenticated := UserFromContext(ctx)
		if authenticated {
			current, err := q.GetUser(ctx, actor.ID)
			if err != nil || !current.Enabled {
				return problem("forbidden", "This account is disabled.")
			}
			actor = userRow(current)
		}
		existing, found, e := findSend(ctx, q, room, in, f)
		if e != nil {
			return e
		}
		if found {
			result = existing
			return nil
		}
		r, e := q.GetRoom(ctx, room)
		if e != nil {
			return missing(e, "The room")
		}
		resource, e := q.GetResource(ctx, r.ResourceID)
		if e != nil {
			return e
		}
		if in.ThreadRootID != "" {
			root, e := q.GetMessage(ctx, in.ThreadRootID)
			if e != nil || root.RoomID != room || root.ThreadRootID.Valid {
				return problem("invalid_message", "The thread root must be a top-level message in this room.")
			}
		}
		recipients := in.RecipientAgentIDs
		if !in.PostOnly {
			members, err := q.ListMembers(ctx, room)
			if err != nil {
				return err
			}
			for _, member := range members {
				if mentionsHandle(in.Content, member.Handle) {
					recipients = append(recipients, member.ID)
				}
			}
			slices.Sort(recipients)
			recipients = slices.Compact(recipients)
		}
		agents := []Agent{}
		if !in.PostOnly {
			for _, agentID := range recipients {
				a, e := q.GetAgent(ctx, agentID)
				if e != nil || !a.Enabled {
					return problem("invalid_agent", "A recipient does not exist or is disabled.")
				}
				if _, e = q.GetMembership(ctx, sqlgen.GetMembershipParams{RoomID: room, AgentID: agentID}); e != nil {
					return problem("invalid_agent", "Every recipient must be a member of this room.")
				}
				agents = append(agents, agentRow(a))
			}
		}
		references, e := snapshotReferences(ctx, q, room, in.References, len(agents) > 0)
		if e != nil {
			return e
		}
		targets := make(map[string]Runner, len(agents))
		for _, agent := range agents {
			if err := enabledOwner(ctx, q, agent.OwnerUserID); err != nil {
				return err
			}
			if !b.RequireRunner && !r.RunnerID.Valid && agent.PreferredRunnerID == "" {
				continue
			}
			if b.RequireRunner && agent.OwnerUserID == "" && agent.PreferredRunnerID == "" && r.RunnerID.Valid && (in.RunnerRevision == nil || *in.RunnerRevision != r.RunnerRevision) {
				return problem("runner_revision_conflict", "The Room execution fallback changed. Refresh before assigning work.")
			}
			runner, err := resolveAgentRunner(ctx, q, r, agent)
			if err != nil {
				return err
			}
			targets[agent.ID] = runner
		}
		seq, e := q.NextMessageSequence(ctx, room)
		if e != nil {
			return e
		}
		stamp := now()
		m := Message{AuthorUserID: actor.ID, AuthorName: actor.Name, ID: newID(), RoomID: room, ThreadRootID: in.ThreadRootID, Seq: seq, AuthorKind: "user", Content: in.Content, RequestID: in.RequestID, CreatedAt: stamp, UpdatedAt: stamp, References: references}
		if e := insertMessage(ctx, q, m, f); e != nil {
			return e
		}
		result = SendResult{Message: m, Runs: []Run{}}
		for _, agent := range agents {
			run := Run{RequestedByUserID: actor.ID, Model: b.Model, RuntimeKey: b.RuntimeKey, ID: newID(), RoomID: room, ThreadRootID: in.ThreadRootID, OriginMessageID: m.ID, AgentID: agent.ID, AgentSnapshot: agent, ResourceID: r.ResourceID, Environment: json.RawMessage(resource.Environment), Prompt: in.Content, Status: "queued", CreatedAt: stamp, UpdatedAt: stamp, Files: []RunFile{}, Checks: json.RawMessage(`[]`), MaxTokens: b.MaxTokens, MaxSteps: b.MaxSteps, TimeoutSeconds: b.TimeoutSeconds, References: references}
			run.RunnerID, run.RunnerName = targets[agent.ID].ID, targets[agent.ID].Name
			snapshot, e := json.Marshal(run)
			if e != nil {
				return e
			}
			if e = q.CreateRun(ctx, sqlgen.CreateRunParams{RequestedByUserID: actor.ID, ID: run.ID, RoomID: room, ThreadRootID: nullableParent(run.ThreadRootID), OriginMessageID: m.ID, AgentID: agent.ID, ResourceID: r.ResourceID, Status: "queued", Snapshot: string(snapshot), CreatedAt: stamp, UpdatedAt: stamp}); e != nil {
				return e
			}
			result.Runs = append(result.Runs, run)
			if e := event(ctx, q, "run.queued", room, run.ID); e != nil {
				return e
			}
		}
		if err := notifyMentions(ctx, q, m); err != nil {
			return err
		}
		return event(ctx, q, "message.created", room, m.ID)
	})
	return result, err
}
func insertMessage(ctx context.Context, q *sqlgen.Queries, m Message, f string) error {
	if m.References == nil {
		m.References = []Reference{}
	}
	references, err := json.Marshal(m.References)
	if err != nil {
		return err
	}
	err = q.CreateMessage(ctx, sqlgen.CreateMessageParams{TaskID: m.TaskID, ReviewID: m.ReviewID, AuthorUserID: m.AuthorUserID, AuthorName: m.AuthorName, ID: m.ID, RoomID: m.RoomID, ThreadRootID: nullableParent(m.ThreadRootID), Seq: m.Seq, AuthorKind: m.AuthorKind, AgentID: nullableParent(m.AgentID), RunID: m.RunID, Content: m.Content, RequestID: m.RequestID, Fingerprint: f, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, ReferencesJson: string(references)})
	if err != nil {
		return err
	}
	return q.SetRoomMessageSequence(ctx, sqlgen.SetRoomMessageSequenceParams{LastMessageSeq: m.Seq, ID: m.RoomID})
}

// AddRunNotice records a context reset in the same conversation without creating
// deliveries. A retry cannot duplicate the notice for this run.
func (s *Store) AddRunNotice(ctx context.Context, runID, text string) error {
	if !validText(text, MaxRunErrorSize, false) {
		return problem("invalid_message", "The context notice is invalid.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := getRun(ctx, q, runID)
		if e != nil {
			return e
		}
		if _, e = q.GetRunNotice(ctx, runID); e == nil {
			return nil
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		seq, e := q.NextMessageSequence(ctx, r.RoomID)
		if e != nil {
			return e
		}
		m := Message{ID: newID(), RoomID: r.RoomID, ThreadRootID: r.ThreadRootID, Seq: seq, AuthorKind: "system", RunID: runID, Content: text, CreatedAt: now(), UpdatedAt: now()}
		if e := insertMessage(ctx, q, m, ""); e != nil {
			return e
		}
		return event(ctx, q, "message.created", r.RoomID, m.ID)
	})
}
