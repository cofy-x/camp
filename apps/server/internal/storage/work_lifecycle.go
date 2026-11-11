package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const workAuthorizationLifetime = 30 * 24 * time.Hour

func normalizeWorkItem(w *orchestration.WorkItem) {
	if w.Question == "" {
		w.QuestionKind = ""
	} else if w.QuestionKind == "" {
		w.QuestionKind = orchestration.WorkQuestionJudgment
	}
	if w.ExpiresAt == "" {
		w.ExpiresAt = w.Deadline
	}
	if w.Deadline == "" {
		w.Deadline = w.ExpiresAt
	}
	if w.StopRequested && w.Status != "cancelled" {
		w.Status = "stopping"
	}
	if w.CompletionMode == "" {
		w.CompletionMode = "criteria"
		if strings.TrimSpace(w.CompletionCriteria) == "" {
			w.CompletionMode = "human"
		}
	}
}

func workDueAt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return value, nil
	}
	return workTime(value)
}

func (s *Store) WorkItemDefaults(ctx context.Context, room, policyID string) (out orchestration.WorkItemDefaults, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		r, e := q.GetRoom(ctx, room)
		if e != nil {
			return missing(e, "The Room")
		}
		out = orchestration.WorkItemDefaults{Budget: defaultWorkBudget(orchestration.WorkBudget{}), ExpiresAt: time.Now().Add(workAuthorizationLifetime).UTC().Format(time.RFC3339Nano), ParticipantAgentIDs: []string{}, Execution: orchestration.WorkExecutionAllowance{MaxTurns: 6, MaxModelRequests: 24}}
		if policyID != "" {
			row, e := q.GetAutomationPolicyID(ctx, policyID)
			if e != nil {
				return missing(e, "The automation policy")
			}
			p, e := automationPolicy(row)
			if e != nil {
				return e
			}
			if p.RoomID != room || p.OwnerUserID != actorID(ctx) {
				return problem("forbidden", "Choose your own policy in this Room.")
			}
			out.Budget = defaultWorkBudget(p.ItemBudget)
			out.ParticipantAgentIDs = append([]string{}, p.ParticipantAgentIDs...)
			out.FirstAgentID = p.FirstAgentID
			out.Execution = orchestration.WorkExecutionAllowance{MaxTurns: 6, MaxModelRequests: min(24, out.Budget.MaxModelRequests)}
			return nil
		}
		members, e := q.ListMembers(ctx, room)
		if e != nil {
			return e
		}
		for _, member := range members {
			if member.Enabled {
				if out.FirstAgentID == "" || member.ID == r.DefaultAgentID.String {
					out.FirstAgentID = member.ID
				}
			}
		}
		if out.FirstAgentID != "" {
			out.ParticipantAgentIDs = []string{out.FirstAgentID}
		}
		return nil
	})
	return
}

func ensureWorkSource(ctx context.Context, q *sqlgen.Queries, room, sourceID, messageID string) (string, error) {
	if messageID != "" {
		m, e := q.GetMessage(ctx, messageID)
		if e != nil || m.RoomID != room {
			return "", problem("invalid_work_item", "Choose an immutable Message from this Room.")
		}
		canonical := "room:" + m.ID
		if sourceID != "" && sourceID != canonical {
			return "", problem("invalid_work_item", "The Message and source event must identify the same original.")
		}
		sourceID = canonical
		_, e = q.GetAutomationEvent(ctx, canonical)
		if errors.Is(e, sql.ErrNoRows) {
			thread := m.ThreadRootID.String
			if thread == "" {
				thread = "room"
			}
			actorID := m.AuthorUserID
			if m.AuthorKind == "agent" {
				actorID = m.AgentID.String
			}
			if m.AuthorKind == "system" {
				actorID = "system"
			}
			ev := orchestration.SourceEvent{ID: canonical, RoomID: room, SourceID: "room", ThreadKey: room + ":" + thread, Kind: "room.message", ActorKind: m.AuthorKind, ActorID: actorID, ActorLogin: m.AuthorName, MessageID: m.ID, Content: m.Content, OccurredAt: m.CreatedAt, Status: "context"}
			raw, e := json.Marshal(ev)
			if e != nil {
				return "", e
			}
			if _, e = q.InsertAutomationEvent(ctx, sqlgen.InsertAutomationEventParams{ID: ev.ID, RoomID: room, SourceID: ev.SourceID, ThreadKey: ev.ThreadKey, Kind: ev.Kind, ActorID: ev.ActorID, ActorKind: ev.ActorKind, MessageID: ev.MessageID, Status: "context", Value: string(raw)}); e != nil {
				return "", e
			}
			if e = event(ctx, q, "automation.event.updated", room, ev.ID); e != nil {
				return "", e
			}
		} else if e != nil {
			return "", e
		}
	}
	if sourceID != "" {
		row, e := q.GetAutomationEvent(ctx, sourceID)
		if e != nil || row.RoomID != room {
			return "", problem("invalid_work_item", "Choose source evidence from this Room.")
		}
	}
	return sourceID, nil
}

func workObservedConversation(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem, source, thread string) (bool, error) {
	n, e := q.WorkObservedConversation(ctx, sqlgen.WorkObservedConversationParams{WorkItemID: w.ID, SourceID: source, ThreadKey: thread})
	if e != nil {
		return false, e
	}
	if n > 0 {
		return true, nil
	}
	if source == "room" {
		n, e = q.WorkObservedRoomThread(ctx, sqlgen.WorkObservedRoomThreadParams{WorkItemID: w.ID, RoomID: w.RoomID, ThreadKey: thread})
		return n > 0, e
	}
	return false, nil
}

func setWorkConversation(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem, source, thread string, enabled bool) error {
	if !validText(source, 128, false) || !validText(thread, 512, false) {
		return problem("invalid_work_item", "Choose an observed source conversation.")
	}
	observed, e := workObservedConversation(ctx, q, w, source, thread)
	if e != nil {
		return e
	}
	if !observed {
		return problem("invalid_work_item", "First associate evidence from this conversation.")
	}
	_, e = q.GetWorkWatch(ctx, sqlgen.GetWorkWatchParams{WorkItemID: w.ID, SourceID: source, ThreadKey: thread})
	exists := e == nil
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if !enabled && !exists {
		return problem("invalid_work_item", "This matter does not follow that conversation.")
	}
	watches, e := q.WorkWatches(ctx, w.ID)
	if e != nil {
		return e
	}
	if enabled && !exists && len(watches) >= 32 {
		return problem("invalid_work_item", "Follow at most 32 conversations.")
	}
	n := int64(0)
	if enabled {
		n = 1
	}
	if e = q.SaveWorkWatch(ctx, sqlgen.SaveWorkWatchParams{WorkItemID: w.ID, SourceID: source, ThreadKey: thread, Enabled: n}); e != nil {
		return e
	}
	return setWorkTaskWatch(ctx, q, w.ActiveTaskID, source, thread, enabled)
}

func bindWorkSource(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, sourceID, messageID string, follow bool) error {
	id, e := ensureWorkSource(ctx, q, w.RoomID, sourceID, messageID)
	if e != nil {
		return e
	}
	if id == "" {
		if follow {
			return problem("invalid_work_item", "Choose the source conversation to follow.")
		}
		return nil
	}
	if e = linkWorkEvidence(ctx, q, w, []string{id}); e != nil {
		return e
	}
	if !follow {
		return nil
	}
	ev, e := q.GetAutomationEvent(ctx, id)
	if e != nil {
		return e
	}
	source, thread := ev.SourceID, ev.ThreadKey
	if messageID != "" {
		m, e := q.GetMessage(ctx, messageID)
		if e != nil {
			return e
		}
		anchor := m.ThreadRootID.String
		if anchor == "" {
			anchor = m.ID
		}
		source, thread = "room", w.RoomID+":"+anchor
	}
	return setWorkConversation(ctx, q, *w, source, thread, true)
}

func bindWorkDelegation(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, policyID string, revision int64) error {
	if policyID != "" {
		row, e := q.GetAutomationPolicyID(ctx, policyID)
		if e != nil {
			return missing(e, "The automation policy")
		}
		p, e := automationPolicy(row)
		if e != nil {
			return e
		}
		if p.RoomID != w.RoomID || p.OwnerUserID != w.OwnerUserID || p.OwnerUserID != actorID(ctx) {
			return problem("forbidden", "Delegate only through your own Room policy.")
		}
		if revision < 1 || p.Revision != revision {
			return problem("revision_conflict", "The policy changed. Review its current delegation before enabling follow-up.")
		}
		if !p.Enabled || p.Mode != "auto" {
			return problem("source_unavailable", "Enable an Auto policy before delegating future work.")
		}
		if e = automationAuthority(ctx, q, p); e != nil {
			return e
		}
		w.DelegationPolicyRevision = p.Revision
	} else {
		w.DelegationPolicyRevision = 0
	}
	frontier, e := q.AutomationFrontier(ctx, w.RoomID)
	if e != nil {
		return e
	}
	w.PolicyID, w.DelegationStartAfter = policyID, frontier
	return nil
}

func workIntentReason(in orchestration.WorkItemOperation) (string, string, error) {
	text := in.Comment
	if text == "" {
		text = in.Reason
	}
	if !validText(text, 8192, true) {
		return "", "", problem("invalid_work_item", "Use a comment of at most 8192 UTF-8 bytes.")
	}
	if strings.TrimSpace(text) != "" {
		return text, "user", nil
	}
	if in.Kind == "answer" || in.Kind == "answer_continue" {
		if !validText(in.Content, 8192, false) {
			return "", "", problem("invalid_work_item", "Provide your answer.")
		}
		return in.Content, "user", nil
	}
	labels := map[string]string{"execute": "Started a finite collaboration.", "wait": "Scheduled a follow-up.", "complete": "Confirmed the matter is complete.", "stop": "Stopped this matter.", "cancel": "Stopped future follow-ups.", "delegate": "Changed automatic follow-up delegation.", "evidence": "Associated immutable source evidence.", "follow": "Followed a source conversation.", "unfollow": "Stopped following a source conversation.", "authorize": "Changed the finite authorization.", "request_judgment": "Requested a human judgment.", "ignore": "Recorded no action for this evidence."}
	return labels[in.Kind], "system", nil
}

func recordWorkHumanFact(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, kind, text string) (string, error) {
	user, e := q.GetUser(ctx, actorID(ctx))
	if e != nil || !user.Enabled {
		return "", problem("unauthenticated", "An enabled member is required.")
	}
	ev := orchestration.SourceEvent{ID: "work-intent:" + newID(), RoomID: w.RoomID, SourceID: "workitem", ThreadKey: "workitem:" + w.ID, WorkItemID: w.ID, Kind: "workitem." + kind, ActorKind: "user", ActorID: user.ID, ActorLogin: user.Name, Content: text, OccurredAt: now(), Status: "context"}
	return ev.ID, insertWorkFact(ctx, q, w, ev)
}

func startWorkExecution(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, ids []string, first, comment, requestID, hash string, b Budget) (string, error) {
	if workTerminal(*w) || workExpired(*w) {
		return "", problem("work_budget_exhausted", "This matter no longer authorizes execution.")
	}
	if w.ActiveTaskID != "" {
		return "", problem("task_busy", "A collaboration is already active for this matter.")
	}
	if b.MaxTokens < 1 || b.MaxSteps < 1 || b.TimeoutSeconds < 1 {
		return "", problem("execution_unavailable", "Configure a model and Runner before starting work.")
	}
	if w.Usage.Tasks >= w.Budget.MaxTasks || w.Usage.ReservedModelRequests >= w.Budget.MaxModelRequests {
		return "", problem("work_budget_exhausted", "The cumulative execution allowance is exhausted.")
	}
	if len(ids) == 0 && w.PolicyID != "" {
		p, e := workPolicyForItem(ctx, q, *w)
		if e != nil {
			return "", e
		}
		if p.OwnerUserID != w.OwnerUserID {
			return "", problem("forbidden", "The saved policy delegation changed.")
		}
		ids = append([]string{}, p.ParticipantAgentIDs...)
		first = p.FirstAgentID
	}
	if len(ids) == 1 && first == "" {
		first = ids[0]
	}
	if comment != "" {
		if _, e := recordWorkHumanFact(ctx, q, w, "instruction", comment); e != nil {
			return "", e
		}
	}
	maxRequests := min(24, w.Budget.MaxModelRequests-w.Usage.ReservedModelRequests)
	t, e := createTaskTx(ctx, q, w.RoomID, CreateTaskInput{InputProtocol: 1, Goal: w.Goal, ParticipantAgentIDs: ids, FirstAgentID: first}, b, taskCreation{RequesterUserID: actorID(ctx), OriginAuthorKind: "user", OriginAuthorUserID: actorID(ctx), MaxTurns: 6, MaxModelRequests: maxRequests})
	if e != nil {
		return "", e
	}
	if e = attachWorkTask(ctx, q, w, &t, maxRequests); e != nil {
		return "", e
	}
	if e = enqueueTaskRound(ctx, q, &t, first, t.Goal); e != nil {
		return "", e
	}
	if e = saveTask(ctx, q, &t); e != nil {
		return "", e
	}
	if e = recordTaskAdmission(ctx, q, t, orchestration.TaskStart, orchestration.RequestRef{Scope: "work-item:" + w.ID, ID: requestID, Hash: hash}, t.OriginMessageID); e != nil {
		return "", e
	}
	return t.ID, nil
}

func answerWorkExecution(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, in orchestration.WorkItemOperation, hash string) error {
	if in.TaskID == "" || in.TaskID != w.ActiveTaskID {
		return problem("revision_conflict", "The visible collaboration is no longer this matter's active work.")
	}
	t, e := getTask(ctx, q, in.TaskID)
	if e != nil {
		return e
	}
	if t.Revision != in.TaskRevision {
		return problem("revision_conflict", "The collaboration changed. Review its current question.")
	}
	if t.Status != "waiting_for_person" || t.Question == nil || t.Question.ID != in.QuestionID || t.Question.Answer != "" {
		return problem("question_answered", "This question is no longer waiting for your answer.")
	}
	if e = authorizeTask(ctx, q, t); e != nil {
		return e
	}
	content := in.Content
	if content == "" {
		content = in.Reason
	}
	messageID, e := addTaskInput(ctx, q, &t, content, t.InputProtocol == 1)
	if e != nil {
		return e
	}
	t.Question.Answer, t.Question.AnsweredByUserID = strings.TrimSpace(content), actorID(ctx)
	if e = enqueueTaskRound(ctx, q, &t, t.CurrentAgentID, "A team member answered the current question. Continue the authorized goal within its remaining allowance."); e != nil {
		return e
	}
	if e = saveTask(ctx, q, &t); e != nil {
		return e
	}
	if e = recordTaskAdmission(ctx, q, t, orchestration.TaskAnswer, orchestration.RequestRef{Scope: "work-item:" + w.ID, ID: in.RequestID, Hash: hash}, messageID); e != nil {
		return e
	}
	w.Status = "running"
	w.Question, w.QuestionKind = "", ""
	return nil
}

func stopWorkItem(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem, reason, request, hash string) (orchestration.WorkItem, error) {
	if e := q.CancelWorkTimers(ctx, w.ID); e != nil {
		return w, e
	}
	if e := q.DisableWorkWatches(ctx, w.ID); e != nil {
		return w, e
	}
	if e := q.RetireWorkFollowUps(ctx, w.ID); e != nil {
		return w, e
	}
	hadTask := w.ActiveTaskID != ""
	w.StopRequested = true
	w.Question, w.WaitCondition, w.QuestionKind = "", "", ""
	w.Status = "cancelled"
	if hadTask {
		w.Status = "stopping"
	}
	if e := saveWorkItem(ctx, q, &w); e != nil {
		return w, e
	}
	if hadTask {
		t, e := getTask(ctx, q, w.ActiveTaskID)
		if e != nil {
			return w, e
		}
		// Revocation belongs to the parent matter's current owner/admin. It does
		// not grant that actor cancellation authority over an unrelated Task.
		link, e := q.WorkItemForTask(ctx, t.ID)
		if e != nil {
			return w, e
		}
		if t.WorkItemID != w.ID || link.WorkItemID != w.ID {
			return w, problem("forbidden", "This collaboration does not belong to the stopped matter.")
		}
		if e = workManager(ctx, q, w); e != nil {
			return w, e
		}
		if e = q.CloseTaskAutomationSubscriptions(ctx, t.ID); e != nil {
			return w, e
		}
		if e = q.RetireCancelledAutomationEvents(ctx, t.ID); e != nil {
			return w, e
		}
		t.Status, t.Error = "cancelling", "Stopped with its parent matter."
		if e = saveTask(ctx, q, &t); e != nil {
			return w, e
		}
		if t.ActiveRunID != "" {
			r, e := getRun(ctx, q, t.ActiveRunID)
			if e != nil {
				return w, e
			}
			if _, e = cancelRemoteRun(ctx, q, r, reason); e != nil {
				return w, e
			}
			t, e = getTask(ctx, q, t.ID)
			if e != nil {
				return w, e
			}
		} else {
			t.Status = "cancelled"
		}
		if e = saveTask(ctx, q, &t); e != nil {
			return w, e
		}
		if e = recordTaskAdmission(ctx, q, t, orchestration.TaskCancel, orchestration.RequestRef{Scope: "work-item:" + w.ID, ID: request, Hash: hash}, t.OriginMessageID); e != nil {
			return w, e
		}
	}
	// This bounded eager sweep covers ordinary stops. The dispatch owner repeats
	// the same parent fence for any larger backlog before a POST can be claimed.
	deliveries, e := q.WorkDeliveriesToWithdraw(ctx, w.ID)
	if e != nil {
		return w, e
	}
	for _, row := range deliveries {
		d, e := automationDelivery(row)
		if e != nil {
			return w, e
		}
		if e = withdrawWorkDelivery(ctx, q, d); e != nil {
			return w, e
		}
	}
	w, e = getWorkItem(ctx, q, w.ID)
	if e != nil {
		return w, e
	}
	if !hadTask {
		if e = workNotice(ctx, q, &w, "work_item_finished", reason); e != nil {
			return w, e
		}
	}
	return w, nil
}

func withdrawWorkDelivery(ctx context.Context, q *sqlgen.Queries, d orchestration.Delivery) error {
	d.Status, d.Withdrawn = "withdrawn", true
	d.Reason = "The parent matter was stopped. No further comment is authorized; earlier uncertain delivery evidence is retained."
	return saveAutomationDelivery(ctx, q, d)
}

func (s *Store) FindCreateWorkItem(ctx context.Context, room string, in orchestration.WorkItemInput) (out orchestration.WorkItem, found bool, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		var e error
		out, found, _, e = workReceipt(ctx, q, in.RequestID, []any{"create", room, in})
		return e
	})
	return
}
