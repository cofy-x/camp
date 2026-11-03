package storage

import (
	"testing"
	"time"
)

func TestInviteListRechecksAdministratorWithinReadSnapshot(t *testing.T) {
	s := testStore(t)
	admin, member := teamUsers(t, s)
	if _, err := s.CreateInvite(admin, "test-invitation-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if invites, err := s.ListInvites(admin); err != nil || len(invites) == 0 {
		t.Fatal("administrator could not read invitations", invites, err)
	}
	other, _ := UserFromContext(member)
	adminRole := "admin"
	if _, err := s.UpdateUserAccess(admin, other.ID, &adminRole, nil); err != nil {
		t.Fatal(err)
	}
	actor, _ := UserFromContext(admin)
	memberRole := "member"
	if _, err := s.UpdateUserAccess(admin, actor.ID, &memberRole, nil); err != nil {
		t.Fatal(err)
	}
	_, err := s.ListInvites(admin)
	requireCode(t, err, "forbidden")
}
