package storage

import (
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestGitHubVerificationUsesSavedRuleAndDeliveryWithoutAdmission(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, admin, room)
	older := receiveTestGitHub(t, s, source, testGitHubPayload("Previous delivery", "Earlier evidence"))
	check := func() GitHubSourceVerification {
		t.Helper()
		out, err := s.VerifyGitHubSource(admin, room.ID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	input := orchestration.PolicyInput{
		RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Handle approved issues.",
		AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID,
		DailyEvaluations: 2, DailyTasks: 1,
	}
	policy, err := s.SaveAutomationPolicy(admin, room.ID, source.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if out := check(); out.Mode != "auto" || out.RuleRevision != policy.Revision || out.LastReceivedAt != older.ReceivedAt ||
		out.Sample == nil || out.Sample.Outcome != "before_rule" ||
		out.RunnerIssue != nil || out.Delivery == nil || out.Delivery.State != "room_files" {
		t.Fatalf("unexpected saved rule check: %+v", out)
	}
	disallowed := strings.Replace(string(testGitHubPayload("Issue from another account", "More evidence")), `"id":7`, `"id":8`, 1)
	receiveTestGitHub(t, s, source, []byte(disallowed))
	if sample := check().Sample; sample == nil || sample.Outcome != "sender_not_allowed" || sample.ActorID != "8" {
		t.Fatal(sample)
	}
	receiveTestGitHub(t, s, source, testGitHubPayload("Issue from the allowed account", "More evidence"))
	if sample := check().Sample; sample == nil || sample.Outcome != "eligible" || sample.Kind != "issues.opened" || sample.ActorID != "7" {
		t.Fatal(sample)
	}
	if _, err := s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',1,'$.taskInputsVersion',1)"); err != nil {
		t.Fatal(err)
	}
	repo, err := s.SaveGitHubRepository(admin,
		orchestration.GitHubRepositoryInput{RequestID: newID(), Repository: "acme/output", Enabled: true},
		orchestration.GitHubRepository{Repository: "acme/output", RepositoryID: 12, ActorID: 7, CIWorkflowID: 23, CIWorkflow: "ci.yml", Enabled: true},
		strings.Repeat("a", 48),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveProjectSettings(admin, room.ID, orchestration.ProjectSettingsInput{
		RequestID: newID(),
		ProjectSettings: orchestration.ProjectSettings{
			RoomID: room.ID, Enabled: true, RuleID: policy.ID, RuleRevision: policy.Revision,
			ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID,
			Budget: defaultWorkBudget(orchestration.WorkBudget{}), CompletionMode: "automatic",
			GitHubDelivery: &orchestration.GitHubDeliverySpec{
				ConnectionID: repo.ID, ConnectionRevision: repo.Revision, BaseBranch: "main", Mode: "merge",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := check().Delivery; d == nil || d.State != "saved" || d.Repository != "acme/output" || d.Branch != "main" || d.CIWorkflow != "ci.yml" {
		t.Fatal(d)
	}
	input.RequestID, input.Revision = newID(), policy.Revision
	if _, err = s.SaveAutomationPolicy(admin, room.ID, source.ID, input); err != nil {
		t.Fatal(err)
	}
	if d := check().Delivery; d == nil || d.State != "stale" || d.Repository != "" {
		t.Fatal(d)
	}
	if _, err := s.db.Exec("UPDATE runners SET enabled=0"); err != nil {
		t.Fatal(err)
	}
	if issue := check().RunnerIssue; issue == nil || issue.Code != "runner_unavailable" {
		t.Fatal(issue)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_usage", 0)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assertScalar(t, s, "SELECT count(*) FROM github_deliveries", 3)
}
