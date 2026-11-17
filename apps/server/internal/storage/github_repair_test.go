package storage

import (
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func failDeliveryCI(t *testing.T, s *Store, claim *GitHubDeliveryClaim, budgets ...Budget) orchestration.GitHubDeliveryResult {
	t.Helper()
	d := claim.Delivery
	d.Stage, d.Status, d.FailureCode, d.Reason, d.Diagnostics = "checks", "blocked", "ci_failed", "CI failed for this head.", "Expected a nonempty title."
	d.HeadSHA, d.PullRequest = strings.Repeat("e", 40), 12
	r := orchestration.GitHubDeliveryResult{Delivery: d}
	budget := roomBudget
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	if err := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, r, budget); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGitHubRepairRetainsOneMatterPRSourceLedgerAndFiniteAllowance(t *testing.T) {
	s, actor, original, _ := deliveryFixture(t, 1)
	claim := claimDelivery(t, s)
	r := failDeliveryCI(t, s, claim)
	if err := s.CompleteGitHubDelivery(ctx, claim.Delivery.ID, claim.Delivery.Revision, r, roomBudget); err != nil {
		t.Fatal("lost receipt replay", err)
	}
	w, err := s.GetWorkItem(actor, original.ID)
	if err != nil || w.Status != "running" || w.ActiveTaskID == "" || w.Usage.Tasks != 2 || w.Usage.Actions != 5 || w.GitHubDelivery.Status != "repairing" || w.GitHubDelivery.Repairs != 1 {
		t.Fatal(w, err)
	}
	if next, err := s.ClaimGitHubDelivery(ctx); err != nil || next != nil {
		t.Fatal("repair must wait for accepted task output", next, err)
	}
	task, err := s.GetTaskState(ctx, w.ActiveTaskID)
	if err != nil || task.BaseSharedCommit != original.GitHubDelivery.ResultCommit || task.RequestedByUserID != original.OwnerUserID {
		t.Fatal(task, err)
	}
	origin, err := s.queries().GetMessage(ctx, task.OriginMessageID)
	if err != nil || origin.AuthorKind != "system" {
		t.Fatal("automatic repair was attributed to a person", origin, err)
	}
	finishTaskTestRound(t, s, task, strings.Repeat("c", 40), CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "Fixed and checked the accepted result.", WorkItemCompletion: &WorkItemCompletion{CriteriaMet: true, Reason: "The corrected result satisfies the original goal."}})
	if err = s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	d := w.GitHubDelivery
	if err != nil || w.Status != "waiting" || d.Status != "queued" || d.PullRequest != 12 || d.ID != original.GitHubDelivery.ID || d.BaselineCommit != strings.Repeat("b", 40) || d.ResultCommit != strings.Repeat("c", 40) || d.InitialResultCommit != strings.Repeat("b", 40) || d.PreviousHeadSHA != strings.Repeat("e", 40) {
		t.Fatal(w, err)
	}
	var sources int
	if err = s.db.QueryRow("SELECT count(*) FROM github_delivery_sources WHERE delivery_id=?", d.ID).Scan(&sources); err != nil || sources != 2 {
		t.Fatal(sources, err)
	}
	if _, err = s.db.Exec("DELETE FROM github_delivery_sources WHERE delivery_id=?", d.ID); err == nil {
		t.Fatal("accepted source history was mutable")
	}
	claim = claimDelivery(t, s)
	failDeliveryCI(t, s, claim)
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Status != "needs_attention" || w.ActiveTaskID != "" || w.Usage.Tasks != 2 || w.GitHubDelivery.FailureCode != "repair_limit" {
		t.Fatal(w, err)
	}
	request := orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "repair_delivery", DeliveryRevision: w.GitHubDelivery.Revision}
	continued, err := s.OperateWorkItem(actor, w.ID, request, roomBudget)
	if err != nil || continued.ActiveTaskID == "" || continued.Usage.Tasks != 3 || continued.Usage.Actions != 6 || continued.GitHubDelivery.Repairs != 2 || continued.GitHubDelivery.Spec.MaxRepairs != 2 {
		t.Fatal("explicit finite recovery failed", continued, err)
	}
	if replay, err := s.OperateWorkItem(actor, w.ID, request, roomBudget); err != nil || replay.ActiveTaskID != continued.ActiveTaskID || replay.Usage.Tasks != 3 {
		t.Fatal("uncertain recovery created another task", replay, err)
	}
}

func TestGitHubRepairRechecksSourceScopeBudgetAndUnknownEffects(t *testing.T) {
	for _, change := range []string{"source", "agent", "budget", "unknown"} {
		t.Run(change, func(t *testing.T) {
			s, actor, w, _ := deliveryFixture(t, 1)
			claim := claimDelivery(t, s)
			switch change {
			case "source":
				_, _ = s.db.Exec("UPDATE resources SET current_commit=? WHERE id=?", strings.Repeat("f", 40), w.GitHubDelivery.ResourceID)
			case "agent":
				_, _ = s.db.Exec("UPDATE agents SET access='read',revision=revision+1 WHERE id=?", w.GitHubDelivery.ParticipantAgentIDs[0])
			case "budget":
				_, _ = s.db.Exec("UPDATE work_items SET value=json_set(value,'$.budget.maxTasks',1) WHERE id=?", w.ID)
			}
			if change == "unknown" {
				d := claim.Delivery
				d.Status, d.Reconcile, d.FailureCode = "unknown", true, "ci_failed"
				if err := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, orchestration.GitHubDeliveryResult{Delivery: d}, roomBudget); err != nil {
					t.Fatal(err)
				}
			} else {
				failDeliveryCI(t, s, claim)
			}
			got, err := s.GetWorkItem(actor, w.ID)
			if err != nil || got.Usage.Tasks != 1 || got.ActiveTaskID != "" || got.GitHubDelivery.Repairs != 0 {
				t.Fatal(got, err)
			}
			if change == "source" && got.GitHubDelivery.FailureCode != "source_changed" {
				t.Fatal(got.GitHubDelivery)
			}
		})
	}
}

func TestGitHubRepairDoesNotTreatCosmeticAgentChangesAsNewAuthority(t *testing.T) {
	s, actor, w, _ := deliveryFixture(t, 1)
	claim := claimDelivery(t, s)
	if _, err := s.db.Exec("UPDATE agents SET name='Renamed writer',revision=revision+1 WHERE id=?", w.GitHubDelivery.ParticipantAgentIDs[0]); err != nil {
		t.Fatal(err)
	}
	larger := roomBudget
	larger.MaxTokens *= 2
	larger.MaxSteps *= 2
	larger.TimeoutSeconds *= 2
	failDeliveryCI(t, s, claim, larger)
	got, err := s.GetWorkItem(actor, w.ID)
	if err != nil || got.ActiveTaskID == "" || got.Usage.Tasks != 2 || got.GitHubDelivery.Repairs != 1 {
		t.Fatal(got, err)
	}
	task, err := s.GetTaskState(ctx, got.ActiveTaskID)
	if err != nil || task.Budget.MaxTokens > roomBudget.MaxTokens || task.Budget.MaxSteps > roomBudget.MaxSteps || task.Budget.TimeoutSeconds > roomBudget.TimeoutSeconds {
		t.Fatal("repair enlarged the frozen per-request allowance", task.Budget, err)
	}
}
