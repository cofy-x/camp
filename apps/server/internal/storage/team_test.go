package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSlowMutationsRecheckCurrentAuthority(t *testing.T) {
	s, room, agent := roomFixture(t)
	admin, member := teamUsers(t, s)
	owner, _ := UserFromContext(admin)
	person, _ := UserFromContext(member)
	role := "admin"
	if _, err := s.UpdateUserAccess(admin, person.ID, &role, nil); err != nil {
		t.Fatal(err)
	}
	role = "member"
	if _, err := s.UpdateUserAccess(member, owner.ID, &role, nil); err != nil {
		t.Fatal(err)
	}
	resource, err := s.GetResource(ctx, room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	adminWrites := map[string]func() error{
		"agent profile":        func() error { _, err := s.SaveAgent(admin, agent); return err },
		"resource environment": func() error { _, err := s.UpdateResource(admin, resource); return err },
		"remove member":        func() error { return s.LeaveRoom(admin, room.ID, agent.ID) },
		"host import": func() error {
			_, err := s.CreateResource(admin, Resource{Name: "Imported", SourcePath: "/operator/import", CurrentCommit: strings.Repeat("c", 40)})
			return err
		},
	}
	for name, write := range adminWrites {
		t.Run(name, func(t *testing.T) { requireCode(t, write(), "forbidden") })
	}
	enabled := false
	if _, err := s.UpdateUserAccess(member, owner.ID, nil, &enabled); err != nil {
		t.Fatal(err)
	}
	memberWrites := map[string]func() error{
		"room update": func() error { _, err := s.UpdateRoom(admin, room); return err },
		"room create": func() error {
			_, err := s.CreateRoom(admin, Room{Name: "Later", ResourceID: room.ResourceID})
			return err
		},
		"join": func() error { return s.JoinRoom(admin, room.ID, agent.ID) },
		"empty resource": func() error {
			_, err := s.CreateResource(admin, Resource{Name: "Empty", CurrentCommit: strings.Repeat("c", 40)})
			return err
		},
		"upload": func() error {
			_, err := s.RegisterRoomUpload(admin, room.ID, "late-upload", strings.Repeat("d", 32), strings.Repeat("c", 40), "notes.txt", 5, strings.Repeat("e", 64))
			return err
		},
		"draft": func() error {
			_, err := s.SaveDraft(admin, "document", "file", json.RawMessage(`{"content":"late"}`), 0)
			return err
		},
		"discard draft": func() error { _, err := s.DeleteDraft(admin, "document", "file", 0); return err },
		"preferences":   func() error { return s.SavePreferences(admin, Preferences{Wallpaper: "solid"}) },
		"layout":        func() error { return s.SaveLayout(admin, "browser", json.RawMessage(`{}`)) },
		"read state":    func() error { return s.SaveRoomRead(admin, room.ID, 0) },
		"inbox":         func() error { return s.ReadInboxItem(admin, "missing") },
	}
	for name, write := range memberWrites {
		t.Run(name, func(t *testing.T) { requireCode(t, write(), "unauthenticated") })
	}
}

func teamUsers(t *testing.T, s *Store) (context.Context, context.Context) {
	t.Helper()
	if e := s.EnsureSetupToken(ctx, strings.Repeat("a", 64)); e != nil {
		t.Fatal(e)
	}
	admin, e := s.SetupAdmin(ctx, strings.Repeat("a", 64), "owner", "Owner", strings.Repeat("h", 64), "session-owner", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	if e != nil {
		t.Fatal(e)
	}
	adminCtx := WithUser(ctx, admin)
	_, e = s.CreateInvite(adminCtx, "invite-member", time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	member, e := s.AcceptInvite(ctx, "invite-member", "writer", "Writer", strings.Repeat("h", 64), "session-member", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	if e != nil {
		t.Fatal(e)
	}
	return adminCtx, WithUser(ctx, member)
}
func TestTeamDisabledUserCannotAdmitOrClaimWork(t *testing.T) {
	s, room, _ := roomFixture(t)
	admin, member := teamUsers(t, s)
	u, _ := UserFromContext(member)
	sent, e := s.SendMessage(member, room.ID, SendMessageInput{RequestID: "first", Content: "@assistant read"}, roomBudget)
	if e != nil || len(sent.Runs) != 1 {
		t.Fatalf("send %v %v", sent, e)
	}
	disabled := false
	if _, e = s.UpdateUserAccess(admin, u.ID, nil, &disabled); e != nil {
		t.Fatal(e)
	}
	_, e = s.SendMessage(member, room.ID, SendMessageInput{RequestID: "second", Content: "@assistant read"}, roomBudget)
	requireCode(t, e, "forbidden")
	_, e = s.ClaimRun(ctx, sent.Runs[0].ID)
	requireCode(t, e, "agent_unavailable")
	if _, e = s.SessionUser(ctx, "session-member"); e == nil {
		t.Fatal("disabled account kept session")
	}
}
func TestTeamNotificationsAndReadStateArePrivateAndAtomic(t *testing.T) {
	s, room, _ := roomFixture(t)
	admin, member := teamUsers(t, s)
	u, _ := UserFromContext(member)
	sent, e := s.SendMessage(admin, room.ID, SendMessageInput{RequestID: "mention", Content: "@writer Please review."}, roomBudget)
	if e != nil || len(sent.Runs) != 0 {
		t.Fatal(e, sent)
	}
	if e = s.SaveRoomRead(member, room.ID, sent.Message.Seq); e != nil {
		t.Fatal(e)
	}
	reads, e := s.ListRoomReads(admin)
	if e != nil || len(reads) != 0 {
		t.Fatal("read state shared", reads, e)
	}
	items, e := s.ListInbox(member)
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	if e = s.ReadInboxItem(admin, items[0].ID); e == nil {
		t.Fatal("another user read private notification")
	}
	work, e := s.SendMessage(member, room.ID, SendMessageInput{RequestID: "work", Content: "@assistant inspect"}, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.ClaimRun(ctx, work.Runs[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	if _, e = s.FinishRun(ctx, run, "Done", false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.FinishRun(ctx, run, "Duplicate", false); e != nil {
		t.Fatal(e)
	}
	items, e = s.ListInbox(member)
	if e != nil || len(items) != 2 {
		t.Fatal(items, e)
	}
	if work.Runs[0].RequestedByUserID != u.ID {
		t.Fatal("missing attribution")
	}
	assertScalar(t, s, "SELECT count(*) FROM resource_publications", 1)
	assertScalar(t, s, "SELECT count(*) FROM inbox WHERE kind='run_finished'", 1)
}

func TestRevocationWinsAgainstLateSuccessfulPublication(t *testing.T) {
	for _, cause := range []string{"member", "agent", "membership", "cancel"} {
		t.Run(cause, func(t *testing.T) {
			s, room, agent := roomFixture(t)
			admin, member := teamUsers(t, s)
			sent, err := s.SendMessage(member, room.ID, SendMessageInput{RequestID: "late-result", Content: "@assistant Write a guide."}, roomBudget)
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.ClaimRun(ctx, sent.Runs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := s.GetConversation(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			run.SessionID = conversation.SessionID
			run.SessionGeneration = conversation.Generation
			run, err = s.UpdateRun(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			switch cause {
			case "member":
				user, _ := UserFromContext(member)
				enabled := false
				_, err = s.UpdateUserAccess(admin, user.ID, nil, &enabled)
			case "agent":
				agent.Enabled = false
				_, err = s.SaveAgent(admin, agent)
			case "membership":
				err = s.LeaveRoom(admin, room.ID, agent.ID)
			case "cancel":
				cancelled := run
				cancelled.Status = "cancelling"
				_, err = s.UpdateRun(ctx, cancelled)
			}
			if err != nil {
				t.Fatal(err)
			}
			run.Status = "succeeded"
			run.ResultCommit = strings.Repeat("b", 40)
			run.Output = "Existing tool output."
			finished, err := s.FinishRun(ctx, run, "The model completed its reply.", true)
			if err != nil {
				t.Fatal(err)
			}
			if finished.Status != "cancelled" || finished.Published || finished.ResultCommit != run.ResultCommit || finished.Output != run.Output {
				t.Fatalf("revoked work published or lost its result: %+v", finished)
			}
			resource, err := s.GetResource(ctx, room.ResourceID)
			if err != nil || resource.CurrentCommit != run.BaselineCommit {
				t.Fatal("shared files changed after revocation", resource, err)
			}
			if _, err = s.FinishRun(ctx, run, "Duplicate reply", true); err != nil {
				t.Fatal(err)
			}
			assertScalar(t, s, "SELECT count(*) FROM resource_publications", 0)
			assertScalar(t, s, "SELECT count(*) FROM messages WHERE run_id <> ''", 1)
			assertScalar(t, s, "SELECT count(*) FROM agent_conversations WHERE resumable = 1", 0)
		})
	}
}

func TestReenableAndRejoinDoNotReviveRevokedRequests(t *testing.T) {
	for _, cause := range []string{"member", "agent", "membership"} {
		t.Run(cause, func(t *testing.T) {
			s, room, agent := roomFixture(t)
			admin, member := teamUsers(t, s)
			activeRequest, err := s.SendMessage(member, room.ID, SendMessageInput{RequestID: "active", Content: "@assistant Start."}, roomBudget)
			if err != nil {
				t.Fatal(err)
			}
			active, err := s.ClaimRun(ctx, activeRequest.Runs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			queued, err := s.SendMessage(member, room.ID, SendMessageInput{RequestID: "queued", Content: "@assistant Later."}, roomBudget)
			if err != nil {
				t.Fatal(err)
			}
			switch cause {
			case "member":
				user, _ := UserFromContext(member)
				enabled := false
				if _, err = s.UpdateUserAccess(admin, user.ID, nil, &enabled); err != nil {
					t.Fatal(err)
				}
				enabled = true
				_, err = s.UpdateUserAccess(admin, user.ID, nil, &enabled)
			case "agent":
				agent.Enabled = false
				agent, err = s.SaveAgent(admin, agent)
				if err != nil {
					t.Fatal(err)
				}
				agent.Enabled = true
				_, err = s.SaveAgent(admin, agent)
			case "membership":
				if err = s.LeaveRoom(admin, room.ID, agent.ID); err != nil {
					t.Fatal(err)
				}
				err = s.JoinRoom(member, room.ID, agent.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimRun(ctx, queued.Runs[0].ID); err == nil {
				t.Fatal("revoked queue became eligible again")
			}
			updated, err := s.UpdateRun(ctx, active)
			if err != nil || updated.CancellationReason == "" {
				t.Fatal("stale lifecycle update cleared revocation", updated, err)
			}
			active.Status = "succeeded"
			active.ResultCommit = strings.Repeat("b", 40)
			finished, err := s.FinishRun(ctx, active, "Late output.", true)
			if err != nil || finished.Status != "cancelled" || finished.Published {
				t.Fatal("re-enabled identity published revoked work", finished, err)
			}
			next, err := s.SendMessage(member, room.ID, SendMessageInput{RequestID: "new-request", Content: "@assistant Explicit new request."}, roomBudget)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimRun(ctx, next.Runs[0].ID); err != nil {
				t.Fatal("new explicit request was incorrectly revoked", err)
			}
		})
	}
}

func TestAgentProfileChangePreservesFrozenQueuedRequest(t *testing.T) {
	s, room, agent := roomFixture(t)
	admin, member := teamUsers(t, s)
	sent, err := s.SendMessage(member, room.ID, SendMessageInput{RequestID: "profile", Content: "@assistant Write."}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	before := sent.Runs[0].AgentSnapshot
	agent.Name = "Revised Assistant"
	agent.Instructions = "Review carefully."
	if _, err = s.SaveAgent(admin, agent); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimRun(ctx, sent.Runs[0].ID)
	if err != nil || claimed.CancellationReason != "" || claimed.AgentSnapshot != before {
		t.Fatal("profile edit changed or revoked frozen work", claimed, err)
	}
}

func TestTeamInvitationRollbackExpiryAndRoleRace(t *testing.T) {
	s := testStore(t)
	admin, member := teamUsers(t, s)
	_, e := s.CreateInvite(admin, "retryable", time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.AcceptInvite(ctx, "retryable", "owner", "Duplicate", strings.Repeat("h", 64), "duplicate", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	requireCode(t, e, "username_conflict")
	_, e = s.AcceptInvite(ctx, "retryable", "third", "Third", strings.Repeat("h", 64), "third", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	if e != nil {
		t.Fatal("failed creation consumed invite", e)
	}
	i, e := s.CreateInvite(admin, "revoked", time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RevokeInvite(admin, i.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.AcceptInvite(ctx, "revoked", "fourth", "Fourth", strings.Repeat("h", 64), "fourth", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	requireCode(t, e, "invalid_invite")
	u, _ := UserFromContext(member)
	role := "admin"
	if _, e = s.UpdateUserAccess(admin, u.ID, &role, nil); e != nil {
		t.Fatal(e)
	}
	owner, _ := UserFromContext(admin)
	role = "member"
	if _, e = s.UpdateUserAccess(member, owner.ID, &role, nil); e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateInvite(admin, "stale-authority", time.Now().Add(time.Hour))
	requireCode(t, e, "forbidden")
}

func TestTeamDraftDeletionCannotResurrectStaleText(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	draft, e := s.SaveDraft(owner, "conversation", "room", []byte(`{"content":"unsent"}`), 0)
	if e != nil {
		t.Fatal(e)
	}
	cleared, e := s.DeleteDraft(owner, "conversation", "room", draft.Revision)
	if e != nil || cleared.Revision <= draft.Revision || string(cleared.Value) != "null" {
		t.Fatal(cleared, e)
	}
	_, e = s.SaveDraft(owner, "conversation", "room", []byte(`{"content":"stale"}`), 0)
	requireCode(t, e, "revision_conflict")
	_, e = s.SaveDraft(owner, "conversation", "room", []byte(`{"content":"stale"}`), draft.Revision)
	requireCode(t, e, "revision_conflict")
	summaries, e := s.ListDrafts(owner, "conversation")
	if e != nil || len(summaries) != 0 {
		t.Fatal("deleted draft appeared in list", summaries, e)
	}
	if _, e = s.SaveDraft(owner, "conversation", "room", []byte(`{"content":"new"}`), cleared.Revision); e != nil {
		t.Fatal(e)
	}
}
