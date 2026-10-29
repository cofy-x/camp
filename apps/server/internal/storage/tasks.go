package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func taskSnapshot(raw string) (CollaborationTask, error) {
	var t CollaborationTask
	err := json.Unmarshal([]byte(raw), &t)
	return t, err
}
func getTask(ctx context.Context, q *sqlgen.Queries, id string) (CollaborationTask, error) {
	raw, err := q.GetTask(ctx, id)
	if err != nil {
		return CollaborationTask{}, missing(err, "The collaboration")
	}
	return taskSnapshot(raw)
}
func (s *Store) GetTask(ctx context.Context, id string) (CollaborationTask, error) {
	return getTask(ctx, s.queries(), id)
}
func taskRows(rows []string, err error) ([]CollaborationTask, error) {
	out := make([]CollaborationTask, 0, len(rows))
	if err != nil {
		return out, err
	}
	for _, raw := range rows {
		t, e := taskSnapshot(raw)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, nil
}
func (s *Store) ListRoomTasks(ctx context.Context, room string) ([]CollaborationTask, error) {
	if _, err := s.GetRoom(ctx, room); err != nil {
		return nil, err
	}
	return taskRows(s.queries().ListRoomTasks(ctx, room))
}
func (s *Store) ListOpenTasks(ctx context.Context) ([]CollaborationTask, error) {
	return taskRows(s.queries().ListOpenTasks(ctx))
}
func (s *Store) ListTaskRuns(ctx context.Context, id string) ([]Run, error) {
	if _, err := s.GetTask(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.queries().ListTaskRuns(ctx, id)
	if err != nil {
		return nil, err
	}
	return runsFromSnapshots(rows)
}
func saveTask(ctx context.Context, q *sqlgen.Queries, t *CollaborationTask) error {
	t.Revision++
	t.UpdatedAt = now()
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err = q.SaveTask(ctx, sqlgen.SaveTaskParams{ID: t.ID, Status: t.Status, Revision: t.Revision, Snapshot: string(raw), UpdatedAt: t.UpdatedAt}); err != nil {
		return err
	}
	return event(ctx, q, "task.updated", t.RoomID, t.ID)
}
func taskReceipt(ctx context.Context, q *sqlgen.Queries, scope, request string, in any) (CollaborationTask, bool, string, error) {
	if !validText(request, 128, false) {
		return CollaborationTask{}, false, "", problem("invalid_task", "A stable request ID is required.")
	}
	raw, _ := json.Marshal(in)
	hash := remoteHash(raw)
	old, err := q.GetTaskRequest(ctx, sqlgen.GetTaskRequestParams{UserID: actorID(ctx), Scope: scope, RequestID: request})
	if errors.Is(err, sql.ErrNoRows) {
		return CollaborationTask{}, false, hash, nil
	}
	if err != nil {
		return CollaborationTask{}, false, hash, err
	}
	if old.RequestHash != hash {
		return CollaborationTask{}, false, hash, problem("request_conflict", "This request ID belongs to different task content.")
	}
	t, err := taskSnapshot(old.Result)
	return t, true, hash, err
}
func saveTaskReceipt(ctx context.Context, q *sqlgen.Queries, scope, request, hash string, t CollaborationTask) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return q.SaveTaskRequest(ctx, sqlgen.SaveTaskRequestParams{UserID: actorID(ctx), Scope: scope, RequestID: request, RequestHash: hash, Result: string(raw)})
}
func taskParticipants(ctx context.Context, q *sqlgen.Queries, room sqlgen.Room, ids []string) ([]TaskParticipant, error) {
	if len(ids) < 1 || len(ids) > 8 {
		return nil, problem("invalid_task", "Choose between one and eight room agents.")
	}
	out := make([]TaskParticipant, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, problem("invalid_task", "Each participant must be unique.")
		}
		seen[id] = true
		a, err := q.GetAgent(ctx, id)
		if err != nil || !a.Enabled {
			return nil, problem("agent_unavailable", "A collaboration participant is unavailable.")
		}
		if _, err = q.GetMembership(ctx, sqlgen.GetMembershipParams{RoomID: room.ID, AgentID: id}); err != nil {
			return nil, problem("agent_unavailable", "Share each participating agent into this room first.")
		}
		agent := agentRow(a)
		runner, err := resolveAgentRunner(ctx, q, room, agent)
		if err != nil {
			return nil, err
		}
		out = append(out, TaskParticipant{Agent: agent, RunnerID: runner.ID, RunnerName: runner.Name})
	}
	return out, nil
}
func taskParticipant(t CollaborationTask, id string) (TaskParticipant, bool) {
	for _, p := range t.Participants {
		if p.Agent.ID == id {
			return p, true
		}
	}
	return TaskParticipant{}, false
}
func (s *Store) CreateTask(ctx context.Context, roomID string, in CreateTaskInput, b Budget) (out CollaborationTask, err error) {
	in.Goal = strings.TrimSpace(in.Goal)
	if !validText(in.Goal, MaxRunPromptSize, false) {
		return out, problem("invalid_task", "Provide a collaboration goal of at most 8192 bytes.")
	}
	if b.MaxTokens < 1 || b.MaxSteps < 1 || b.TimeoutSeconds < 1 {
		return out, problem("invalid_task", "Configure a valid execution budget.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		scope := "create:" + roomID
		old, replay, hash, e := taskReceipt(ctx, q, scope, in.RequestID, in)
		if e != nil {
			return e
		}
		if replay {
			out = old
			return nil
		}
		room, e := q.GetRoom(ctx, roomID)
		if e != nil {
			return missing(e, "The room")
		}
		resource, e := q.GetResource(ctx, room.ResourceID)
		if e != nil {
			return e
		}
		participants, e := taskParticipants(ctx, q, room, in.ParticipantAgentIDs)
		if e != nil {
			return e
		}
		refs, e := snapshotReferences(ctx, q, roomID, in.References, true)
		if e != nil {
			return e
		}
		if in.ThreadRootID != "" {
			m, e := q.GetMessage(ctx, in.ThreadRootID)
			if e != nil || m.RoomID != roomID || m.ThreadRootID.Valid {
				return problem("invalid_task", "Choose a top-level message in this room.")
			}
		}
		stamp := now()
		user, _ := UserFromContext(ctx)
		out = CollaborationTask{ID: newID(), RoomID: roomID, ThreadRootID: in.ThreadRootID, ResourceID: resource.ID, RequestedByUserID: actorID(ctx), Goal: in.Goal, Status: "queued", Revision: 1, Participants: participants, CurrentAgentID: in.FirstAgentID, BaseSharedCommit: resource.CurrentCommit, CheckpointCommit: resource.CurrentCommit, MaxTurns: 6, MaxModelRequests: 24, Inputs: []TaskInput{}, Environment: json.RawMessage(resource.Environment), Budget: b, References: refs, CreatedAt: stamp, UpdatedAt: stamp}
		if _, ok := taskParticipant(out, in.FirstAgentID); !ok {
			return problem("invalid_task", "Choose the first agent from the approved participants.")
		}
		seq, e := q.NextMessageSequence(ctx, roomID)
		if e != nil {
			return e
		}
		m := Message{ID: newID(), TaskID: out.ID, RoomID: roomID, ThreadRootID: in.ThreadRootID, Seq: seq, AuthorKind: "user", AuthorUserID: user.ID, AuthorName: user.Name, Content: in.Goal, References: refs, CreatedAt: stamp, UpdatedAt: stamp}
		out.OriginMessageID = m.ID
		if out.ThreadRootID == "" {
			out.ThreadRootID = m.ID
		}
		if e = insertMessage(ctx, q, m, ""); e != nil {
			return e
		}
		raw, _ := json.Marshal(out)
		if e = q.CreateTask(ctx, sqlgen.CreateTaskParams{ID: out.ID, RoomID: roomID, RequestedByUserID: out.RequestedByUserID, Status: out.Status, Revision: out.Revision, Snapshot: string(raw), CreatedAt: stamp, UpdatedAt: stamp}); e != nil {
			return e
		}
		if e = enqueueTaskRound(ctx, q, &out, in.FirstAgentID, in.Goal); e != nil {
			return e
		}
		if e = saveTask(ctx, q, &out); e != nil {
			return e
		}
		return saveTaskReceipt(ctx, q, scope, in.RequestID, hash, out)
	})
	return
}

// Queue creation does not consume a round or hold a writer lease. The fixed
// participant and routing snapshot is reauthorized when execution begins.
func enqueueTaskRound(ctx context.Context, q *sqlgen.Queries, t *CollaborationTask, agentID, instruction string) error {
	p, ok := taskParticipant(*t, agentID)
	if !ok {
		return problem("invalid_task", "The next agent is not an approved participant.")
	}
	// Keep the approved next step even when it must wait for more budget.
	t.CurrentAgentID = agentID
	t.Handoff = instruction
	if t.TurnsUsed >= t.MaxTurns || t.ModelRequestsUsed >= t.MaxModelRequests {
		t.Status = "needs_attention"
		t.Error = "The approved collaboration budget is exhausted. Add a finite allowance to continue."
		t.ActiveRunID = ""
		return nil
	}
	origin := t.OriginMessageID
	if t.TurnsUsed > 0 {
		seq, e := q.NextMessageSequence(ctx, t.RoomID)
		if e != nil {
			return e
		}
		m := Message{ID: newID(), TaskID: t.ID, RoomID: t.RoomID, ThreadRootID: t.ThreadRootID, Seq: seq, AuthorKind: "system", Content: "Next: " + p.Agent.Name + ". " + instruction, CreatedAt: now(), UpdatedAt: now()}
		if e = insertMessage(ctx, q, m, ""); e != nil {
			return e
		}
		origin = m.ID
	}
	b := t.Budget
	r := Run{TaskID: t.ID, InputCommit: t.CheckpointCommit, TaskInputRevision: int64(len(t.Inputs)), RequestedByUserID: t.RequestedByUserID, RunnerID: p.RunnerID, RunnerName: p.RunnerName, ID: newID(), RoomID: t.RoomID, ThreadRootID: t.ThreadRootID, OriginMessageID: origin, AgentID: agentID, AgentSnapshot: p.Agent, ResourceID: t.ResourceID, Environment: t.Environment, Prompt: truncateUTF8(instruction, MaxRunPromptSize), Status: "queued", CreatedAt: now(), UpdatedAt: now(), Model: b.Model, RuntimeKey: b.RuntimeKey, MaxTokens: b.MaxTokens, MaxSteps: b.MaxSteps, TimeoutSeconds: b.TimeoutSeconds, References: t.References, Files: []RunFile{}, Checks: json.RawMessage(`[]`)}
	if err := authorizeRunExecution(ctx, q, r); err != nil {
		return err
	}
	raw, _ := json.Marshal(r)
	if err := q.CreateRun(ctx, sqlgen.CreateRunParams{ID: r.ID, RoomID: r.RoomID, ThreadRootID: nullableParent(r.ThreadRootID), OriginMessageID: r.OriginMessageID, AgentID: r.AgentID, ResourceID: r.ResourceID, Status: r.Status, Snapshot: string(raw), RequestedByUserID: r.RequestedByUserID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}); err != nil {
		return err
	}
	t.ActiveRunID = r.ID
	t.ActiveAttemptID = ""
	t.CurrentAgentID = agentID
	t.Status = "queued"
	t.Handoff = instruction
	t.Error = ""
	return event(ctx, q, "run.queued", r.RoomID, r.ID)
}
func taskManage(ctx context.Context, q *sqlgen.Queries, t CollaborationTask) error {
	u, err := q.GetUser(ctx, actorID(ctx))
	if err != nil || !u.Enabled || (u.ID != t.RequestedByUserID && u.Role != "admin") {
		return problem("forbidden", "Only the collaboration requester or an administrator can manage this task.")
	}
	return nil
}
func (s *Store) OperateTask(ctx context.Context, id, action string, in TaskOperation) (out CollaborationTask, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		scope := action + ":" + id
		old, replay, hash, e := taskReceipt(ctx, q, scope, in.RequestID, in)
		if e != nil {
			return e
		}
		if replay {
			out = old
			return nil
		}
		out, e = getTask(ctx, q, id)
		if e != nil {
			return e
		}
		if out.Status == "completed" || out.Status == "cancelled" || out.Status == "interrupted" {
			return problem("task_finished", "This collaboration is already finished. Start a new collaboration to continue.")
		}
		if action != "inputs" && in.Revision != out.Revision {
			return problem("revision_conflict", "This collaboration changed. Refresh before continuing.")
		}
		switch action {
		case "inputs":
			if e = addTaskInput(ctx, q, &out, in.Content); e != nil {
				return e
			}
		case "answers":
			if out.Status != "waiting_for_person" || out.Question == nil || out.Question.ID != in.QuestionID || out.Question.Answer != "" {
				return problem("question_answered", "This question is no longer waiting for an answer.")
			}
			if e = addTaskInput(ctx, q, &out, in.Content); e != nil {
				return e
			}
			out.Question.Answer = strings.TrimSpace(in.Content)
			out.Question.AnsweredByUserID = actorID(ctx)
			if e = enqueueTaskRound(ctx, q, &out, out.CurrentAgentID, "A team member answered the question. Process their answer and continue the original goal."); e != nil {
				return e
			}
		case "continue":
			if e = taskManage(ctx, q, out); e != nil {
				return e
			}
			if out.ActiveRunID != "" || out.Status == "publishing" {
				return problem("task_busy", "Wait for the current execution to stop before continuing.")
			}
			if in.AddTurns < 0 || in.AddTurns > 24 || in.AddModelRequests < 0 || in.AddModelRequests > 96 {
				return problem("invalid_task", "Add at most 24 turns and 96 model requests per explicit authorization.")
			}
			out.MaxTurns += in.AddTurns
			out.MaxModelRequests += in.AddModelRequests
			if len(in.ParticipantAgentIDs) > 0 {
				room, e := q.GetRoom(ctx, out.RoomID)
				if e != nil {
					return e
				}
				out.Participants, e = taskParticipants(ctx, q, room, in.ParticipantAgentIDs)
				if e != nil {
					return e
				}
			}
			if in.FromLatest {
				resource, e := q.GetResource(ctx, out.ResourceID)
				if e != nil {
					return e
				}
				out.PreviousDraftCommit = out.CheckpointCommit
				out.BaseSharedCommit = resource.CurrentCommit
				out.CheckpointCommit = resource.CurrentCommit
				out.CheckpointRevision++
				out.PublicationRunID = ""
			}
			if strings.TrimSpace(in.Content) != "" {
				if e = addTaskInput(ctx, q, &out, in.Content); e != nil {
					return e
				}
			}
			target := in.AgentID
			if target == "" {
				target = out.CurrentAgentID
			}
			instruction := "Continue the original goal using the latest task inputs."
			if target == out.CurrentAgentID && out.Handoff != "" {
				instruction = out.Handoff
			}
			if e = enqueueTaskRound(ctx, q, &out, target, instruction); e != nil {
				return e
			}
		case "cancel":
			if e = taskManage(ctx, q, out); e != nil {
				return e
			}
			out.Status = "cancelling"
			out.Error = "Cancellation requested."
			if e = saveTask(ctx, q, &out); e != nil {
				return e
			}
			if out.ActiveRunID != "" {
				r, e := getRun(ctx, q, out.ActiveRunID)
				if e != nil {
					return e
				}
				if _, e = cancelRemoteRun(ctx, q, r, "Collaboration cancelled by its requester or an administrator."); e != nil {
					return e
				}
				out, e = getTask(ctx, q, id)
				if e != nil {
					return e
				}
			} else {
				out.Status = "cancelled"
			}
		default:
			return problem("invalid_task", "Unknown collaboration operation.")
		}
		if e = saveTask(ctx, q, &out); e != nil {
			return e
		}
		return saveTaskReceipt(ctx, q, scope, in.RequestID, hash, out)
	})
	return
}
func addTaskInput(ctx context.Context, q *sqlgen.Queries, t *CollaborationTask, content string) error {
	content = strings.TrimSpace(content)
	total := len(content)
	for _, i := range t.Inputs {
		total += len(i.Content)
	}
	if !validText(content, MaxRunPromptSize, false) || len(t.Inputs) >= 128 || total > 64<<10 {
		return problem("invalid_task", "Task input must fit the 8192-byte message and 64 KiB task context limits.")
	}
	u, e := q.GetUser(ctx, actorID(ctx))
	if e != nil {
		return e
	}
	input := TaskInput{ID: newID(), AuthorUserID: u.ID, AuthorName: u.Name, Content: content, CreatedAt: now()}
	t.Inputs = append(t.Inputs, input)
	seq, e := q.NextMessageSequence(ctx, t.RoomID)
	if e != nil {
		return e
	}
	m := Message{ID: input.ID, TaskID: t.ID, RoomID: t.RoomID, ThreadRootID: t.ThreadRootID, Seq: seq, AuthorKind: "user", AuthorUserID: u.ID, AuthorName: u.Name, Content: content, CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt}
	return insertMessage(ctx, q, m, "")
}
func taskNotice(ctx context.Context, q *sqlgen.Queries, t CollaborationTask, kind, text string) error {
	seq, e := q.NextMessageSequence(ctx, t.RoomID)
	if e != nil {
		return e
	}
	m := Message{ID: newID(), TaskID: t.ID, RoomID: t.RoomID, ThreadRootID: t.ThreadRootID, Seq: seq, AuthorKind: "system", Content: text, CreatedAt: now(), UpdatedAt: now()}
	if e = insertMessage(ctx, q, m, ""); e != nil {
		return e
	}
	return q.CreateInboxItem(ctx, sqlgen.CreateInboxItemParams{ID: newID(), UserID: t.RequestedByUserID, Kind: kind, RoomID: t.RoomID, MessageID: m.ID, TaskID: t.ID, CreatedAt: now()})
}

// TaskBrief never truncates the original goal or explicit task inputs. General
// room history is separately bounded by the existing context builder.
func (t CollaborationTask) TaskBrief(r Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Camp collaboration %s\nOriginal goal:\n%s\n\nCurrent handoff:\n%s\n\nApproved participants:\n", t.ID, t.Goal, r.Prompt)
	for _, p := range t.Participants {
		fmt.Fprintf(&b, "- %s (%s), %s access\n", p.Agent.Name, p.Agent.ID, p.Agent.Access)
	}
	b.WriteString("\nTask inputs to process this round:\n")
	end := min(int(r.TaskInputRevision), len(t.Inputs))
	for _, i := range t.Inputs[:end] {
		fmt.Fprintf(&b, "%s: %s\n", i.AuthorName, i.Content)
	}
	b.WriteString("\nYou must end this round with exactly one Camp coordination tool: camp_handoff, camp_ask_person, or camp_finish. Ordinary @mentions do not dispatch work. Working files are a task draft until final publication. A read-only reviewer must hand changed drafts back to a writer for final checks.\n")
	return b.String()
}
func validateIntent(t CollaborationTask, r Run, i *CoordinationIntent) error {
	if i == nil || !validText(i.CallID, 128, false) || len(i.ArtifactPaths) > 128 {
		return problem("invalid_intent", "A successful collaboration round requires one durable coordination intent.")
	}
	for _, p := range i.ArtifactPaths {
		if !validTaskPath(p) {
			return problem("invalid_intent", "Artifact paths must be relative workspace files.")
		}
	}
	switch i.Kind {
	case "handoff":
		if _, ok := taskParticipant(t, i.TargetAgentID); !ok || !validText(i.Instruction, MaxRunPromptSize, false) {
			return problem("invalid_intent", "Handoff requires an approved participant and an instruction.")
		}
	case "ask_person":
		if !validText(i.Question, MaxRunPromptSize, false) || len(i.Options) > 8 {
			return problem("invalid_intent", "A question must be nonempty with at most eight options.")
		}
		for _, option := range i.Options {
			if !validText(option, 512, false) {
				return problem("invalid_intent", "Question options must be short nonempty text.")
			}
		}
	case "finish":
		if !validText(i.Summary, 32<<10, false) {
			return problem("invalid_intent", "A finish intent requires a result summary.")
		}
		if r.AgentSnapshot.Access != "write" && t.CheckpointCommit != t.BaseSharedCommit {
			return problem("invalid_intent", "A writer must run final checks before publishing this draft.")
		}
	default:
		return problem("invalid_intent", "Unknown collaboration intent.")
	}
	return nil
}
func validTaskPath(p string) bool {
	return validText(p, 4096, false) && p != "." && !strings.HasPrefix(p, "/") && !strings.ContainsAny(p, "\\\x00") && !slices.Contains(strings.Split(p, "/"), "..") && !strings.Contains(p, "//")
}

func (s *Store) FindTaskCreation(ctx context.Context, room string, in CreateTaskInput) (CollaborationTask, bool, error) {
	in.Goal = strings.TrimSpace(in.Goal)
	t, found, _, err := taskReceipt(ctx, s.queries(), "create:"+room, in.RequestID, in)
	return t, found, err
}

func (s *Store) FirstRunnerSuccess(ctx context.Context, id string) (runID, stamp string, err error) {
	row, err := s.queries().FirstRunnerSuccess(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return row.ID, row.UpdatedAt, err
}

// Older receipts remain useful evidence of a successful execution, but they do
// not establish which executor was used. Never infer that from today's Runner.
func (s *Store) FirstRunnerUnrecordedSuccess(ctx context.Context, id string) (runID, stamp string, err error) {
	row, err := s.queries().FirstRunnerUnrecordedSuccess(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return row.ID, row.UpdatedAt, err
}
