package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestBootstrapReturnsCurrentNotificationFrontierWithoutReplayingHistory(t *testing.T) {
	f := newTeamFixture(t)
	before, err := f.store.ListWorkspaceEvents(context.Background(), 0)
	if err != nil || len(before) == 0 {
		t.Fatal(before, err)
	}
	response := teamRequest(t, f, f.memberCookie, "GET", "/api/bootstrap?clientId=frontier", nil)
	requireHTTP(t, response, 200)
	var boot storage.Bootstrap
	if err = json.Unmarshal(response.Body.Bytes(), &boot); err != nil {
		t.Fatal(err)
	}
	if boot.WorkspaceEventCursor != before[len(before)-1].ID {
		t.Fatal("bootstrap did not return the current notification frontier", boot)
	}
	actor := storage.WithUser(context.Background(), f.member)
	if _, err = f.store.SendMessage(actor, f.room.ID, storage.SendMessageInput{RequestID: "after-frontier", Content: "New after bootstrap", PostOnly: true}, storage.Budget{MaxTokens: 100, MaxSteps: 1, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	later, err := f.store.ListWorkspaceEvents(actor, boot.WorkspaceEventCursor)
	if err != nil || len(later) == 0 || later[len(later)-1].Type != "message.created" || later[0].ID <= boot.WorkspaceEventCursor {
		t.Fatal(later, err)
	}
}

func TestAgentRoomMembershipReadRequiresCurrentTeamSession(t *testing.T) {
	f := newTeamFixture(t)
	path := "/api/agents/" + f.room.DefaultAgentID + "/rooms"
	requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), 401)
	response := teamRequest(t, f, f.memberCookie, "GET", path, nil)
	requireHTTP(t, response, 200)
	var body struct {
		RoomIDs []string `json:"roomIds"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body.RoomIDs) != 1 || body.RoomIDs[0] != f.room.ID {
		t.Fatal(body, err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/agents/missing/rooms", nil), 404)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/team/members/"+f.member.ID, map[string]any{"enabled": false}), 200)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), 401)
}

func TestMessagePageReturnsItsReadFrontierAndRetainsThreadFiltering(t *testing.T) {
	h, store, room := roomAPI(t)
	root, err := store.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RequestID: "root", Content: "Root", PostOnly: true}, storage.Budget{MaxTokens: 100, MaxSteps: 1, TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := store.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RequestID: "reply", ThreadRootID: root.Message.ID, Content: "Reply", PostOnly: true}, storage.Budget{MaxTokens: 100, MaxSteps: 1, TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	response := roomRequest(t, h, "GET", "/api/rooms/"+room.ID+"/messages?threadRootId="+root.Message.ID+"&after="+jsonNumber(root.Message.Seq), "")
	requireHTTP(t, response, 200)
	var page storage.MessagePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Through != reply.Message.Seq || len(page.Messages) != 1 || page.Messages[0].ID != reply.Message.ID {
		t.Fatal(page, err)
	}
}
