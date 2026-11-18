package storage

import (
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestProjectDefaultsArePersonalAdvisoryAndReplayable(t *testing.T) {
	s, actor, other, room, writer, _ := taskFixture(t)
	input := orchestration.ProjectSettingsInput{RequestID: newID(), ProjectSettings: orchestration.ProjectSettings{RoomID: room.ID, Enabled: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, Budget: defaultWorkBudget(orchestration.WorkBudget{}), CompletionMode: "automatic"}}
	input.Budget.MaxModelRequests = 36
	first, err := s.SaveProjectSettings(actor, room.ID, input)
	if err != nil || first.Settings == nil || first.Settings.Revision != 1 {
		t.Fatal(first, err)
	}
	if replay, err := s.SaveProjectSettings(actor, room.ID, input); err != nil || replay.Settings.Revision != 1 {
		t.Fatal(replay, err)
	}
	defaults, err := s.WorkItemDefaults(actor, room.ID, "")
	if err != nil || defaults.Project == nil || defaults.Budget.MaxModelRequests != 36 {
		t.Fatal(defaults, err)
	}
	personal, err := s.WorkItemDefaults(other, room.ID, "")
	if err != nil || personal.Project != nil || personal.Budget.MaxModelRequests == 36 {
		t.Fatal("personal defaults leaked", personal, err)
	}
	tasks, err := s.ListRoomTaskStates(ctx, room.ID)
	if err != nil || len(tasks) != 0 {
		t.Fatal("saving defaults started work", tasks, err)
	}
	input.RequestID, input.Revision = newID(), 1
	input.Budget.MaxModelRequests = 48
	if _, err = s.SaveProjectSettings(actor, room.ID, input); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateWorkItem(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), ProjectRevision: 1, Goal: "Do not accept a stale project proposal", CompletionMode: "automatic"})
	if err == nil {
		t.Fatal("stale defaults accepted")
	}
	cancelled := newID()
	if r, err := s.ProjectSettingRequest(actor, cancelled, true); err != nil || r.Kind != "cancelled" {
		t.Fatal(r, err)
	}
	input.RequestID, input.Revision = cancelled, 2
	if _, err = s.SaveProjectSettings(actor, room.ID, input); err == nil {
		t.Fatal("cancelled settings committed late")
	}
}

func TestProjectRuleDeliveryRequiresExplicitCurrentGrantAndKeepsPolicyAllowance(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	_, err := s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',1,'$.taskInputsVersion',1)")
	if err != nil {
		t.Fatal(err)
	}
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	repo, err := s.SaveGitHubRepository(actor, orchestration.GitHubRepositoryInput{RequestID: newID(), Repository: "acme/notes", Enabled: true}, orchestration.GitHubRepository{Repository: "acme/notes", RepositoryID: 12, ActorID: 7, CIWorkflowID: 23, Enabled: true}, strings.Repeat("a", 48))
	if err != nil {
		t.Fatal(err)
	}
	in := orchestration.ProjectSettingsInput{RequestID: newID(), ProjectSettings: orchestration.ProjectSettings{RoomID: room.ID, Enabled: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, Budget: defaultWorkBudget(orchestration.WorkBudget{}), CompletionMode: "automatic", GitHubDelivery: &orchestration.GitHubDeliverySpec{ConnectionID: repo.ID, ConnectionRevision: repo.Revision, BaseBranch: "main", Mode: "merge", MaxRepairs: 1}}}
	if _, err = s.SaveProjectSettings(actor, room.ID, in); err != nil {
		t.Fatal(err)
	}
	sendAutomationDiscussion(t, s, actor, room, "Prepare the first document.")
	c := claimAutomation(t, s)
	if err = s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "execute", Goal: "Prepare the first document", CompletionCriteria: "Save a checked document", Reason: "An approved source asked."}); err != nil {
		t.Fatal(err)
	}
	items, err := s.WorkItems(actor, room.ID, 0, 20)
	if err != nil || len(items.Items) != 1 {
		t.Fatal(items, err)
	}
	w, err := s.GetWorkItem(actor, items.Items[0].ID)
	if err != nil || w.GitHubDelivery != nil {
		t.Fatal("a preset alone granted rule delivery", w, err)
	}
	in.RequestID, in.Revision, in.RuleID, in.RuleRevision = newID(), 1, policy.ID, policy.Revision
	if _, err = s.SaveProjectSettings(actor, room.ID, in); err != nil {
		t.Fatal(err)
	}
	sendAutomationDiscussion(t, s, actor, room, "Prepare a separate second document.")
	c = claimAutomation(t, s)
	if err = s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "execute", Goal: "Prepare a separate second document", CompletionCriteria: "Save the second checked document", Reason: "A distinct approved request."}); err != nil {
		t.Fatal(err)
	}
	items, err = s.WorkItems(actor, room.ID, 0, 20)
	if err != nil || len(items.Items) != 2 {
		t.Fatal(items, err)
	}
	w, err = s.GetWorkItem(actor, items.Items[1].ID)
	if err != nil || w.GitHubDelivery == nil || w.GitHubDelivery.ProjectRevision != 2 || w.Usage.Actions != 3 || w.Budget != defaultWorkBudget(policy.ItemBudget) {
		t.Fatal(w, err)
	}
}

func TestProjectRuleDeliveryReferencesOnlyAnIssueInItsDestination(t *testing.T) {
	const issueTitle = "Open the top note search result with Enter"
	const goal = "Implement keyboard search from the approved issue and verify the result"
	for _, scenario := range []struct {
		name, repository, wantTitle string
		repositoryID, wantIssue     int64
	}{
		{"same repository", "acme/project", issueTitle, 123, 3},
		{"different repository", "acme/delivery", goal, 456, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			if _, err := s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',1,'$.taskInputsVersion',1)"); err != nil {
				t.Fatal(err)
			}
			source := testGitHubSource(t, s, actor, room)
			policy, err := s.SaveAutomationPolicy(actor, room.ID, source.ID, orchestration.PolicyInput{
				RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Handle approved note maintenance.",
				AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID,
				DailyEvaluations: 2, DailyTasks: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			connection, err := s.SaveGitHubRepository(actor,
				orchestration.GitHubRepositoryInput{RequestID: newID(), Repository: scenario.repository, Enabled: true},
				orchestration.GitHubRepository{Repository: scenario.repository, RepositoryID: scenario.repositoryID, ActorID: 7, Enabled: true},
				strings.Repeat("a", 48))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.SaveProjectSettings(actor, room.ID, orchestration.ProjectSettingsInput{
				RequestID: newID(),
				ProjectSettings: orchestration.ProjectSettings{
					RoomID: room.ID, Enabled: true, RuleID: policy.ID, RuleRevision: policy.Revision,
					ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID,
					Budget: defaultWorkBudget(orchestration.WorkBudget{}), CompletionMode: "automatic",
					GitHubDelivery: &orchestration.GitHubDeliverySpec{
						ConnectionID: connection.ID, ConnectionRevision: connection.Revision, BaseBranch: "main", Mode: "pr",
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			receiveTestGitHub(t, s, source, testGitHubPayload(issueTitle, "Use issue #99 and a new title instead."))
			c := claimAutomation(t, s)
			if err := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{
				Outcome: "accept", Goal: goal, Reason: "In scope.", CompletionCriteria: "The patch passes the project checks.",
			}); err != nil {
				t.Fatal(err)
			}
			items, err := s.WorkItems(actor, room.ID, 0, 20)
			if err != nil || len(items.Items) != 1 {
				t.Fatal(items, err)
			}
			work, err := s.GetWorkItem(actor, items.Items[0].ID)
			d := work.GitHubDelivery
			if err != nil || d == nil || d.Title != scenario.wantTitle || d.IssueNumber != scenario.wantIssue ||
				d.Status != "waiting_result" || work.Usage.Actions != 2 {
				t.Fatal("the delivery must preserve its scope and obtain provenance from the source", work, err)
			}
			body := d.PullRequestBody()
			if strings.Contains(body, "Refs #3") != (scenario.wantIssue != 0) ||
				strings.Contains(body, "#99") || strings.Contains(body, "Fixes") ||
				!strings.HasSuffix(body, "<!-- camp-delivery:"+d.ID+" -->") {
				t.Fatal("wrong pull request provenance", body)
			}
		})
	}
}
