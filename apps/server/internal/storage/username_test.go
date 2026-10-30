package storage

import (
	"strings"
	"testing"
	"time"
)

func TestUsernameValidationPreservesSetupAndInvite(t *testing.T) {
	s := testStore(t)
	setupHash := strings.Repeat("a", 64)
	passwordHash := strings.Repeat("h", 64)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	invalid := []string{"", "ab", strings.Repeat("a", 33), "invalid!name", "1starts-with-number", "-starts", "_starts", "with space", "with/slash", "用户abc"}
	if err := s.EnsureSetupToken(ctx, setupHash); err != nil {
		t.Fatal(err)
	}
	for _, username := range invalid {
		t.Run("setup/"+username, func(t *testing.T) {
			_, err := s.SetupAdmin(ctx, setupHash, username, "Owner", passwordHash, "owner-session", expires)
			requireCode(t, err, "invalid_user")
			assertScalar(t, s, "SELECT count(*) FROM users", 0)
			assertScalar(t, s, "SELECT count(*) FROM sessions", 0)
		})
	}
	owner, err := s.SetupAdmin(ctx, setupHash, " Valid-User_1 ", "Owner", passwordHash, "owner-session", expires)
	if err != nil || owner.Username != "valid-user_1" {
		t.Fatalf("setup after invalid attempts: %+v, %v", owner, err)
	}
	actor := WithUser(ctx, owner)
	if _, err := s.CreateInvite(actor, "invite-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, username := range invalid {
		t.Run("invite/"+username, func(t *testing.T) {
			_, err := s.AcceptInvite(ctx, "invite-hash", username, "Member", passwordHash, "member-session", expires)
			requireCode(t, err, "invalid_user")
			assertScalar(t, s, "SELECT count(*) FROM users", 1)
			assertScalar(t, s, "SELECT count(*) FROM sessions", 1)
		})
	}
	member, err := s.AcceptInvite(ctx, "invite-hash", strings.Repeat("a", 32), "Member", passwordHash, "member-session", expires)
	if err != nil || member.Username != strings.Repeat("a", 32) {
		t.Fatalf("invite after invalid attempts: %+v, %v", member, err)
	}
	for _, user := range []User{owner, member} {
		found, hash, err := s.LoginUser(ctx, " "+strings.ToUpper(user.Username)+" ")
		if err != nil || found.ID != user.ID || hash != passwordHash {
			t.Fatalf("normalized login for %q: %+v, %v", user.Username, found, err)
		}
	}
}
