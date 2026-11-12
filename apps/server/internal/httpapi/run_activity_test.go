package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestRunActivityValidatesHistoryPositionsAndLeavesOriginalReadsExplicit(t *testing.T) {
	h, store, room := roomAPI(t)
	sent := roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", `{"requestId":"activity-window","content":"@assistant Work"}`)
	requireHTTP(t, sent, 200)
	var result storage.SendResult
	if err := json.Unmarshal(sent.Body.Bytes(), &result); err != nil || len(result.Runs) != 1 {
		t.Fatal(err)
	}
	id := result.Runs[0].ID
	for _, item := range []struct{ kind, text string }{{"output", "ORIGINAL_RAW_LOG"}, {"tool_call", `{"name":"read_file","arguments":{"path":"notes.md"}}`}} {
		if _, err := store.AppendRunEvent(context.Background(), id, item.kind, item.text); err != nil {
			t.Fatal(err)
		}
	}
	prefix := "/api/runs/" + id + "/activity"
	response := roomRequest(t, h, "GET", prefix+"?limit=6", "")
	requireHTTP(t, response, 200)
	var page storage.RunActivityWindow
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Detail != "notes.md" || page.Through != 2 {
		t.Fatal(response.Body, err)
	}
	for _, query := range []string{"?limit=51", "?limit=0", "?limit=1&limit=2", "?limit=-1", "?limit=1.5", "?after=0", "?after=01", "?before=3&after=1", "?through=3", "?through=0&after=1", "?before=4", "?through=-1", "?through=0&through=0", "?extra=1"} {
		requireHTTP(t, roomRequest(t, h, "GET", prefix+query, ""), 400)
	}
	requireHTTP(t, roomRequest(t, h, "GET", "/api/runs/missing/activity", ""), 404)
	original := roomRequest(t, h, "GET", "/api/runs/"+id+"/events", "")
	requireHTTP(t, original, 200)
	var body struct{ Events []storage.RunEvent }
	if err := json.Unmarshal(original.Body.Bytes(), &body); err != nil || len(body.Events) != 2 || body.Events[0].Text != "ORIGINAL_RAW_LOG" {
		t.Fatal("projection changed raw events", original.Body, err)
	}
}

func TestRunActivityRequiresCurrentEnabledMember(t *testing.T) {
	f := newTeamFixture(t)
	result, err := f.store.SendMessage(storage.WithUser(context.Background(), f.admin), f.room.ID, storage.SendMessageInput{RequestID: "activity-auth", Content: "Work", RecipientAgentIDs: []string{f.room.DefaultAgentID}}, storage.Budget{MaxTokens: 32, MaxSteps: 2, TimeoutSeconds: 2})
	if err != nil || len(result.Runs) != 1 {
		t.Fatal(err)
	}
	path := "/api/runs/" + result.Runs[0].ID + "/activity"
	requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), http.StatusUnauthorized)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusOK)
	disabled := false
	if _, err := f.store.UpdateUserAccess(storage.WithUser(context.Background(), f.admin), f.member.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusUnauthorized)
}
