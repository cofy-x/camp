package storage

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestPublicationEchoDoesNotCreateAnotherAutomaticRequest(t *testing.T) {
	s, actor, w, _ := deliveryFixture(t)
	claim := claimDelivery(t, s)
	d := claim.Delivery
	d.HeadSHA = strings.Repeat("e", 40)
	d.PullRequest = 12
	d.Stage, d.Status = "merge", "queued"
	d.NextCheckAt = now()
	if e := s.CompleteGitHubDelivery(ctx, d.ID, d.Revision, orchestration.GitHubDeliveryResult{Delivery: d}); e != nil {
		t.Fatal(e)
	}
	claim = claimDelivery(t, s)
	d = claim.Delivery
	source, e := s.CreateGitHubSource(actor, w.RoomID, CreateGitHubSourceInput{RequestID: newID(), Repository: d.Repository, Secret: strings.Repeat("s", 32)}, strings.Repeat("c", 48))
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.GetTaskState(ctx, w.Execution.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveAutomationPolicy(actor, w.RoomID, source.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Review new external requests.", AllowedActorIDs: []string{"7", "8"}, ParticipantAgentIDs: []string{task.CurrentAgentID}, FirstAgentID: task.CurrentAgentID, DailyEvaluations: 10, DailyTasks: 3}); e != nil {
		t.Fatal(e)
	}
	for _, sample := range []struct {
		name, action, body, sha string
		actor                   int64
		merged, own             bool
	}{
		{"our-open", "opened", d.PullRequestBody(), d.HeadSHA, 7, false, true},
		{"our-merge-before-response", "closed", d.PullRequestBody(), d.HeadSHA, 7, true, true},
		{"another-actor", "opened", d.PullRequestBody(), d.HeadSHA, 8, false, false},
		{"another-head", "opened", d.PullRequestBody(), strings.Repeat("f", 40), 7, false, false},
		{"new-human-edit", "edited", d.PullRequestBody(), d.HeadSHA, 7, false, false},
		{"new-human-request", "opened", "Please add another feature.\n" + d.PullRequestBody(), d.HeadSHA, 7, false, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"action": sample.action, "repository": map[string]any{"id": d.RepositoryID, "full_name": d.Repository}, "sender": map[string]any{"id": sample.actor, "login": "person", "type": "User"}, "pull_request": map[string]any{"id": 120, "number": 12, "title": d.Title, "body": sample.body, "merged": sample.merged, "merge_commit_sha": strings.Repeat("f", 40), "head": map[string]any{"sha": sample.sha, "ref": d.Branch, "repo": map[string]any{"id": d.RepositoryID}}, "base": map[string]any{"ref": d.Spec.BaseBranch}}})
			event, _, e := s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, "aaaaaaaa-bbbb-cccc-dddd-"+newID()[:12], "pull_request", raw)
			if e != nil {
				t.Fatal(e)
			}
			row, e := s.queries().GetAutomationEvent(ctx, "github:"+event.ID)
			if e != nil {
				t.Fatal(e)
			}
			value, e := automationEvent(row)
			if e != nil {
				t.Fatal(e)
			}
			if sample.own && (value.Status != "ignored" || value.Reason != "own_output") {
				t.Fatal("publication spawned its own request", value)
			}
			if !sample.own && (value.Status != "pending" || value.Reason == "own_output") {
				t.Fatal("new external evidence was suppressed", value)
			}
		})
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
}
