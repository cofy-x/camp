package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func deliveryFixture(t *testing.T, repairs ...int) (*Store, context.Context, orchestration.WorkItem, orchestration.GitHubRepository) {
	t.Helper()
	s, actor, _, room, writer, _ := taskFixture(t)
	if _, e := s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',1,'$.taskInputsVersion',1)"); e != nil {
		t.Fatal(e)
	}
	input := orchestration.GitHubRepositoryInput{RequestID: newID(), Repository: "acme/notes", Token: "a-private-repository-token", Enabled: true, CIWorkflow: "ci.yml"}
	repo, e := s.SaveGitHubRepository(actor, input, orchestration.GitHubRepository{Repository: "acme/notes", RepositoryID: 12, ActorID: 7, DefaultBranch: "main", CIWorkflowID: 23, CIWorkflow: "ci.yml", Enabled: true}, strings.Repeat("a", 48))
	if e != nil {
		t.Fatal(e)
	}
	replay, found, e := s.FindGitHubRepositoryRequest(actor, input)
	if e != nil || !found || replay.ID != repo.ID {
		t.Fatal(replay, found, e)
	}
	maxRepairs := 0
	if len(repairs) > 0 {
		maxRepairs = repairs[0]
	}
	w, e := s.CreateWorkItemWithBudget(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Improve the release notes", Start: true, CompletionMode: "automatic", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, GitHubDelivery: &orchestration.GitHubDeliverySpec{ConnectionID: repo.ID, ConnectionRevision: repo.Revision, BaseBranch: "main", Mode: "release", Tag: "v0.1.1", MaxRepairs: maxRepairs}}, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.GetTaskState(ctx, w.ActiveTaskID)
	if e != nil {
		t.Fatal(e)
	}
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "Verified notes saved.", WorkItemCompletion: &WorkItemCompletion{CriteriaMet: true, Reason: "The checked release note is ready."}})
	if e = s.ReconcileTask(ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	w, e = s.GetWorkItem(actor, w.ID)
	if e != nil || w.Status != "waiting" || w.PendingCompletion == nil || w.GitHubDelivery == nil || w.GitHubDelivery.Status != "queued" {
		t.Fatal(w, e)
	}
	return s, actor, w, repo
}
func claimDelivery(t *testing.T, s *Store) *GitHubDeliveryClaim {
	t.Helper()
	claim, e := s.ClaimGitHubDelivery(ctx)
	if e != nil || claim == nil {
		t.Fatal(claim, e)
	}
	return claim
}
func completeDeliveryStage(t *testing.T, s *Store, claim *GitHubDeliveryClaim, stage, status string) {
	t.Helper()
	d := claim.Delivery
	d.Stage, d.Status = stage, status
	d.NextCheckAt = now()
	result := orchestration.GitHubDeliveryResult{Delivery: d}
	if e := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, result); e != nil {
		t.Fatal(e)
	}
	if e := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, result); e != nil {
		t.Fatal("receipt replay did not resolve a lost database response", e)
	}
}
func TestGitHubDeliveryCompletesOnlyAfterEveryConfirmedEffect(t *testing.T) {
	s, actor, w, _ := deliveryFixture(t)
	for _, stage := range []string{"branch", "pull_request", "checks", "merge", "release"} {
		claim := claimDelivery(t, s)
		other, e := s.ClaimGitHubDelivery(ctx)
		if e != nil || other != nil {
			t.Fatal("one effect had concurrent owners", other, e)
		}
		completeDeliveryStage(t, s, claim, stage, "queued")
		w, _ = s.GetWorkItem(actor, w.ID)
		if w.Status != "waiting" || w.PendingCompletion == nil {
			t.Fatal("intermediate receipt completed the matter", w)
		}
	}
	claim := claimDelivery(t, s)
	completeDeliveryStage(t, s, claim, "release", "succeeded")
	w, e := s.GetWorkItem(actor, w.ID)
	if e != nil || w.Status != "completed" || w.PendingCompletion != nil || w.GitHubDelivery.Status != "succeeded" || w.Usage.Actions != 4 {
		t.Fatal(w, e)
	}
	assertScalar(t, s, "SELECT count(*) FROM github_delivery_receipts", 6)
	assertScalar(t, s, "SELECT count(*) FROM work_item_evidence WHERE kind='action.result'", 6)
}
func TestGitHubDeliveryStopPreservesAnUnknownEffectAndItsAttention(t *testing.T) {
	s, actor, w, _ := deliveryFixture(t)
	completeDeliveryStage(t, s, claimDelivery(t, s), "branch", "queued")
	claim := claimDelivery(t, s)
	w, _ = s.GetWorkItem(actor, w.ID)
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "stop"})
	d := claim.Delivery
	d.Status, d.Reconcile, d.Reason = "unknown", true, "The branch response was lost."
	if e := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, orchestration.GitHubDeliveryResult{Delivery: d, Effect: "branch", Outcome: "unknown"}); e != nil {
		t.Fatal(e)
	}
	w, _ = s.GetWorkItem(actor, w.ID)
	if w.Status != "cancelled" || w.GitHubDelivery.Status != "unknown" {
		t.Fatal(w)
	}
	page := attentionFor(t, s, actor)
	if page.Total != 1 || page.Items[0].Target.WorkItemID != w.ID {
		t.Fatal("stop hid an unresolved external effect", page)
	}
	if _, e := s.CheckGitHubDelivery(actor, w.ID, w.GitHubDelivery.Revision); e != nil {
		t.Fatal(e)
	}
	if attentionFor(t, s, actor).Total != 1 {
		t.Fatal("checking a receipt was treated as resolution")
	}
	check := claimDelivery(t, s)
	if !check.Delivery.Reconcile {
		t.Fatal("checking authorized another write", check)
	}
	completeDeliveryStage(t, s, check, "pull_request", "queued")
	w, _ = s.GetWorkItem(actor, w.ID)
	if w.GitHubDelivery.Status != "stopped" || attentionFor(t, s, actor).Total != 0 {
		t.Fatal(w)
	}
	next, e := s.ClaimGitHubDelivery(ctx)
	if e != nil || next != nil {
		t.Fatal("a stopped delivery continued", next, e)
	}
}
func TestGitHubDeliveryRecoversCrashedWritesAsReadOnlyClaims(t *testing.T) {
	s, _, _, _ := deliveryFixture(t)
	completeDeliveryStage(t, s, claimDelivery(t, s), "branch", "queued")
	claim := claimDelivery(t, s)
	if _, e := s.db.Exec("UPDATE github_publications SET next_check_at=? WHERE id=?", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), claim.Delivery.ID); e != nil {
		t.Fatal(e)
	}
	recovered := claimDelivery(t, s)
	if !recovered.Delivery.Reconcile || recovered.Delivery.ID != claim.Delivery.ID || recovered.Delivery.Spec != claim.Delivery.Spec {
		t.Fatal(recovered)
	}
}
func TestGitHubConnectionRequestCancellationFencesLateCredentialActivation(t *testing.T) {
	s, actor, _, _, _, _ := taskFixture(t)
	input := orchestration.GitHubRepositoryInput{RequestID: newID(), Repository: "acme/notes", Token: "never-save-this-token", Enabled: true}
	if value, e := s.CancelGitHubRepositoryRequest(actor, input.RequestID); e != nil || value != nil {
		t.Fatal(value, e)
	}
	_, e := s.SaveGitHubRepository(actor, input, orchestration.GitHubRepository{Repository: input.Repository, RepositoryID: 12, ActorID: 7}, strings.Repeat("a", 48))
	requireCode(t, e, "request_cancelled")
	assertScalar(t, s, "SELECT count(*) FROM github_repositories", 0)
	rows, _ := s.GitHubRepositories(actor)
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), input.Token) {
		t.Fatal("credential leaked")
	}
}

func TestGitHubDeliveryKnownFailureCanResumeOnlyUnchangedWorkAndConsumesAFiniteRetry(t *testing.T) {
	s, actor, w, _ := deliveryFixture(t)
	completeDeliveryStage(t, s, claimDelivery(t, s), "branch", "queued")
	claim := claimDelivery(t, s)
	d := claim.Delivery
	d.Status, d.Reason = "blocked", "Permission was rejected."
	if e := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, orchestration.GitHubDeliveryResult{Delivery: d}); e != nil {
		t.Fatal(e)
	}
	w, _ = s.GetWorkItem(actor, w.ID)
	if w.Status != "needs_attention" {
		t.Fatal(w)
	}
	input := orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "retry_delivery", DeliveryRevision: w.GitHubDelivery.Revision}
	resumed, e := s.OperateWorkItem(actor, w.ID, input, roomBudget)
	if e != nil || resumed.Status != "waiting" || resumed.Usage.Actions != 5 {
		t.Fatal(resumed, e)
	}
	replay, e := s.OperateWorkItem(actor, w.ID, input, roomBudget)
	if e != nil || replay.Usage.Actions != 5 {
		t.Fatal("lost response spent another retry", replay, e)
	}
	next := claimDelivery(t, s)
	if next.Delivery.Reconcile || next.Delivery.Stage != "branch" {
		t.Fatal(next)
	}
}

func TestGitHubWaitingPollsDoNotRefreshBusinessHistory(t *testing.T) {
	s, actor, w, _ := deliveryFixture(t)
	completeDeliveryStage(t, s, claimDelivery(t, s), "checks", "queued")
	claim := claimDelivery(t, s)
	d := claim.Delivery
	d.Status, d.Reason = "waiting", "Waiting for CI."
	d.NextCheckAt = now()
	d.Checks++
	if e := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, orchestration.GitHubDeliveryResult{Delivery: d}); e != nil {
		t.Fatal(e)
	}
	before, _ := s.GetWorkItem(actor, w.ID)
	claim = claimDelivery(t, s)
	d = claim.Delivery
	d.Status = "waiting"
	d.NextCheckAt = now()
	d.Checks++
	if e := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, orchestration.GitHubDeliveryResult{Delivery: d}); e != nil {
		t.Fatal(e)
	}
	after, _ := s.GetWorkItem(actor, w.ID)
	if before.Revision != after.Revision || len(before.Evidence) != len(after.Evidence) {
		t.Fatal("unchanged CI observations refreshed the matter", before.Revision, after.Revision)
	}
}
