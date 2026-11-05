package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// createAutomationTask uses an explicitly delegated sponsor without attributing
// external text or the system's actions to that sponsor as an authored message.
func createAutomationTask(ctx context.Context, tx *sql.Tx, c orchestration.Candidate, a orchestration.Assessment, matters ...*orchestration.WorkItem) (CollaborationTask, error) {
	q := sqlgen.New(tx)
	p := c.Policy
	if (a.Outcome != "accept" && a.Outcome != "execute") || !p.Enabled || p.Mode != "auto" {
		return CollaborationTask{}, problem("invalid_task", "An automatic task requires an accepted automatic policy decision.")
	}
	in := CreateTaskInput{InputProtocol: 1, PagedResponse: true, Goal: strings.TrimSpace(a.Goal), ParticipantAgentIDs: p.ParticipantAgentIDs, FirstAgentID: p.FirstAgentID}
	if len(c.Events) == 0 {
		return CollaborationTask{}, problem("invalid_task", "An automatic task requires immutable source evidence.")
	}
	// Preserve sourceEventId as the legacy GitHub event resolver. Generic Room
	// and GitHub provenance has a separate namespaced source identity.
	in.SourceEventID = c.Events[0].ExternalEventID
	if c.Events[0].MessageID != "" {
		m, err := q.GetMessage(ctx, c.Events[0].MessageID)
		if err != nil || m.RoomID != p.RoomID {
			return CollaborationTask{}, problem("invalid_task", "The source message is outside the policy Room.")
		}
		in.ThreadRootID = m.ThreadRootID.String
		if in.ThreadRootID == "" {
			in.ThreadRootID = m.ID
		}
	}
	budget := Budget{RequireRunner: true, Model: c.Budget.Model, RuntimeKey: c.Budget.RuntimeKey, MaxTokens: c.Budget.MaxTokens, MaxSteps: c.Budget.MaxSteps, TimeoutSeconds: c.Budget.TimeoutSeconds}
	t, err := createTaskTx(ctx, q, p.RoomID, in, budget, taskCreation{RequesterUserID: p.OwnerUserID, OriginAuthorKind: "system", OriginAuthorName: "Automation", MaxTurns: p.MaxTurns, MaxModelRequests: p.MaxModelRequests})
	if err != nil {
		return t, err
	}
	t.AutomationPolicyID, t.AutomationPolicyRevision = p.ID, p.Revision
	t.AutomationSourceEventID = c.Events[0].ID
	t.AutomationScope = c.Events[0].ThreadKey
	for _, event := range c.Events {
		if event.RoomID != p.RoomID || (event.SourceID != p.SourceID && event.WorkItemID == "") {
			return t, problem("invalid_task", "The task evidence must belong to its policy source and Room.")
		}
		if _, err = appendAutomationTaskEvent(ctx, q, &t, event); err != nil {
			return t, err
		}
		if event.WorkItemID != "" {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO automation_subscriptions(id,task_id,source_id,thread_key,enabled,revision) VALUES(?,?,?,?,1,1) ON CONFLICT(task_id,source_id,thread_key) DO NOTHING`, newID(), t.ID, event.SourceID, event.ThreadKey); err != nil {
			return t, err
		}
	}
	if len(matters) > 0 {
		if err = attachWorkTask(ctx, q, matters[0], &t, t.MaxModelRequests); err != nil {
			return t, err
		}
	}
	if err = enqueueTaskRound(ctx, q, &t, p.FirstAgentID, in.Goal); err != nil {
		return t, err
	}
	if err = saveTask(ctx, q, &t); err != nil {
		return t, err
	}
	return t, recordAutomationTaskAdmission(ctx, q, t, c)
}

func taskReceivesEvents(t CollaborationTask) bool {
	switch t.Status {
	case "completed", "cancelled", "interrupted", "cancelling":
		return false
	default:
		return true
	}
}

// deliverAutomationEvent is called in the source event's transaction. An input
// delivered to several tasks has independent IDs and task-local sequence numbers.
func deliverAutomationEvent(ctx context.Context, tx *sql.Tx, event orchestration.SourceEvent) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.task_id FROM automation_subscriptions s JOIN collaboration_tasks t ON t.id=s.task_id WHERE s.enabled=1 AND s.source_id=? AND s.thread_key=? AND t.room_id=? ORDER BY s.task_id`, event.SourceID, event.ThreadKey, event.RoomID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	q := sqlgen.New(tx)
	for _, id := range ids {
		t, err := getTask(ctx, q, id)
		if err != nil {
			return err
		}
		if !taskReceivesEvents(t) {
			continue
		}
		if t.AutomationPolicyID != "" {
			policy, err := q.GetAutomationPolicyID(ctx, t.AutomationPolicyID)
			if err != nil {
				return err
			}
			if policy.Enabled == 0 && policy.SourceID == event.SourceID {
				continue
			}
		}
		changed, err := appendAutomationTaskEvent(ctx, q, &t, event)
		if err != nil {
			return err
		}
		if changed {
			if err = saveTask(ctx, q, &t); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendAutomationTaskEvent(ctx context.Context, q *sqlgen.Queries, t *CollaborationTask, event orchestration.SourceEvent) (bool, error) {
	if event.RoomID != t.RoomID || event.ID == "" || event.Content == "" {
		return false, problem("invalid_task", "The input requires source evidence from the task Room.")
	}
	remaining, changed := event.Content, false
	for part := int64(1); len(remaining) > 0; part++ {
		content := truncateUTF8(remaining, MaxRunPromptSize)
		remaining = remaining[len(content):]
		_, err := q.GetTaskInputDelivery(ctx, sqlgen.GetTaskInputDeliveryParams{TaskID: t.ID, SourceEventID: event.ID, PartIndex: part})
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		input := TaskInput{Seq: t.InputRevision + 1, ID: newID(), SourceEventID: event.ID, PartIndex: int(part), AuthorKind: "external", ExternalActorID: event.ActorID, AuthorName: event.ActorLogin, Content: content, CreatedAt: event.OccurredAt}
		if builtinWorkSource(event.SourceID) {
			input.AuthorKind = event.ActorKind
			input.ExternalActorID = ""
			if event.ActorKind == "user" {
				input.AuthorUserID = event.ActorID
			}
		}
		if input.CreatedAt == "" {
			input.CreatedAt = now()
		}
		if input.AuthorName == "" {
			input.AuthorName = event.ActorID
		}
		if event.SourceID == "room" {
			m, err := q.GetMessage(ctx, event.MessageID)
			if err != nil || m.RoomID != t.RoomID || part != 1 || remaining != "" || m.Content != content {
				return false, problem("invalid_task", "The subscribed message must match its immutable original.")
			}
			input.MessageID, input.AuthorKind, input.AuthorUserID, input.AuthorName, input.ExternalActorID = m.ID, m.AuthorKind, m.AuthorUserID, m.AuthorName, ""
		} else {
			seq, err := q.NextMessageSequence(ctx, t.RoomID)
			if err != nil {
				return false, err
			}
			input.MessageID = newID()
			name := "External: " + input.AuthorName
			if builtinWorkSource(event.SourceID) {
				name = "Camp: " + input.AuthorName
			}
			m := Message{ID: input.MessageID, TaskID: t.ID, RoomID: t.RoomID, ThreadRootID: t.ThreadRootID, Seq: seq, AuthorKind: "system", AuthorName: name, Content: content, CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt}
			if err = insertMessage(ctx, q, m, ""); err != nil {
				return false, err
			}
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return false, err
		}
		if err = q.InsertTaskInput(ctx, sqlgen.InsertTaskInputParams{ID: input.ID, TaskID: t.ID, MessageID: input.MessageID, SourceEventID: event.ID, PartIndex: part, Seq: input.Seq, ContentBytes: int64(len(content)), Value: string(raw)}); err != nil {
			return false, err
		}
		t.InputRevision, t.InputBytes = input.Seq, t.InputBytes+int64(len(content))
		changed = true
	}
	return changed, nil
}

// answerAutomationEvent recognizes a command only within an already authorized
// subscription. It does not evaluate text or turn an external actor into a user.
func answerAutomationEvent(ctx context.Context, tx *sql.Tx, event orchestration.SourceEvent, policy orchestration.Policy) (bool, error) {
	fields := strings.Fields(event.Content)
	if len(fields) == 0 || fields[0] != "/camp" || len(fields) < 2 || fields[1] != "answer" {
		return false, nil
	}
	if !policy.Enabled || policy.Mode != "auto" || !policy.AllowAnswers || event.ActorKind != "user" || event.RoomID != policy.RoomID || event.SourceID != policy.SourceID || event.Kind != "issue_comment.created" || !slices.Contains(policy.AllowedActorIDs, event.ActorID) || len(fields) < 4 {
		return false, nil
	}
	questionID := fields[2]
	answer := strings.TrimSpace(event.Content)
	for range 3 {
		index := strings.IndexFunc(answer, unicode.IsSpace)
		if index < 0 {
			return false, nil
		}
		answer = strings.TrimLeftFunc(answer[index:], unicode.IsSpace)
	}
	if !validText(answer, MaxRunPromptSize, false) {
		return false, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.snapshot FROM automation_subscriptions s JOIN collaboration_tasks t ON t.id=s.task_id WHERE s.enabled=1 AND s.source_id=? AND s.thread_key=? AND t.room_id=? AND t.status='waiting_for_person'`, event.SourceID, event.ThreadKey, event.RoomID)
	if err != nil {
		return true, err
	}
	var target *CollaborationTask
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return true, err
		}
		t, e := taskSnapshot(raw)
		if e != nil {
			rows.Close()
			return true, e
		}
		if t.Question != nil && t.Question.ID == questionID && t.Question.Answer == "" {
			target = &t
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil || target == nil {
		return false, err
	}
	q := sqlgen.New(tx)
	owner, ownerErr := q.GetUser(ctx, policy.OwnerUserID)
	if ownerErr != nil || !owner.Enabled || automationSourceAvailable(ctx, q, policy) != nil {
		return false, nil
	}
	if err = authorizeTask(ctx, q, *target); err != nil {
		return false, nil
	}
	var sent int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM automation_deliveries WHERE task_id=? AND source_id=? AND status='sent' AND json_extract(value,'$.questionId')=? AND CAST(json_extract(value,'$.repositoryId') AS TEXT)||':'||CAST(json_extract(value,'$.number') AS TEXT)=?`, target.ID, event.SourceID, questionID, event.ThreadKey).Scan(&sent); err != nil {
		return false, err
	}
	if sent == 0 {
		return false, nil
	}
	if _, err = appendAutomationTaskEvent(ctx, q, target, event); err != nil {
		return true, err
	}
	target.Question.Answer, target.Question.AnswerSourceEventID, target.Question.AnsweredByExternalActorID = answer, event.ID, event.ActorID
	if err = enqueueTaskRound(ctx, q, target, target.CurrentAgentID, "An authorized external participant answered the question. Process their answer and continue the original goal."); err != nil {
		return true, err
	}
	return true, saveTask(ctx, q, target)
}

func (s *Store) TaskSubscriptions(ctx context.Context, id string) (out []orchestration.Subscription, err error) {
	out = []orchestration.Subscription{}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if _, err := getTask(ctx, q, id); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,task_id,source_id,thread_key,enabled,revision FROM automation_subscriptions WHERE task_id=? ORDER BY source_id,thread_key`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item orchestration.Subscription
			if err = rows.Scan(&item.ID, &item.TaskID, &item.SourceID, &item.ThreadKey, &item.Enabled, &item.Revision); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) SetTaskSubscription(ctx context.Context, id string, in orchestration.SubscriptionInput) (out orchestration.Subscription, err error) {
	if !validText(in.RequestID, 128, false) || !validText(in.SourceID, 128, false) || !validText(in.ThreadKey, 512, false) {
		return out, problem("invalid_subscription", "Provide a stable request ID and source Thread.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		old, replay, hash, e := automationReceipt(ctx, q, in.RequestID, []any{"subscription", id, in})
		if e != nil {
			return e
		}
		if replay {
			out = *old.Subscription
			return nil
		}
		t, e := getTask(ctx, q, id)
		if e != nil {
			return e
		}
		if e = taskManage(ctx, q, t); e != nil {
			return e
		}
		if in.Enabled && !taskReceivesEvents(t) {
			return problem("task_finished", "A finished collaboration cannot receive new subscribed inputs.")
		}
		if e = validateTaskSubscription(ctx, tx, t, in); e != nil {
			return e
		}
		e = tx.QueryRowContext(ctx, `SELECT id,task_id,source_id,thread_key,enabled,revision FROM automation_subscriptions WHERE task_id=? AND source_id=? AND thread_key=?`, id, in.SourceID, in.ThreadKey).Scan(&out.ID, &out.TaskID, &out.SourceID, &out.ThreadKey, &out.Enabled, &out.Revision)
		if errors.Is(e, sql.ErrNoRows) {
			if in.Revision != 0 {
				return problem("revision_conflict", "The subscription changed. Refresh before saving.")
			}
			existing, err := q.ListAutomationTaskSubscriptions(ctx, t.ID)
			if err != nil {
				return err
			}
			if len(existing) >= 32 {
				return problem("invalid_subscription", "A collaboration supports at most 32 followed conversations.")
			}
			out = orchestration.Subscription{ID: newID(), TaskID: id, SourceID: in.SourceID, ThreadKey: in.ThreadKey}
		} else if e != nil {
			return e
		} else if in.Revision != out.Revision {
			return problem("revision_conflict", "The subscription changed. Refresh before saving.")
		}
		out.Enabled, out.Revision = in.Enabled, out.Revision+1
		if _, e = tx.ExecContext(ctx, `INSERT INTO automation_subscriptions(id,task_id,source_id,thread_key,enabled,revision) VALUES(?,?,?,?,?,?) ON CONFLICT(task_id,source_id,thread_key) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision`, out.ID, id, out.SourceID, out.ThreadKey, out.Enabled, out.Revision); e != nil {
			return e
		}
		if in.Enabled && t.InputProtocol != 1 {
			t.InputProtocol = 1
		}
		if e = saveTask(ctx, q, &t); e != nil {
			return e
		}
		return saveAutomationReceipt(ctx, q, in.RequestID, hash, AutomationReceipt{Kind: "subscription", Subscription: &out})
	})
	return out, err
}

func validateTaskSubscription(ctx context.Context, tx *sql.Tx, t CollaborationTask, in orchestration.SubscriptionInput) error {
	if in.SourceID == "room" {
		prefix := t.RoomID + ":"
		if !strings.HasPrefix(in.ThreadKey, prefix) {
			return problem("invalid_subscription", "Choose a Thread in this collaboration's Room.")
		}
		root := strings.TrimPrefix(in.ThreadKey, prefix)
		if root != "room" {
			m, err := sqlgen.New(tx).GetMessage(ctx, root)
			if err != nil || m.RoomID != t.RoomID || m.ThreadRootID.Valid {
				return problem("invalid_subscription", "Choose a top-level message in this Room.")
			}
		}
		return nil
	}
	if _, err := automationSourceKind(ctx, sqlgen.New(tx), t.RoomID, in.SourceID, in.Enabled); err != nil {
		return problem("invalid_subscription", "Choose an available event source in this Room.")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM automation_events WHERE room_id=? AND source_id=? AND thread_key=?`, t.RoomID, in.SourceID, in.ThreadKey).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return problem("invalid_subscription", fmt.Sprintf("The source Thread %q has not been observed in this Room.", in.ThreadKey))
	}
	return nil
}
