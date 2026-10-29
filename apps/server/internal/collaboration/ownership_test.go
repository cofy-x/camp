package collaboration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestNewRoomDoesNotImplicitlySharePersonalAssistant(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token := strings.Repeat("a", 64)
	if err = s.EnsureSetupToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	owner, err := s.SetupAdmin(ctx, token, "owner", "Owner", strings.Repeat("h", 64), "owner-session", time.Now().Add(time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := storage.WithUser(ctx, owner)
	if _, err = s.CreateInvite(ownerCtx, "invite", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	member, err := s.AcceptInvite(ctx, "invite", "member", "Member", strings.Repeat("h", 64), "member-session", time.Now().Add(time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.SaveAgent(ownerCtx, storage.Agent{Name: "Private assistant", Handle: "assistant", Access: "write", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{store: s, files: resources.New(filepath.Join(dir, "resources"))}
	room, err := service.CreateRoom(storage.WithUser(ctx, member), "Member room", "")
	if err != nil {
		t.Fatal(err)
	}
	members, err := s.ListMembers(ctx, room.ID)
	if err != nil || room.DefaultAgentID != "" || len(members) != 0 {
		t.Fatal("new room implicitly shared a personal Agent", private.ID, room, members, err)
	}
}
