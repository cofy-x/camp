package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type GitHubDeliveryClaim struct {
	Delivery      orchestration.GitHubDelivery
	CredentialRef string `json:"-"`
}

func githubDeliveryValue(row sqlgen.GithubPublication) (out orchestration.GitHubDelivery, err error) {
	err = json.Unmarshal([]byte(row.Value), &out)
	out.Revision, out.Status = row.Revision, row.Status
	return
}
func saveGitHubDelivery(ctx context.Context, q *sqlgen.Queries, d *orchestration.GitHubDelivery) error {
	raw, e := json.Marshal(d)
	if e != nil {
		return e
	}
	n, e := q.SaveGitHubDelivery(ctx, sqlgen.SaveGitHubDeliveryParams{ID: d.ID, Revision: d.Revision, Status: d.Status, NextCheckAt: d.NextCheckAt, Value: string(raw)})
	if e != nil {
		return e
	}
	if n != 1 {
		return problem("revision_conflict", "The GitHub delivery changed.")
	}
	d.Revision++
	return nil
}
func populateGitHubDelivery(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem) error {
	w.GitHubDelivery = nil
	row, e := q.GitHubDelivery(ctx, w.ID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	d, e := githubDeliveryValue(row)
	if e != nil {
		return e
	}
	w.GitHubDelivery = &d
	return nil
}
func authorizeGitHubDelivery(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, spec *orchestration.GitHubDeliverySpec) error {
	if spec == nil {
		return nil
	}
	if w.CompletionMode != "automatic" || w.PolicyID != "" {
		return problem("invalid_delivery", "GitHub delivery needs a directly authorized Work with automatic completion.")
	}
	row, e := q.GitHubRepository(ctx, spec.ConnectionID)
	if e != nil {
		return missing(e, "The repository connection")
	}
	if row.OwnerUserID != w.OwnerUserID || row.OwnerUserID != actorID(ctx) || row.Enabled == 0 {
		return problem("forbidden", "Choose your own enabled repository connection.")
	}
	if row.Revision != spec.ConnectionRevision {
		return problem("revision_conflict", "The repository connection changed. Review its current settings.")
	}
	connection, e := repositoryValue(row)
	if e != nil {
		return e
	}
	if !validGitHubRef(spec.BaseBranch) {
		return problem("invalid_delivery", "Choose a valid target branch.")
	}
	effects := 2
	switch spec.Mode {
	case "pr":
		if spec.Tag != "" || spec.WaitForDeployment {
			return problem("invalid_delivery", "A pull request cannot publish a release or deployment.")
		}
	case "merge":
		effects = 3
		if spec.Tag != "" {
			return problem("invalid_delivery", "Choose release mode to publish a tag.")
		}
	case "release":
		effects = 4
		if !validGitHubRef(spec.Tag) {
			return problem("invalid_delivery", "Provide an exact valid release tag.")
		}
	default:
		return problem("invalid_delivery", "Choose pull request, merge, or release.")
	}
	if spec.Mode != "pr" && connection.CIWorkflowID < 1 {
		return problem("invalid_delivery", "Configure a required CI workflow before automatic merging.")
	}
	if spec.WaitForDeployment && connection.DeploymentWorkflowID < 1 {
		return problem("invalid_delivery", "Configure the deployment workflow first.")
	}
	if w.Usage.Actions+effects > w.Budget.MaxActions {
		return problem("work_budget_exhausted", "The delivery exceeds this matter's finite external action allowance.")
	}
	room, e := q.GetRoom(ctx, w.RoomID)
	if e != nil {
		return e
	}
	resource, e := q.GetResource(ctx, room.ResourceID)
	if e != nil {
		return e
	}
	d := orchestration.GitHubDelivery{SourceSelection: "gitignore-v1", ID: newID(), WorkItemID: w.ID, RoomID: w.RoomID, Repository: connection.Repository, RepositoryID: connection.RepositoryID, Private: connection.Private, ActorID: connection.ActorID, CIWorkflowID: connection.CIWorkflowID, DeploymentWorkflowID: connection.DeploymentWorkflowID, Spec: *spec, Stage: "result", Status: "waiting_result", Revision: 1, ResourceID: room.ResourceID, BaselineCommit: resource.CurrentCommit, Title: truncateUTF8(strings.Split(w.Goal, "\n")[0], 180), CreatedAt: now()}
	d.Branch = "camp/work-" + d.ID
	raw, _ := json.Marshal(d)
	if e = q.InsertGitHubPublication(ctx, sqlgen.InsertGitHubPublicationParams{ID: d.ID, WorkItemID: w.ID, ConnectionID: connection.ID, Status: d.Status, Value: string(raw)}); e != nil {
		return e
	}
	w.Usage.Actions += effects
	return saveWorkItem(ctx, q, w)
}

func validGitHubRef(ref string) bool {
	if ref == "" || ref == "@" || len(ref) > 200 || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	for _, r := range ref {
		if r <= 32 || r == 127 || strings.ContainsRune("~^:?*[\\", r) {
			return false
		}
	}
	return true
}

func prepareGitHubDelivery(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, task CollaborationTask) error {
	row, e := q.GitHubDelivery(ctx, w.ID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	d, e := githubDeliveryValue(row)
	if e != nil {
		return e
	}
	if d.Status != "waiting_result" {
		return nil
	}
	if task.PublishedCommit == "" || task.ResourceID != d.ResourceID || task.BaseSharedCommit != d.BaselineCommit {
		d.Status = "blocked"
		d.Reason = "Room files changed from the originally authorized delivery baseline. The published result is retained; review its source before external delivery."
		return saveGitHubDelivery(ctx, q, &d)
	}
	d.ResultCommit = task.PublishedCommit
	d.Stage, d.Status = "prepare", "queued"
	d.NextCheckAt = now()
	deadline := time.Now().Add(time.Hour)
	expiry, _ := time.Parse(time.RFC3339Nano, w.ExpiresAt)
	if expiry.Before(deadline) {
		deadline = expiry
	}
	d.Deadline = deadline.UTC().Format(time.RFC3339Nano)
	return saveGitHubDelivery(ctx, q, &d)
}

// Claims serialize one finite step. A crashed visible write is reconciled, never
// resent. Leases only recover read/immutable preparation and read-only evidence.
func (s *Store) ClaimGitHubDelivery(ctx context.Context) (out *GitHubDeliveryClaim, err error) {
	due, err := s.queries().DueGitHubDeliveries(ctx, now())
	if err != nil || len(due) == 0 {
		return nil, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		rows, e := q.DueGitHubDeliveries(ctx, now())
		if e != nil {
			return e
		}
		for _, row := range rows {
			d, e := githubDeliveryValue(row)
			if e != nil {
				return e
			}
			w, e := getWorkItem(ctx, q, d.WorkItemID)
			if e != nil {
				return e
			}
			repo, e := q.GitHubRepository(ctx, d.Spec.ConnectionID)
			if e != nil {
				return e
			}
			owner, e := q.GetUser(ctx, w.OwnerUserID)
			if e != nil {
				return e
			}
			stopped := w.StopRequested || workTerminal(w)
			if !owner.Enabled || ((repo.Enabled == 0 || workExpired(w)) && !d.Reconcile) {
				if d.Reconcile || (d.Status == "working" && githubVisibleStage(d.Stage)) {
					d.Status, d.Reconcile = "unknown", true
				} else {
					d.Status = "blocked"
				}
				d.Reason = "Current repository, owner, or unexpired Work authority is required. Earlier receipts are retained."
				if e = githubDeliveryAttention(ctx, q, &w, &d); e != nil {
					return e
				}
				continue
			}
			if stopped && !d.Reconcile {
				// A dispatched but unconfirmed write stays unknown even after withdrawal.
				if d.Status == "working" && githubVisibleStage(d.Stage) {
					d.Status, d.Reconcile = "unknown", true
					d.Reason = "Delivery was stopped while an external action was unconfirmed. Check its receipt."
				} else {
					d.Status = "stopped"
					d.Reason = "Further delivery was withdrawn."
				}
				if e = saveGitHubDelivery(ctx, q, &d); e != nil {
					return e
				}
				if e = event(ctx, q, "work_item.updated", d.RoomID, d.WorkItemID); e != nil {
					return e
				}
				continue
			}
			deadline, _ := time.Parse(time.RFC3339Nano, d.Deadline)
			if !d.Reconcile && !deadline.IsZero() && time.Now().After(deadline) {
				d.Status = "blocked"
				d.Reason = "The finite delivery deadline ended. Review the recorded result."
				if e = githubDeliveryAttention(ctx, q, &w, &d); e != nil {
					return e
				}
				continue
			}
			if d.Status == "working" && githubVisibleStage(d.Stage) {
				d.Reconcile = true
			}
			current, e := pendingWorkCompletionCurrent(ctx, q, &w)
			if e != nil {
				return e
			}
			if !current && !d.Reconcile {
				d.Status = "blocked"
				d.Reason = "The matter changed after its result was accepted. Review the delivery before further actions."
				if e = githubDeliveryAttention(ctx, q, &w, &d); e != nil {
					return e
				}
				continue
			}
			d.Status = "working"
			d.NextCheckAt = time.Now().Add(3 * time.Minute).UTC().Format(time.RFC3339Nano)
			if e = saveGitHubDelivery(ctx, q, &d); e != nil {
				return e
			}
			out = &GitHubDeliveryClaim{Delivery: d, CredentialRef: repo.CredentialRef}
			return nil
		}
		return nil
	})
	return
}
func githubVisibleStage(stage string) bool {
	return stage == "branch" || stage == "pull_request" || stage == "merge" || stage == "release"
}
func githubDeliveryAttention(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, d *orchestration.GitHubDelivery) error {
	if e := saveGitHubDelivery(ctx, q, d); e != nil {
		return e
	}
	if w.Status == "waiting" {
		w.Status = "needs_attention"
		w.WaitCondition = d.Reason
		if e := workNotice(ctx, q, w, "work_item_attention", d.Reason); e != nil {
			return e
		}
		return saveWorkItem(ctx, q, w)
	}
	return event(ctx, q, "work_item.updated", d.RoomID, d.WorkItemID)
}

func (s *Store) CompleteGitHubDelivery(ctx context.Context, id string, revision int64, result orchestration.GitHubDeliveryResult) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		rawResult, _ := json.Marshal(result)
		receipt, e := q.GitHubDeliveryReceipt(ctx, sqlgen.GitHubDeliveryReceiptParams{DeliveryID: id, Revision: revision})
		if e == nil {
			if receipt != string(rawResult) {
				return problem("request_conflict", "This claim already has another receipt.")
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		row, e := q.GitHubDeliveryByID(ctx, id)
		if e != nil {
			return e
		}
		old, e := githubDeliveryValue(row)
		if e != nil {
			return e
		}
		if row.Revision != revision || row.Status != "working" {
			return problem("revision_conflict", "The delivery claim changed.")
		}
		d := result.Delivery
		d.ID, d.WorkItemID, d.RoomID, d.Revision = old.ID, old.WorkItemID, old.RoomID, old.Revision
		eventID := "github-delivery:" + old.ID + ":" + strconv.FormatInt(revision, 10)
		raw, _ := json.Marshal(result)
		if e = q.RecordGitHubDeliveryReceipt(ctx, sqlgen.RecordGitHubDeliveryReceiptParams{DeliveryID: id, Revision: revision, EventID: eventID, Value: string(raw)}); e != nil {
			return e
		}
		w, e := getWorkItem(ctx, q, d.WorkItemID)
		if e != nil {
			return e
		}
		current, e := pendingWorkCompletionCurrent(ctx, q, &w)
		if e != nil {
			return e
		}
		if !current && d.Status != "unknown" {
			d.Status = "stopped"
			d.Reason = "The receipt was retained; further delivery is withdrawn because its matter changed."
		}
		if d.Status == "blocked" || d.Status == "unknown" {
			return githubDeliveryAttention(ctx, q, &w, &d)
		}
		if e = saveGitHubDelivery(ctx, q, &d); e != nil {
			return e
		}
		if !current {
			return event(ctx, q, "work_item.updated", d.RoomID, d.WorkItemID)
		}
		if d.Status == "waiting" && old.Stage == d.Stage && old.Reason == d.Reason {
			return nil
		}
		id := eventID
		ev := orchestration.SourceEvent{ID: id, RoomID: w.RoomID, SourceID: "action", ThreadKey: "workitem:" + w.ID, WorkItemID: w.ID, CausationID: d.ID, Kind: "action.result", ActorKind: "system", ActorID: "github-delivery", Content: string(raw), OccurredAt: now(), Status: "context"}
		if e = insertWorkFact(ctx, q, &w, ev); e != nil {
			return e
		}
		if d.Status == "succeeded" {
			proposal := w.PendingCompletion
			completed, e := finalizeWorkItem(ctx, q, &w, proposal.Reason, true)
			if e != nil {
				return e
			}
			if completed {
				w.PendingCompletion = nil
				if e = appendWorkDecision(ctx, q, w, orchestration.WorkDecision{Kind: "complete", Reason: proposal.Reason, ActorKind: "system", ActorID: "confirmed-receipts", TaskID: proposal.TaskID, EvidenceIDs: []string{id}}); e != nil {
					return e
				}
			}
		}
		if w.PendingCompletion != nil {
			if e = advanceWorkCompletionFrontier(ctx, q, &w); e != nil {
				return e
			}
		}
		return saveWorkItem(ctx, q, &w)
	})
}

// CheckGitHubDelivery authorizes reads of an uncertain effect, never a resend.
// The request uses the delivery revision as its CAS and is safe to repeat after
// a lost response: only the first request can make this revision runnable.
func (s *Store) CheckGitHubDelivery(ctx context.Context, workID string, revision int64) (out orchestration.WorkItem, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		w, e := getWorkItem(ctx, q, workID)
		if e != nil {
			return e
		}
		if e = workManager(ctx, q, w); e != nil {
			return e
		}
		if w.OwnerUserID != actorID(ctx) {
			return problem("forbidden", "Only the repository connection owner can check this delivery.")
		}
		row, e := q.GitHubDelivery(ctx, w.ID)
		if e != nil {
			return missing(e, "The GitHub delivery")
		}
		d, e := githubDeliveryValue(row)
		if e != nil {
			return e
		}
		if d.Revision != revision {
			return problem("revision_conflict", "The delivery changed. Reload its current receipt.")
		}
		if (d.Status != "unknown" && d.Status != "blocked") || !d.Reconcile || !githubVisibleStage(d.Stage) {
			return problem("invalid_delivery", "Only an unconfirmed external action needs a receipt check.")
		}
		if w.PendingCompletion != nil && !w.StopRequested && !workTerminal(w) {
			originalStatus := w.Status
			w.Status = "waiting"
			current, e := pendingWorkCompletionCurrent(ctx, q, &w)
			if e != nil {
				return e
			}
			if current {
				if e = saveWorkItem(ctx, q, &w); e != nil {
					return e
				}
			} else {
				w.Status = originalStatus
			}
		}
		d.Status = "queued"
		d.NextCheckAt = now()
		if e = saveGitHubDelivery(ctx, q, &d); e != nil {
			return e
		}
		out, e = workItemDetails(ctx, q, w)
		return e
	})
	return
}

func withdrawGitHubDelivery(ctx context.Context, q *sqlgen.Queries, workID string) error {
	row, e := q.GitHubDelivery(ctx, workID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	d, e := githubDeliveryValue(row)
	if e != nil {
		return e
	}
	if d.Status == "working" || d.Status == "succeeded" || d.Status == "stopped" || d.Reconcile {
		return nil
	}
	d.Status = "stopped"
	d.Reason = "Further delivery was withdrawn with its matter."
	return saveGitHubDelivery(ctx, q, &d)
}

func retryGitHubDelivery(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, revision int64) error {
	if w.OwnerUserID != actorID(ctx) {
		return problem("forbidden", "Only the repository connection owner can resume this delivery.")
	}
	row, e := q.GitHubDelivery(ctx, w.ID)
	if e != nil {
		return missing(e, "The GitHub delivery")
	}
	d, e := githubDeliveryValue(row)
	if e != nil {
		return e
	}
	if revision != d.Revision {
		return problem("revision_conflict", "The delivery changed. Review its current outcome.")
	}
	if d.Status != "blocked" || d.Reconcile || d.Stage == "result" {
		return problem("invalid_delivery", "Only a known failed action can be resumed. Check unconfirmed receipts without resending.")
	}
	w.Status = "waiting"
	current, e := pendingWorkCompletionCurrent(ctx, q, w)
	if e != nil {
		return e
	}
	if !current {
		return problem("invalid_delivery", "The accepted result or its Work context changed. Authorize fresh work instead of replaying an old delivery.")
	}
	if githubVisibleStage(d.Stage) {
		if w.Usage.Actions >= w.Budget.MaxActions {
			return problem("work_budget_exhausted", "This retry needs one remaining external action.")
		}
		w.Usage.Actions++
	}
	d.Status, d.Reason = "queued", ""
	d.Checks = 0
	d.NextCheckAt = now()
	deadline := time.Now().Add(time.Hour)
	expiry, _ := time.Parse(time.RFC3339Nano, w.ExpiresAt)
	if expiry.Before(deadline) {
		deadline = expiry
	}
	d.Deadline = deadline.UTC().Format(time.RFC3339Nano)
	w.WaitCondition = "Waiting for confirmed GitHub delivery receipts."
	return saveGitHubDelivery(ctx, q, &d)
}
