package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAutomationActivityHTTPDescendingFilterAndLegacyCursorContracts(t *testing.T) {
	f := newTeamFixture(t)
	path := "/api/rooms/" + f.room.ID + "/automation-events"
	for index := range 3 {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/messages", map[string]any{"requestId": fmt.Sprint(index), "content": fmt.Sprintf("Message %d", index), "postOnly": true}), http.StatusOK)
	}
	response := teamRequest(t, f, f.memberCookie, "GET", path+"?direction=desc&sourceId=room&limit=1", nil)
	requireHTTP(t, response, http.StatusOK)
	var first storage.AutomationEventPage
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil || len(first.Items) != 1 || first.Items[0].Preview != "Message 2" || first.NextBefore == nil || first.NextAfter != nil || !first.HasMore {
		t.Fatal(response.Body.String(), err)
	}
	older := teamRequest(t, f, f.memberCookie, "GET", path+fmt.Sprintf("?direction=desc&sourceId=room&limit=1&before=%d&through=%d", *first.NextBefore, first.Through), nil)
	requireHTTP(t, older, http.StatusOK)
	var next storage.AutomationEventPage
	if err := json.Unmarshal(older.Body.Bytes(), &next); err != nil || len(next.Items) != 1 || next.Items[0].Preview != "Message 1" {
		t.Fatal(older.Body.String(), err)
	}
	legacy := teamRequest(t, f, f.memberCookie, "GET", path+"?limit=1", nil)
	requireHTTP(t, legacy, http.StatusOK)
	var ascending storage.AutomationEventPage
	if err := json.Unmarshal(legacy.Body.Bytes(), &ascending); err != nil || len(ascending.Items) != 1 || ascending.Items[0].Preview != "Message 0" || ascending.NextBefore != nil || ascending.NextAfter == nil {
		t.Fatal(legacy.Body.String(), err)
	}
	for _, query := range []string{"direction=desc&after=0", "direction=asc&before=1", "direction=desc&before=0", "direction=desc&before=99999", "direction=unknown", "direction=", "direction=desc&direction=asc", "sourceId=room&sourceId=task", "sourceId=missing", "direction=desc&before=1&before=2", "direction=desc&limit=21"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"?"+query, nil), http.StatusBadRequest)
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", path+"?direction=desc", nil), http.StatusUnauthorized)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("activity history was cacheable")
	}
}
