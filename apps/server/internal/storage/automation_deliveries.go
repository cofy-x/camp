package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const maxAutomationDeliveryAttempts = 32

// Private references are stored alongside, rather than inside, the public
// dispatch history so neither API responses nor operation receipts expose them.
type storedAutomationDelivery struct {
	orchestration.Delivery
	AttemptCredentials []string `json:"attemptCredentials,omitempty"`
}

func automationDelivery(row sqlgen.AutomationDelivery) (d orchestration.Delivery, err error) {
	var stored storedAutomationDelivery
	if err = json.Unmarshal([]byte(row.Value), &stored); err != nil {
		return
	}
	d = stored.Delivery
	for index := range d.Attempts {
		if index < len(stored.AttemptCredentials) {
			d.Attempts[index].CredentialRef = stored.AttemptCredentials[index]
		}
		if d.Attempts[index].Outcome == "sending" && row.Status != "sending" {
			d.Attempts[index].Outcome = "unknown"
		}
	}
	d.Status = row.Status
	d.Withdrawn = d.Withdrawn || row.Status == "withdrawn"
	d.Revision = row.Revision
	d.CredentialRef = row.CredentialRef
	// Older retained outcomes have one known dispatch identity but no exact
	// dispatch revision. Preserve it before any recovery can rotate the actor.
	if len(d.Attempts) == 0 && d.ActorID > 0 && (row.Status == "sending" || row.Status == "sent" || row.Status == "unknown" || row.Status == "reconcile") {
		outcome := row.Status
		if outcome == "reconcile" {
			outcome = "unknown"
		}
		d.Attempts = []orchestration.DeliveryAttempt{{ActorID: d.ActorID, CredentialRef: d.CredentialRef, Outcome: outcome}}
	}
	if len(d.Receipts) == 0 && d.CommentID > 0 && d.URL != "" {
		d.Receipts = []orchestration.DeliveryReceipt{{CommentID: d.CommentID, ActorID: d.ActorID, URL: d.URL}}
	}
	return
}
func saveAutomationDelivery(ctx context.Context, q *sqlgen.Queries, d orchestration.Delivery) error {
	stored := storedAutomationDelivery{Delivery: d}
	for _, attempt := range d.Attempts {
		stored.AttemptCredentials = append(stored.AttemptCredentials, attempt.CredentialRef)
	}
	raw, e := json.Marshal(stored)
	if e != nil {
		return e
	}
	if err := q.UpdateAutomationDelivery(ctx, sqlgen.UpdateAutomationDeliveryParams{ID: d.ID, Status: d.Status, Value: string(raw), CredentialRef: d.CredentialRef}); err != nil {
		return err
	}
	t, err := getTask(ctx, q, d.TaskID)
	if err != nil {
		return err
	}
	row, err := q.GetAutomationDelivery(ctx, d.ID)
	if err != nil {
		return err
	}
	d.Revision = row.Revision
	if err = recordWorkActionResult(ctx, q, d); err != nil {
		return err
	}
	return event(ctx, q, "automation.delivery.updated", t.RoomID, d.ID)
}

// Queue only immutable questions, actionable failures and final results. This
// function runs in the same transaction as the corresponding task transition.
func queueAutomationTaskNotice(ctx context.Context, q *sqlgen.Queries, t CollaborationTask, kind, text string) error {
	subs, e := q.ListAutomationTaskSubscriptions(ctx, t.ID)
	if e != nil {
		return e
	}
	for _, sub := range subs {
		if sub.Enabled == 0 || sub.SourceID == "room" {
			continue
		}
		row, e := q.GetAutomationPolicy(ctx, sqlgen.GetAutomationPolicyParams{RoomID: t.RoomID, SourceID: sub.SourceID})
		if e != nil {
			continue
		}
		p, e := automationPolicy(row)
		if e != nil {
			return e
		}
		if !p.Enabled || p.Mode != "auto" || !p.AllowComments {
			continue
		}
		src, e := q.GetGitHubSource(ctx, sub.SourceID)
		if e != nil {
			return e
		}
		var repo, number int64
		if _, e = fmt.Sscanf(sub.ThreadKey, "%d:%d", &repo, &number); e != nil || repo != src.RepositoryID || number < 1 {
			continue
		}
		key := kind + ":" + t.CheckpointCommit
		if kind == "task_attention" {
			key = fmt.Sprintf("%s:%d", kind, t.Revision)
		}
		question := ""
		body := text
		if kind == "task_question" && t.Question != nil {
			question = t.Question.ID
			key = kind + ":" + question
			body += "\n\nReply with `/camp answer " + question + " <your answer>` to continue this task within its remaining allowance."
		}
		if len(body) > 48<<10 {
			return problem("invalid_task", "The external comment exceeds its bounded body limit.")
		}
		id := remoteHash([]byte(t.ID + ":" + sub.SourceID + ":" + sub.ThreadKey + ":" + key))
		d := orchestration.Delivery{ID: id, TaskID: t.ID, SourceID: sub.SourceID, PolicyID: p.ID, Revision: 1, Repository: src.Repository, RepositoryID: repo, Number: number, Body: body, Marker: "<!-- camp-effect:" + id + " -->", CredentialRef: p.CredentialRef, ActorID: p.CommentActorID, QuestionID: question, Status: "pending", CreatedAt: now()}
		raw, e := json.Marshal(d)
		if e != nil {
			return e
		}
		if e = q.InsertAutomationDelivery(ctx, sqlgen.InsertAutomationDeliveryParams{ID: id, TaskID: t.ID, SourceID: sub.SourceID, PolicyID: p.ID, Value: string(raw), CredentialRef: p.CredentialRef, CreatedAt: d.CreatedAt}); e != nil {
			return e
		}
		if t.WorkItemID != "" {
			if e = q.InsertWorkAction(ctx, sqlgen.InsertWorkActionParams{ID: d.ID, WorkItemID: t.WorkItemID, Kind: "github.comment", Status: "pending", TaskID: t.ID, DeliveryID: d.ID, Body: d.Body, CreatedAt: d.CreatedAt}); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) TaskAutomationDeliveries(ctx context.Context, id string) ([]orchestration.Delivery, error) {
	if _, e := s.GetTaskState(ctx, id); e != nil {
		return nil, e
	}
	rows, e := s.queries().ListAutomationDeliveries(ctx, id)
	if e != nil {
		return nil, e
	}
	out := []orchestration.Delivery{}
	for _, r := range rows {
		d, e := automationDelivery(r)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func (s *Store) ClaimAutomationDelivery(ctx context.Context) (out *orchestration.Delivery, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		stale, e := q.StaleAutomationDeliveries(ctx)
		if e != nil {
			return e
		}
		for _, row := range stale {
			delivery, e := automationDelivery(row)
			if e != nil {
				return e
			}
			task, e := getTask(ctx, q, delivery.TaskID)
			if e != nil {
				return e
			}
			if e = withdrawAutomationDelivery(ctx, q, delivery, task); e != nil {
				return e
			}
		}
		rows, e := q.PendingAutomationDeliveries(ctx)
		if e != nil {
			return e
		}
		for _, row := range rows {
			d, e := automationDelivery(row)
			if e != nil {
				return e
			}
			pRow, e := q.GetAutomationPolicyID(ctx, d.PolicyID)
			if e != nil {
				return e
			}
			p, e := automationPolicy(pRow)
			if e != nil {
				return e
			}
			t, e := getTask(ctx, q, d.TaskID)
			if e != nil {
				return e
			}
			d.Reconcile = row.Status == "reconcile"
			if !d.Reconcile && t.WorkItemID != "" {
				parent, e := getWorkItem(ctx, q, t.WorkItemID)
				if e != nil {
					return e
				}
				if parent.StopRequested || parent.Status == "cancelled" {
					if e = withdrawWorkDelivery(ctx, q, d); e != nil {
						return e
					}
					continue
				}
			}
			cancelled := t.Status == "cancelling" || t.Status == "cancelled"
			questionResolved := d.QuestionID != "" && (t.Status != "waiting_for_person" || t.Question == nil || t.Question.ID != d.QuestionID || t.Question.Answer != "")
			if d.Reconcile && (cancelled || questionResolved) {
				d.Withdrawn = true
			}
			if !d.Reconcile && (d.Withdrawn || cancelled || questionResolved) {
				if e = withdrawAutomationDelivery(ctx, q, d, t); e != nil {
					return e
				}
				continue
			}
			if !p.Enabled || p.Mode != "auto" || !p.AllowComments || p.CredentialRef == "" || enabledOwner(ctx, q, p.OwnerUserID) != nil || automationSourceAvailable(ctx, q, p) != nil || authorizeTaskEffects(ctx, q, t) != nil {
				d.Status = "blocked"
				if d.Withdrawn {
					d.Status = "withdrawn"
				}
				d.Reason = "The source, comment permission, credential or task authority is unavailable."
				if e = saveAutomationDelivery(ctx, q, d); e != nil {
					return e
				}
				continue
			}
			if !d.Reconcile {
				if len(d.Attempts) >= maxAutomationDeliveryAttempts || len(d.Receipts) >= maxAutomationDeliveryAttempts {
					d.Status = "blocked"
					d.Reason = "This comment has reached its limit of 32 dispatch attempts or confirmed receipts. Existing delivery evidence is retained; no further comment was sent."
					if e = saveAutomationDelivery(ctx, q, d); e != nil {
						return e
					}
					continue
				}
				if e = chargeWorkAction(ctx, q, t); e != nil {
					d.Status = "blocked"
					d.Reason = "The work item's action allowance, deadline or current state prevents this send."
					if e = saveAutomationDelivery(ctx, q, d); e != nil {
						return e
					}
					continue
				}
				d.CredentialRef = p.CredentialRef
				d.ActorID = p.CommentActorID
				d.Attempts = append(d.Attempts, orchestration.DeliveryAttempt{Revision: d.Revision + 1, ActorID: d.ActorID, CredentialRef: d.CredentialRef, StartedAt: now(), Outcome: "sending"})
			}
			d.Status = "sending"
			if e = saveAutomationDelivery(ctx, q, d); e != nil {
				return e
			}
			d.Revision++
			d.ReconciliationCredentialRef = p.CredentialRef
			out = &d
			return nil
		}
		return nil
	})
	return
}

func withdrawAutomationDelivery(ctx context.Context, q *sqlgen.Queries, d orchestration.Delivery, t CollaborationTask) error {
	d.Status, d.Withdrawn = "withdrawn", true
	if t.Status == "cancelling" || t.Status == "cancelled" {
		d.Reason = "The collaboration was cancelled. No further comment was sent."
	} else {
		d.Reason = "This question is no longer waiting for an answer. No further comment was sent."
	}
	return saveAutomationDelivery(ctx, q, d)
}

func (s *Store) CompleteAutomationDelivery(ctx context.Context, id string, result orchestration.DeliveryResult) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		row, e := q.GetAutomationDelivery(ctx, id)
		if e != nil {
			return e
		}
		if row.Status != "sending" || result.ClaimRevision != row.Revision {
			return nil
		}
		d, e := automationDelivery(row)
		if e != nil {
			return e
		}
		attemptOutcome := result.AttemptOutcome
		if result.Status == "delivered" {
			result.Status = "sent"
		}
		if result.Status != "sent" && result.Status != "unknown" && result.Status != "failed" {
			result = orchestration.DeliveryResult{Status: "unknown", Reason: "The delivery outcome is unavailable."}
		}
		if result.Status == "sent" && (result.CommentID < 1 || result.URL == "") {
			result = orchestration.DeliveryResult{Status: "unknown", Reason: "The comment receipt is incomplete."}
		}
		if result.Status == "sent" {
			result.URL = automationCommentURL(d, result.CommentID)
		}
		if count := len(d.Attempts); count > 0 && d.Attempts[count-1].Revision == row.Revision {
			if attemptOutcome == "" {
				attemptOutcome = result.Status
			}
			if attemptOutcome == "delivered" {
				attemptOutcome = "sent"
			}
			if attemptOutcome != "sent" && attemptOutcome != "failed" {
				attemptOutcome = "unknown"
			}
			d.Attempts[count-1].Outcome = attemptOutcome
		}
		d.Status, d.Reason, d.CommentID, d.URL = result.Status, result.Reason, result.CommentID, result.URL
		if d.Withdrawn {
			d.Status = "withdrawn"
			d.Reason = "No further comment is authorized. " + result.Reason
			if result.Status == "sent" {
				d.Reason = "No further comment is authorized. Earlier comment delivery was confirmed."
			}
		}
		if result.Status == "sent" && len(result.Receipts) == 0 {
			result.Receipts = []orchestration.DeliveryReceipt{{CommentID: result.CommentID, ActorID: d.ActorID, URL: result.URL}}
		}
		d.ReceiptScanLimited = d.ReceiptScanLimited || result.ReceiptScanLimited
		for _, receipt := range result.Receipts {
			if receipt.CommentID < 1 || receipt.ActorID < 1 {
				continue
			}
			receipt.URL = automationCommentURL(d, receipt.CommentID)
			found := false
			for _, old := range d.Receipts {
				if old.CommentID == receipt.CommentID {
					found = true
					break
				}
			}
			if !found {
				if len(d.Receipts) < maxAutomationDeliveryAttempts {
					d.Receipts = append(d.Receipts, receipt)
				} else {
					d.ReceiptScanLimited = true
				}
			}
		}
		if e = saveAutomationDelivery(ctx, q, d); e != nil {
			return e
		}
		t, e := getTask(ctx, q, d.TaskID)
		if e != nil {
			return e
		}
		return event(ctx, q, "automation.delivery.updated", t.RoomID, d.ID)
	})
}

func automationCommentURL(d orchestration.Delivery, commentID int64) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-%d", d.Repository, d.Number, commentID)
}

type AutomationDeliveryInput struct {
	RequestID string `json:"requestId"`
	Revision  int64  `json:"revision"`
}

func (s *Store) OperateAutomationDelivery(ctx context.Context, id, action string, in AutomationDeliveryInput) (out orchestration.Delivery, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		old, replay, hash, e := automationReceipt(ctx, q, in.RequestID, []any{"delivery", id, action, in})
		if e != nil {
			return e
		}
		if replay {
			out = *old.Delivery
			return nil
		}
		row, e := q.GetAutomationDelivery(ctx, id)
		if e != nil {
			return missing(e, "The comment delivery")
		}
		out, e = automationDelivery(row)
		if e != nil {
			return e
		}
		t, e := getTask(ctx, q, out.TaskID)
		if e != nil {
			return e
		}
		if e = taskManage(ctx, q, t); e != nil {
			return e
		}
		if out.Revision != in.Revision {
			return problem("revision_conflict", "The comment delivery changed. Reload it before continuing.")
		}
		historicalUnknown := false
		for _, attempt := range out.Attempts {
			historicalUnknown = historicalUnknown || attempt.Outcome == "unknown"
		}
		withdrawnReconciliation := out.Withdrawn && out.Status == "withdrawn" && historicalUnknown && action == "reconcile"
		if !withdrawnReconciliation && (out.Withdrawn || (out.Status != "unknown" && out.Status != "failed" && out.Status != "blocked")) {
			return problem("invalid_source", "This delivery is not waiting for recovery.")
		}
		switch action {
		case "reconcile":
			out.Status = "reconcile"
		case "resend":
			if len(out.Attempts) >= maxAutomationDeliveryAttempts || len(out.Receipts) >= maxAutomationDeliveryAttempts {
				return problem("invalid_source", "This comment has reached its limit of 32 dispatch attempts or confirmed receipts. Reconcile its existing delivery evidence instead.")
			}
			out.Status = "pending"
		default:
			return problem("invalid_source", "Choose reconciliation or an explicit resend.")
		}
		if !out.Withdrawn {
			out.Reason = ""
		}
		if e = saveAutomationDelivery(ctx, q, out); e != nil {
			return e
		}
		out.Revision++
		return saveAutomationReceipt(ctx, q, in.RequestID, hash, AutomationReceipt{Kind: "delivery", Delivery: &out})
	})
	return
}
