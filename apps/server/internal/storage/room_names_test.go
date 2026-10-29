package storage

import (
	"strings"
	"testing"
)

func TestRoomDisplayNamesAndRenameKeepIdentity(t *testing.T) {
	s, initial, _ := roomFixture(t)
	room, err := s.CreateRoom(ctx, Room{Name: "  Launch studio  ", ResourceID: initial.ResourceID})
	if err != nil || room.Name != "Launch studio" {
		t.Fatalf("display name: %+v %v", room, err)
	}
	_, err = s.CreateRoom(ctx, Room{Name: "launch STUDIO", ResourceID: initial.ResourceID})
	requireCode(t, err, "name_conflict")
	message, err := sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: "discussion", Content: "A product launch discussion.", PostOnly: true}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	room.Name = "  Café / Launch — 2026  "
	renamed, err := s.UpdateRoom(ctx, room)
	if err != nil || renamed.Name != "Café / Launch — 2026" || renamed.ID != room.ID || renamed.ResourceID != room.ResourceID {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	saved, err := s.GetRoomMessage(ctx, room.ID, message.Message.ID)
	if err != nil || saved.Content != message.Message.Content {
		t.Fatalf("rename lost conversation: %+v %v", saved, err)
	}
	_, err = s.CreateRoom(ctx, Room{Name: "CAFÉ / LAUNCH — 2026", ResourceID: initial.ResourceID})
	requireCode(t, err, "name_conflict")
	renamed.Name = "GENERAL"
	_, err = s.UpdateRoom(ctx, renamed)
	requireCode(t, err, "name_conflict")
	retained, err := s.GetRoom(ctx, room.ID)
	if err != nil || retained.Name != "Café / Launch — 2026" {
		t.Fatalf("failed rename changed room: %+v %v", retained, err)
	}
	room, err = s.CreateRoomWithResource(ctx, Room{Name: "  Research & design  "}, Resource{ID: "design-files", Name: "Design files", CurrentCommit: strings.Repeat("b", 40)})
	if err != nil || room.Name != "Research & design" {
		t.Fatalf("atomic resource creation name: %+v %v", room, err)
	}
}

func TestRoomDisplayNameBoundsAndControlCharacters(t *testing.T) {
	s, room, _ := roomFixture(t)
	for _, name := range []string{strings.Repeat("x", 120), strings.Repeat("é", 60)} {
		created, err := s.CreateRoom(ctx, Room{Name: name, ResourceID: room.ResourceID})
		if err != nil || created.Name != name {
			t.Fatalf("valid byte boundary: %q %v", name, err)
		}
	}
	for _, name := range []string{"", "   ", "\u2003", strings.Repeat("x", 121), strings.Repeat("é", 61), "Launch\nroom", "\tLaunch", "Launch\r", "Launch\x00room", "Launch\u0085room", "bad\xffname"} {
		_, err := s.CreateRoom(ctx, Room{Name: name, ResourceID: room.ResourceID})
		requireCode(t, err, "invalid_room")
		changed := room
		changed.Name = name
		_, err = s.UpdateRoom(ctx, changed)
		requireCode(t, err, "invalid_room")
	}
	retained, err := s.GetRoom(ctx, room.ID)
	if err != nil || retained.Name != room.Name {
		t.Fatalf("invalid names changed room: %+v %v", retained, err)
	}
	rooms, err := s.ListRooms(ctx)
	if err != nil || len(rooms) != 3 {
		t.Fatalf("invalid names persisted: %+v %v", rooms, err)
	}
}
