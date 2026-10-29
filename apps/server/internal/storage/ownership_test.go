package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func personalRunner(t *testing.T, s *Store, actor context.Context, name string) (Runner, JoinRunnerInput) {
	t.Helper()
	_, token, err := s.CreateRunnerEnrollment(actor, name, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	in := JoinRunnerInput{Token: token, RegistrationID: "register-" + name, Credential: strings.Repeat(name, 40), StorageID: "store-" + name}
	runner, err := s.JoinRunner(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	runner, err = s.ConnectRunner(ctx, runner.ID, RunnerConnection{StorageID: in.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true}`), Sources: []RunnerSource{{ID: "project", Alias: "Project", Revision: "v1", Available: true}}})
	if err != nil {
		t.Fatal(err)
	}
	return runner, in
}
func ownedAgent(t *testing.T, s *Store, actor context.Context, room Room, handle string) Agent {
	t.Helper()
	agent, err := s.CreateRoomAgent(actor, room.ID, Agent{Name: handle, Handle: handle, Access: "write", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return agent
}
func TestOwnedRunnerDefaultsAndAgentRoutesStayExplicit(t *testing.T) {
	s, room, teamAgent := roomFixture(t)
	admin, member := teamUsers(t, s)
	person, _ := UserFromContext(member)
	a, _ := personalRunner(t, s, member, "alpha")
	b, _ := personalRunner(t, s, member, "bravo")
	if a.OwnerUserID != person.ID || a.TeamShared {
		t.Fatal("personal registration ownership", a)
	}
	defaults, err := s.GetExecutionDefaults(member)
	if err != nil || defaults.PersonalRunnerID != a.ID {
		t.Fatal("later registration replaced default", defaults, err)
	}
	personal := ownedAgent(t, s, member, room, "personal")
	if personal.OwnerUserID != person.ID || personal.PreferredRunnerID != a.ID {
		t.Fatal("Agent did not snapshot default", personal)
	}
	defaults, err = s.SetExecutionDefault(member, ExecutionDefaultInput{Scope: "personal", RunnerID: b.ID, Revision: defaults.PersonalRevision})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetExecutionDefault(member, ExecutionDefaultInput{Scope: "personal", RunnerID: a.ID, Revision: defaults.PersonalRevision - 1})
	requireCode(t, err, "revision_conflict")
	route, err := s.ResolveAgentRunner(member, room.ID, personal.ID)
	if err != nil || route.ID != a.ID {
		t.Fatal("default change retargeted existing Agent", route, err)
	}
	later := ownedAgent(t, s, member, room, "later")
	if later.PreferredRunnerID != b.ID {
		t.Fatal(later)
	}
	// A configured but unavailable target never falls through to another machine.
	shared := true
	if _, err = s.UpdateRunner(member, b.ID, RunnerUpdate{TeamShared: &shared}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetExecutionDefault(admin, ExecutionDefaultInput{Scope: "team", RunnerID: b.ID}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err = s.UpdateRunner(member, a.ID, RunnerUpdate{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	_, err = s.ResolveAgentRunner(member, room.ID, personal.ID)
	requireCode(t, err, "runner_unavailable")
	route, err = s.ResolveAgentRunner(member, room.ID, teamAgent.ID)
	if err != nil || route.ID != b.ID {
		t.Fatal("team default", route, err)
	}
	// An unavailable configured Runner must not make its Agent impossible to disable.
	personal.Enabled = false
	if _, err = s.SaveAgent(member, personal); err != nil {
		t.Fatal(err)
	}
}
func TestOwnedAgentSharingDoesNotDelegateRunnerManagement(t *testing.T) {
	s, room, _ := roomFixture(t)
	admin, member := teamUsers(t, s)
	runner, _ := personalRunner(t, s, admin, "alpha")
	agent := ownedAgent(t, s, admin, room, "personal")
	_, err := s.SaveAgent(member, agent)
	requireCode(t, err, "forbidden")
	requireCode(t, s.LeaveRoom(member, room.ID, agent.ID), "forbidden")
	_, err = s.UpdateRunner(member, runner.ID, RunnerUpdate{})
	requireCode(t, err, "forbidden")
	_, err = s.RunnerSources(member, runner.ID)
	requireCode(t, err, "forbidden")
	_, err = s.CreateSourceImport(member, SourceImportInput{RequestID: "private", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "v1", RoomName: "Private"})
	requireCode(t, err, "forbidden")
	budget := roomBudget
	budget.RequireRunner = true
	sent, err := s.SendMessage(member, room.ID, SendMessageInput{RequestID: "delegated", Content: "Write a document", RecipientAgentIDs: []string{agent.ID}}, budget)
	if err != nil || len(sent.Runs) != 1 || sent.Runs[0].RunnerID != runner.ID {
		t.Fatal("Room sharing did not allow invocation", sent, err)
	}
	_, err = s.SetExecutionDefault(member, ExecutionDefaultInput{Scope: "personal", RunnerID: runner.ID})
	requireCode(t, err, "runner_forbidden")
	other, err := s.CreateRoom(member, Room{Name: "Other", ResourceID: room.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.JoinRoom(member, other.ID, agent.ID), "forbidden")
	if err = s.JoinRoom(admin, other.ID, agent.ID); err != nil {
		t.Fatal(err)
	}
}
func TestExactRunnerGrantRevocationCancelsFrozenDelegatedRuns(t *testing.T) {
	s, room, agent := roomFixture(t)
	admin, member := teamUsers(t, s)
	runner, _ := personalRunner(t, s, member, "alpha")
	agent.PreferredRunnerID = runner.ID
	_, err := s.SaveAgent(admin, agent)
	requireCode(t, err, "runner_forbidden")
	if _, err = s.SetRunnerAgentGrant(member, runner.ID, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	agent, err = s.SaveAgent(admin, agent)
	if err != nil {
		t.Fatal(err)
	}
	run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, admin, room, agent, "running"))
	queued := sendRemoteTestRun(t, s, admin, room, agent, "queued")
	if _, err = s.SetRunnerAgentGrant(member, runner.ID, agent.ID, false); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetRun(ctx, run.ID)
	if err != nil || current.Status != "cancelling" {
		t.Fatal(current, err)
	}
	current, err = s.GetRun(ctx, queued.ID)
	if err != nil || current.Status != "cancelled" {
		t.Fatal(current, err)
	}
	requireCode(t, s.BeginAttemptModel(ctx, attempt.ID, runner.ID), "run_cancelled")
	// Restoring the grant cannot revive a previous authorization.
	if _, err = s.SetRunnerAgentGrant(member, runner.ID, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	current, err = s.GetRun(ctx, queued.ID)
	if err != nil || current.Status != "cancelled" {
		t.Fatal(current, err)
	}
}
func TestDisabledOwnerRevokesDelegatedWorkCredentialsAndEnrollment(t *testing.T) {
	s, room, _ := roomFixture(t)
	admin, member := teamUsers(t, s)
	owner, _ := UserFromContext(member)
	runner, join := personalRunner(t, s, member, "alpha")
	agent := ownedAgent(t, s, member, room, "personal")
	_, pending, err := s.CreateRunnerEnrollment(member, "Later", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	run := sendRemoteTestRun(t, s, admin, room, agent, "delegated")
	disabled := false
	if _, err = s.UpdateUserAccess(admin, owner.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	run, err = s.GetRun(ctx, run.ID)
	if err != nil || run.Status != "cancelled" {
		t.Fatal(run, err)
	}
	_, err = s.AuthenticateRunner(ctx, join.Credential)
	requireCode(t, err, "unauthenticated")
	_, err = s.JoinRunner(ctx, JoinRunnerInput{Token: pending, RegistrationID: "late", Credential: strings.Repeat("late", 40), StorageID: "late"})
	requireCode(t, err, "owner_disabled")
	current, err := s.GetRunner(ctx, runner.ID)
	if err != nil || !current.Revoked {
		t.Fatal(current, err)
	}
}

func TestRoomDefaultCannotBypassPersonalAgentSharing(t *testing.T) {
	s, room, _ := roomFixture(t)
	admin, member := teamUsers(t, s)
	private := ownedAgent(t, s, admin, room, "private")
	for _, withResource := range []bool{false, true} {
		name := "Direct room"
		if withResource {
			name = "Room with resource"
		}
		t.Run(name, func(t *testing.T) {
			requested := Room{Name: name, ResourceID: room.ResourceID, DefaultAgentID: private.ID}
			var err error
			if withResource {
				_, err = s.CreateRoomWithResource(member, requested, Resource{ID: "denied-resource", Name: name, CurrentCommit: strings.Repeat("b", 40)})
			} else {
				_, err = s.CreateRoom(member, requested)
			}
			requireCode(t, err, "forbidden")
		})
	}
	assertScalar(t, s, "SELECT count(*) FROM rooms", 1)
	assertScalar(t, s, "SELECT count(*) FROM resources", 1)
	allowed, err := s.CreateRoom(admin, Room{Name: "Owner shared", ResourceID: room.ResourceID, DefaultAgentID: private.ID})
	if err != nil {
		t.Fatal(err)
	}
	members, err := s.ListMembers(ctx, allowed.ID)
	if err != nil || len(members) != 1 || members[0].ID != private.ID {
		t.Fatal(members, err)
	}
}

func TestSourceImportDoesNotImplicitlySharePersonalAssistant(t *testing.T) {
	s, room, legacy := roomFixture(t)
	admin, member := teamUsers(t, s)
	legacy.Handle = "legacy-assistant"
	if _, err := s.SaveAgent(admin, legacy); err != nil {
		t.Fatal(err)
	}
	private := ownedAgent(t, s, admin, room, "assistant")
	runner, _ := personalRunner(t, s, member, "alpha")
	item, err := s.CreateSourceImport(member, SourceImportInput{RequestID: "private-assistant", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "v1", RoomName: "Imported"})
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.ClaimSourceImport(ctx, item.ID, runner.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := s.FinishSourceImport(ctx, item.ID, runner.ID, "complete", strings.Repeat("f", 64), Resource{ID: item.ResourceID, CurrentCommit: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.GetRoom(ctx, finished.RoomID)
	if err != nil || created.DefaultAgentID != "" {
		t.Fatal("import automatically shared another owner's Agent", created, private.ID, err)
	}
	members, err := s.ListMembers(ctx, created.ID)
	if err != nil || len(members) != 0 {
		t.Fatal(members, err)
	}
}
