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

func workItemRow(row sqlgen.WorkItem) (w orchestration.WorkItem, err error) {
	err = json.Unmarshal([]byte(row.Value), &w)
	w.ID, w.Seq, w.RoomID, w.OwnerUserID, w.PolicyID = row.ID, row.Seq, row.RoomID, row.OwnerUserID, row.PolicyID
	w.Status, w.Revision, w.CreatedAt, w.UpdatedAt = row.Status, row.Revision, row.CreatedAt, row.UpdatedAt
	normalizeWorkItem(&w)
	return
}

func getWorkItem(ctx context.Context, q *sqlgen.Queries, id string) (orchestration.WorkItem, error) {
	r, err := q.GetWorkItem(ctx, id)
	if err != nil {
		return orchestration.WorkItem{}, missing(err, "The work item")
	}
	return workItemRow(r)
}

func workItemSnapshot(w orchestration.WorkItem) ([]byte, error) {
	w.Evidence, w.Decisions, w.Tasks, w.Timers, w.Actions, w.Watches = nil, nil, nil, nil, nil, nil
	w.HistoryLimited = false
	w.Execution, w.Result = nil, nil
	normalizeWorkItem(&w)
	return json.Marshal(w)
}

func saveWorkItem(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	w.UpdatedAt = now()
	raw, err := workItemSnapshot(*w)
	if err != nil {
		return err
	}
	storedStatus := w.Status
	if storedStatus == "stopping" {
		storedStatus = "running"
	}
	n, err := q.SaveWorkItem(ctx, sqlgen.SaveWorkItemParams{ID: w.ID, Revision: w.Revision, PolicyID: w.PolicyID, Status: storedStatus, Value: string(raw), UpdatedAt: w.UpdatedAt})
	if err != nil {
		return err
	}
	if n != 1 {
		return problem("revision_conflict", "The work item changed. Reload it before deciding.")
	}
	w.Revision++
	return event(ctx, q, "work_item.updated", w.RoomID, w.ID)
}

func workItemDetails(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) (orchestration.WorkItem, error) {
	normalizeWorkItem(&w)
	w.Evidence, w.Decisions, w.Tasks, w.Timers, w.Actions, w.Watches = []orchestration.WorkEvidence{}, []orchestration.WorkDecision{}, []orchestration.WorkTask{}, []orchestration.WorkTimer{}, []orchestration.WorkAction{}, []orchestration.WorkWatch{}
	evidence, err := q.WorkEvidence(ctx, w.ID)
	if err != nil {
		return w, err
	}
	w.HistoryLimited = len(evidence) > 20
	for i, e := range evidence {
		if i == 20 {
			break
		}
		w.Evidence = append(w.Evidence, orchestration.WorkEvidence{EventID: e.EventID, Kind: e.Kind, CreatedAt: e.CreatedAt})
	}
	decisions, err := q.WorkDecisions(ctx, sqlgen.WorkDecisionsParams{WorkItemID: w.ID, Seq: 0, Limit: 21})
	if err != nil {
		return w, err
	}
	w.HistoryLimited = w.HistoryLimited || len(decisions) > 20
	for i, r := range decisions {
		if i == 20 {
			break
		}
		var d orchestration.WorkDecision
		if err = json.Unmarshal([]byte(r.Value), &d); err != nil {
			return w, err
		}
		d.Seq = r.Seq
		w.Decisions = append(w.Decisions, d)
	}
	tasks, err := q.WorkTasks(ctx, w.ID)
	if err != nil {
		return w, err
	}
	w.HistoryLimited = w.HistoryLimited || len(tasks) > 20
	for i, t := range tasks {
		if i == 20 {
			break
		}
		w.Tasks = append(w.Tasks, orchestration.WorkTask{ID: t.TaskID, Status: t.Status, Summary: t.Summary, CreatedAt: t.CreatedAt})
	}
	timers, err := q.WorkTimers(ctx, w.ID)
	if err != nil {
		return w, err
	}
	w.HistoryLimited = w.HistoryLimited || len(timers) > 20
	for i, t := range timers {
		if i == 20 {
			break
		}
		w.Timers = append(w.Timers, orchestration.WorkTimer{ID: t.ID, DueAt: t.DueAt, Condition: t.Condition, Status: t.Status, EventID: t.EventID})
	}
	actions, err := q.WorkActions(ctx, w.ID)
	if err != nil {
		return w, err
	}
	w.HistoryLimited = w.HistoryLimited || len(actions) > 20
	for i, a := range actions {
		if i == 20 {
			break
		}
		w.Actions = append(w.Actions, orchestration.WorkAction{ID: a.ID, Kind: a.Kind, Status: a.CurrentStatus, TaskID: a.TaskID, DeliveryID: a.DeliveryID, Body: a.Body, CreatedAt: a.CreatedAt})
	}
	watches, err := q.WorkWatches(ctx, w.ID)
	if err != nil {
		return w, err
	}
	for _, v := range watches {
		w.Watches = append(w.Watches, orchestration.WorkWatch{SourceID: v.SourceID, ThreadKey: v.ThreadKey, Enabled: v.Enabled != 0})
	}
	if err = populateWorkView(ctx, q, &w); err != nil {
		return w, err
	}
	return w, nil
}

func (s *Store) GetWorkItem(ctx context.Context, id string) (out orchestration.WorkItem, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		w, e := getWorkItem(ctx, q, id)
		if e != nil {
			return e
		}
		out, e = workItemDetails(ctx, q, w)
		return e
	})
	return
}

func (s *Store) WorkItems(ctx context.Context, room string, after int64, limit int) (out orchestration.WorkItemPage, err error) {
	if after < 0 || limit < 0 || limit > 20 {
		return out, problem("invalid_page", "Read at most 20 work items at a time.")
	}
	if limit == 0 {
		limit = 20
	}
	out.Items = []orchestration.WorkItem{}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		if _, e := q.GetRoom(ctx, room); e != nil {
			return missing(e, "The Room")
		}
		rows, e := q.WorkItemsPage(ctx, sqlgen.WorkItemsPageParams{RoomID: room, Seq: after, Limit: int64(limit + 1)})
		if e != nil {
			return e
		}
		out.HasMore = len(rows) > limit
		for i, r := range rows {
			if i == limit {
				break
			}
			w, e := workItemRow(r)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, w)
			out.NextAfter = w.Seq
		}
		return nil
	})
	return
}

func defaultWorkBudget(b orchestration.WorkBudget) orchestration.WorkBudget {
	if b == (orchestration.WorkBudget{}) {
		return orchestration.WorkBudget{MaxEvaluations: 12, MaxTasks: 4, MaxModelRequests: 96, MaxActions: 16, MaxFollowUps: 8}
	}
	return b
}
func validateWorkBudget(b orchestration.WorkBudget) error {
	if b.MaxEvaluations < 1 || b.MaxEvaluations > 1000 || b.MaxTasks < 1 || b.MaxTasks > 100 || b.MaxModelRequests < 1 || b.MaxModelRequests > 9600 || b.MaxActions < 1 || b.MaxActions > 1000 || b.MaxFollowUps < 0 || b.MaxFollowUps > 100 {
		return problem("invalid_work_item", "Use finite positive work item allowances.")
	}
	return nil
}
func workTime(value string) (string, error) {
	t, e := time.Parse(time.RFC3339, value)
	if e != nil {
		return "", problem("invalid_work_item", "Use an RFC3339 date with a timezone.")
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}
func workExpired(w orchestration.WorkItem) bool {
	t, e := time.Parse(time.RFC3339Nano, w.Deadline)
	return e != nil || !time.Now().Before(t)
}
func workTerminal(w orchestration.WorkItem) bool {
	return w.StopRequested || w.Status == "stopping" || w.Status == "completed" || w.Status == "cancelled"
}

func workReceipt(ctx context.Context, q *sqlgen.Queries, request string, in any) (orchestration.WorkItem, bool, string, error) {
	if !validText(request, 128, false) {
		return orchestration.WorkItem{}, false, "", problem("invalid_work_item", "A stable request ID is required.")
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return orchestration.WorkItem{}, false, "", e
	}
	hash := remoteHash(raw)
	r, e := q.GetWorkRequest(ctx, sqlgen.GetWorkRequestParams{UserID: actorID(ctx), RequestID: request})
	if errors.Is(e, sql.ErrNoRows) {
		return orchestration.WorkItem{}, false, hash, nil
	}
	if e != nil {
		return orchestration.WorkItem{}, false, hash, e
	}
	if r.Cancelled != 0 {
		return orchestration.WorkItem{}, false, hash, problem("request_cancelled", "This request was cancelled. Use a new request ID.")
	}
	if r.RequestHash != hash {
		return orchestration.WorkItem{}, false, hash, problem("request_conflict", "This request ID belongs to another work item operation.")
	}
	var w orchestration.WorkItem
	e = json.Unmarshal([]byte(r.Result), &w)
	return w, true, hash, e
}
func saveWorkReceipt(ctx context.Context, q *sqlgen.Queries, request, hash string, w orchestration.WorkItem) error {
	raw, e := json.Marshal(w)
	if e != nil {
		return e
	}
	return q.SaveWorkRequest(ctx, sqlgen.SaveWorkRequestParams{UserID: actorID(ctx), RequestID: request, RequestHash: hash, Result: string(raw)})
}
func (s *Store) WorkItemRequest(ctx context.Context, id string) (out orchestration.WorkItem, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		r, e := q.GetWorkRequest(ctx, sqlgen.GetWorkRequestParams{UserID: actorID(ctx), RequestID: id})
		if e != nil {
			return missing(e, "The work item operation")
		}
		if r.Cancelled != 0 {
			return problem("request_cancelled", "This request was cancelled.")
		}
		return json.Unmarshal([]byte(r.Result), &out)
	})
	return
}

func workManager(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) error {
	if e := requireActor(ctx, q, false); e != nil {
		return e
	}
	u, e := q.GetUser(ctx, actorID(ctx))
	if e != nil {
		return problem("unauthenticated", "Sign in to manage a work item.")
	}
	if u.ID != w.OwnerUserID && u.Role != "admin" {
		return problem("forbidden", "Only the work item owner or an administrator can decide it.")
	}
	return nil
}
func linkWorkEvidence(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, ids []string) error {
	if len(ids) > 20 {
		return problem("invalid_work_item", "Associate at most 20 evidence events per operation.")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return problem("invalid_work_item", "Evidence events must be distinct.")
		}
		seen[id] = true
		r, e := q.GetAutomationEvent(ctx, id)
		if e != nil || r.RoomID != w.RoomID {
			return problem("invalid_work_item", "Choose evidence from this Room.")
		}
		if _, e = q.InsertWorkEvidence(ctx, sqlgen.InsertWorkEvidenceParams{WorkItemID: w.ID, EventID: id, Kind: r.Kind, CreatedAt: now()}); e != nil {
			return e
		}
	}
	return nil
}
func insertWorkItem(ctx context.Context, q *sqlgen.Queries, room, owner string, in orchestration.WorkItemInput) (w orchestration.WorkItem, err error) {
	in.Goal = strings.TrimSpace(in.Goal)
	in.CompletionCriteria = strings.TrimSpace(in.CompletionCriteria)
	if !validText(in.Goal, 8192, false) || !validText(in.CompletionCriteria, 8192, true) {
		return w, problem("invalid_work_item", "Provide a goal and optional completion criteria of at most 8192 bytes each.")
	}
	in.Budget = defaultWorkBudget(in.Budget)
	if err = validateWorkBudget(in.Budget); err != nil {
		return
	}
	if _, err = q.GetRoom(ctx, room); err != nil {
		return w, missing(err, "The Room")
	}
	if in.ExpiresAt != "" {
		if in.Deadline != "" && in.Deadline != in.ExpiresAt {
			return w, problem("invalid_work_item", "Provide one authorization expiry.")
		}
		in.Deadline = in.ExpiresAt
	}
	if in.Deadline == "" {
		in.Deadline = time.Now().Add(workAuthorizationLifetime).UTC().Format(time.RFC3339Nano)
	}
	in.DueAt, err = workDueAt(in.DueAt)
	if err != nil {
		return w, err
	}
	in.Deadline, err = workTime(in.Deadline)
	if err != nil {
		return
	}
	if deadline, _ := time.Parse(time.RFC3339Nano, in.Deadline); !deadline.After(time.Now()) || deadline.After(time.Now().Add(366*24*time.Hour)) {
		return w, problem("invalid_work_item", "Choose a deadline within the next year.")
	}
	if in.PolicyID != "" {
		r, e := q.GetAutomationPolicyID(ctx, in.PolicyID)
		if e != nil || r.RoomID != room || r.OwnerUserID != owner {
			return w, problem("invalid_work_item", "Choose a policy authorized by this work item's owner in this Room.")
		}
		if in.PolicyRevision > 0 && in.PolicyRevision != r.Revision {
			return w, problem("revision_conflict", "The policy changed before this matter was created.")
		}
	}
	w = orchestration.WorkItem{ID: newID(), RoomID: room, OwnerUserID: owner, PolicyID: in.PolicyID, Goal: in.Goal, CompletionCriteria: in.CompletionCriteria, Status: "open", Revision: 1, Deadline: in.Deadline, Budget: in.Budget, CreatedAt: now(), UpdatedAt: now()}
	w.ExpiresAt, w.DueAt = w.Deadline, in.DueAt
	normalizeWorkItem(&w)
	if in.PolicyID != "" {
		w.DelegationStartAfter, err = q.AutomationFrontier(ctx, room)
		if err != nil {
			return w, err
		}
		w.DelegationPolicyRevision = in.PolicyRevision
	}
	raw, err := workItemSnapshot(w)
	if err != nil {
		return
	}
	w.Seq, err = q.InsertWorkItem(ctx, sqlgen.InsertWorkItemParams{ID: w.ID, RoomID: room, OwnerUserID: owner, PolicyID: in.PolicyID, Status: w.Status, Value: string(raw), CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt})
	if err != nil {
		return
	}
	err = linkWorkEvidence(ctx, q, &w, in.EvidenceIDs)
	return
}
func appendWorkDecision(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem, d orchestration.WorkDecision) error {
	d.ID = newID()
	d.CreatedAt = now()
	if d.EvidenceIDs == nil {
		d.EvidenceIDs = []string{}
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return e
	}
	return q.InsertWorkDecision(ctx, sqlgen.InsertWorkDecisionParams{ID: d.ID, WorkItemID: w.ID, Value: string(raw)})
}
func (s *Store) CreateWorkItem(ctx context.Context, room string, in orchestration.WorkItemInput) (orchestration.WorkItem, error) {
	return s.CreateWorkItemWithBudget(ctx, room, in, Budget{})
}

func (s *Store) CreateWorkItemWithBudget(ctx context.Context, room string, in orchestration.WorkItemInput, b Budget) (out orchestration.WorkItem, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		old, replay, hash, e := workReceipt(ctx, q, in.RequestID, []any{"create", room, in})
		if e != nil {
			return e
		}
		if replay {
			out = old
			return nil
		}
		out, e = insertWorkItem(ctx, q, room, actorID(ctx), in)
		if e != nil {
			return e
		}
		sourceID := in.SourceEventID
		if sourceID == "" && in.MessageID == "" && in.Follow && len(in.EvidenceIDs) == 1 {
			sourceID = in.EvidenceIDs[0]
		}
		if e = bindWorkSource(ctx, q, &out, sourceID, in.MessageID, in.Follow); e != nil {
			return e
		}
		if in.Start {
			if _, e = startWorkExecution(ctx, q, &out, in.ParticipantAgentIDs, in.FirstAgentID, "", in.RequestID, hash, b); e != nil {
				return e
			}
			if e = saveWorkItem(ctx, q, &out); e != nil {
				return e
			}
		}
		if e = appendWorkDecision(ctx, q, out, orchestration.WorkDecision{Kind: "create", Reason: in.Goal, ActorKind: "user", ActorID: actorID(ctx), EvidenceIDs: in.EvidenceIDs, Budget: &out.Budget, Deadline: out.Deadline}); e != nil {
			return e
		}
		if e = event(ctx, q, "work_item.updated", room, out.ID); e != nil {
			return e
		}
		out, e = workItemDetails(ctx, q, out)
		if e != nil {
			return e
		}
		return saveWorkReceipt(ctx, q, in.RequestID, hash, out)
	})
	return
}

func workNotice(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, kind, text string) error {
	seq, e := q.NextMessageSequence(ctx, w.RoomID)
	if e != nil {
		return e
	}
	m := Message{ID: newID(), RoomID: w.RoomID, Seq: seq, AuthorKind: "system", AuthorName: "Work item", Content: text, CreatedAt: now(), UpdatedAt: now()}
	if e = insertMessage(ctx, q, m, ""); e != nil {
		return e
	}
	id := newID()
	if e = q.CreateWorkInbox(ctx, sqlgen.CreateWorkInboxParams{ID: id, UserID: w.OwnerUserID, Kind: kind, RoomID: w.RoomID, MessageID: m.ID, WorkItemID: w.ID, CreatedAt: now()}); e != nil {
		return e
	}
	return q.InsertWorkAction(ctx, sqlgen.InsertWorkActionParams{ID: id, WorkItemID: w.ID, Kind: "inbox", Status: "delivered", Body: text, CreatedAt: now()})
}

func (s *Store) OperateWorkItem(ctx context.Context, id string, in orchestration.WorkItemOperation, b Budget) (out orchestration.WorkItem, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		old, replay, hash, e := workReceipt(ctx, q, in.RequestID, []any{"decision", id, in})
		if e != nil {
			return e
		}
		if replay {
			out = old
			return nil
		}
		w, e := getWorkItem(ctx, q, id)
		if e != nil {
			return e
		}
		if e = workManager(ctx, q, w); e != nil {
			return e
		}
		if w.Revision != in.Revision {
			return problem("revision_conflict", "The work item changed. Reload it before deciding.")
		}
		if workTerminal(w) {
			return problem("work_item_finished", "This work item is finished. Create a new matter to authorize further work.")
		}
		reason, reasonSource, e := workIntentReason(in)
		if e != nil {
			return e
		}
		in.Reason = reason
		if in.ExpiresAt != "" {
			if in.Deadline != "" && in.Deadline != in.ExpiresAt {
				return problem("invalid_work_item", "Provide one authorization expiry.")
			}
			in.Deadline = in.ExpiresAt
		}

		if e = linkWorkEvidence(ctx, q, &w, in.EvidenceIDs); e != nil {
			return e
		}
		d := orchestration.WorkDecision{Kind: in.Kind, Reason: in.Reason, ReasonSource: reasonSource, ActorKind: "user", ActorID: actorID(ctx), EvidenceIDs: in.EvidenceIDs}
		switch in.Kind {
		case "authorize":
			if workExpired(w) && in.Deadline == "" {
				return problem("invalid_work_item", "Provide a future deadline to authorize an expired matter.")
			}
			if in.Budget == nil {
				return problem("invalid_work_item", "Provide the new cumulative allowance.")
			}
			if e = validateWorkBudget(*in.Budget); e != nil {
				return e
			}
			next := *in.Budget
			if next.MaxEvaluations < w.Usage.Evaluations || next.MaxTasks < w.Usage.Tasks || next.MaxModelRequests < w.Usage.ReservedModelRequests || next.MaxActions < w.Usage.Actions || next.MaxFollowUps < w.Usage.FollowUps {
				return problem("invalid_work_item", "The new allowance must cover all work already reserved or consumed.")
			}
			w.Budget = next
			d.Budget = &next
			if in.Deadline != "" {
				deadline, e := workTime(in.Deadline)
				if e != nil {
					return e
				}
				t, _ := time.Parse(time.RFC3339Nano, deadline)
				if !t.After(time.Now()) || t.After(time.Now().Add(366*24*time.Hour)) {
					return problem("invalid_work_item", "Choose a future deadline within one year.")
				}
				w.Deadline, w.ExpiresAt = deadline, deadline
				d.Deadline = deadline
			}
			if w.Status == "needs_attention" {
				w.Status = "open"
				w.Question = ""
				w.QuestionKind = ""
			}
		case "evidence":
			if len(in.EvidenceIDs) == 0 {
				return problem("invalid_work_item", "Choose at least one evidence event.")
			}
		case "follow", "unfollow":
			if in.SourceEventID != "" || in.MessageID != "" {
				if in.Kind == "follow" {
					if e = bindWorkSource(ctx, q, &w, in.SourceEventID, in.MessageID, true); e != nil {
						return e
					}
				} else {
					return problem("invalid_work_item", "Choose the followed conversation to stop following.")
				}
			} else if e = setWorkConversation(ctx, q, w, in.SourceID, in.ThreadKey, in.Kind == "follow"); e != nil {
				return e
			}
		case "delegate":
			if e = bindWorkDelegation(ctx, q, &w, in.PolicyID, in.PolicyRevision); e != nil {
				return e
			}
			d.PolicyID, d.PolicyRevision = w.PolicyID, w.DelegationPolicyRevision
			if in.Follow {
				if e = bindWorkSource(ctx, q, &w, in.SourceEventID, in.MessageID, true); e != nil {
					return e
				}
			}
		case "stop":
			w, e = stopWorkItem(ctx, q, w, in.Reason, in.RequestID, hash)
			if e != nil {
				return e
			}
		case "execute":
			comment := in.Content
			if comment == "" {
				comment = in.Comment
			}
			d.TaskID, e = startWorkExecution(ctx, q, &w, in.ParticipantAgentIDs, in.FirstAgentID, comment, in.RequestID, hash, b)
			if e != nil {
				return e
			}
		case "wait":
			if w.ActiveTaskID != "" {
				return problem("task_busy", "Finish or cancel the active collaboration before waiting.")
			}
			if in.WaitCondition == "" && in.ResumeAt != "" {
				in.WaitCondition = "Check this matter again at " + in.ResumeAt
			}
			if !validText(in.WaitCondition, 8192, false) {
				return problem("invalid_work_item", "State what must happen before following up.")
			}
			w.Status = "waiting"
			w.WaitCondition = in.WaitCondition
			w.Question = ""
			w.QuestionKind = ""
			if e = q.CancelWorkTimers(ctx, w.ID); e != nil {
				return e
			}
			if in.ResumeAt != "" {
				due, e := workTime(in.ResumeAt)
				if e != nil {
					return e
				}
				t, _ := time.Parse(time.RFC3339Nano, due)
				deadline, _ := time.Parse(time.RFC3339Nano, w.Deadline)
				if !t.After(time.Now()) || t.After(deadline) {
					return problem("invalid_work_item", "Schedule a future follow-up before the work item deadline.")
				}
				if w.Usage.FollowUps >= w.Budget.MaxFollowUps {
					return problem("work_budget_exhausted", "The follow-up allowance is exhausted.")
				}
				if e = q.InsertWorkTimer(ctx, sqlgen.InsertWorkTimerParams{ID: newID(), WorkItemID: w.ID, DueAt: due, Condition: in.WaitCondition, CreatedAt: now()}); e != nil {
					return e
				}
			}
		case "request_judgment":
			if w.ActiveTaskID != "" {
				return problem("task_busy", "Finish or cancel the active collaboration first.")
			}
			if !validText(in.Question, 8192, false) {
				return problem("invalid_work_item", "Provide the question for a person.")
			}
			w.Status = "waiting_for_person"
			w.Question = in.Question
			w.QuestionKind = orchestration.WorkQuestionJudgment
			if e = q.CancelWorkTimers(ctx, w.ID); e != nil {
				return e
			}
			if e = workNotice(ctx, q, &w, "work_item_attention", in.Question); e != nil {
				return e
			}
		case "answer", "answer_continue":
			if w.ActiveTaskID == "" && (in.TaskID != "" || in.QuestionID != "" || in.TaskRevision != 0) {
				return problem("question_answered", "The visible collaboration question no longer belongs to active work.")
			}
			if workExpired(w) {
				return problem("work_budget_exhausted", "Renew the finite authorization before resuming an expired matter.")
			}
			if w.ActiveTaskID != "" {
				if e = answerWorkExecution(ctx, q, &w, in, hash); e != nil {
					return e
				}
			} else {
				if w.Status != "waiting_for_person" && w.Status != "needs_attention" {
					return problem("invalid_work_item", "This matter is not waiting for a judgment.")
				}
				content := in.Content
				if content == "" {
					content = in.Reason
				}
				if !validText(content, 8192, false) {
					return problem("invalid_work_item", "Provide your answer.")
				}
				w.Status, w.Question, w.QuestionKind = "open", "", ""
				if e = recordWorkHumanAnswer(ctx, q, &w, content); e != nil {
					return e
				}
				if in.Kind == "answer_continue" {
					d.TaskID, e = startWorkExecution(ctx, q, &w, in.ParticipantAgentIDs, in.FirstAgentID, "", in.RequestID, hash, b)
					if e != nil {
						return e
					}
				}
			}
		case "complete", "cancel":
			if w.ActiveTaskID != "" {
				return problem("task_busy", "Finish or cancel the active collaboration first.")
			}
			if in.Kind == "complete" {
				confirmation, e := recordWorkHumanFact(ctx, q, &w, "confirmation", in.Reason)
				if e != nil {
					return e
				}
				d.EvidenceIDs = append(d.EvidenceIDs, confirmation)
				completed, e := completeWorkItem(ctx, q, &w, in.Reason)
				if e != nil {
					return e
				}
				if !completed {
					d.ReasonSource = "system"
					d.Kind = "wait"
					d.Reason = "Completion deferred until external action receipts are confirmed. " + in.Reason
				}
			} else {
				w.Status = "cancelled"
				w.Question = ""
				w.QuestionKind = ""
				w.WaitCondition = ""
				if e = q.CancelWorkTimers(ctx, w.ID); e != nil {
					return e
				}
				if e = workNotice(ctx, q, &w, "work_item_finished", in.Reason); e != nil {
					return e
				}
			}
		case "ignore":
		default:
			return problem("invalid_work_item", "Choose a supported work item decision.")
		}
		if e = appendWorkDecision(ctx, q, w, d); e != nil {
			return e
		}
		if e = saveWorkItem(ctx, q, &w); e != nil {
			return e
		}
		out, e = workItemDetails(ctx, q, w)
		if e != nil {
			return e
		}
		return saveWorkReceipt(ctx, q, in.RequestID, hash, out)
	})
	return
}

func (s *Store) GetAutomationEvent(ctx context.Context, id string) (out orchestration.SourceEvent, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		row, e := q.GetAutomationEvent(ctx, id)
		if e != nil {
			return missing(e, "The source event")
		}
		out, e = workEvidenceEvent(ctx, q, row)
		return e
	})
	return
}

func (s *Store) FindWorkItemOperation(ctx context.Context, id string, in orchestration.WorkItemOperation) (out orchestration.WorkItem, found bool, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		var e error
		out, found, _, e = workReceipt(ctx, q, in.RequestID, []any{"decision", id, in})
		return e
	})
	return
}
