package httpapi

import (
	"encoding/json"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"net/http"
	"strings"
	"testing"
)

func TestRoomRunTimelineAPIIsBoundedAndKeepsLegacyDetails(t *testing.T) {
	h, _, room := roomAPI(t)
	sent := roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", `{"requestId":"timeline","content":"@assistant Work"}`)
	requireHTTP(t, sent, http.StatusOK)
	var result storage.SendResult
	if err := json.Unmarshal(sent.Body.Bytes(), &result); err != nil || len(result.Runs) != 1 {
		t.Fatal(err)
	}
	prefix := "/api/rooms/" + room.ID + "/runs"
	response := roomRequest(t, h, "GET", prefix+"?view=timeline&cursor=&limit=1", "")
	requireHTTP(t, response, http.StatusOK)
	var page storage.RoomRunTimelinePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != result.Runs[0].ID {
		t.Fatal(response.Body, err)
	}
	for _, field := range []string{`"output":`, `"before":`, `"after":`, `"instructions":`, `"references":`, `"runtimeKey":`} {
		if strings.Contains(response.Body.String(), field) {
			t.Fatal("details leaked into timeline", field)
		}
	}
	legacy := roomRequest(t, h, "GET", prefix, "")
	requireHTTP(t, legacy, http.StatusOK)
	if !strings.Contains(legacy.Body.String(), `"output":`) {
		t.Fatal("legacy response changed")
	}
	for _, query := range []string{"?view=wrong", "?view=timeline&view=timeline", "?view=timeline&cursor=x", "?view=timeline&limit=0", "?view=timeline&limit=21", "?view=timeline&limit=01", "?view=timeline&limit=1&limit=1", "?view=timeline&other=x"} {
		requireHTTP(t, roomRequest(t, h, "GET", prefix+query, ""), 400)
	}
	requireHTTP(t, roomRequest(t, h, "GET", "/api/rooms/missing/runs?view=timeline", ""), 404)
}
func TestRoomRunTimelineRequiresEnabledMember(t *testing.T) {
	f := newTeamFixture(t)
	url := "/api/rooms/" + f.room.ID + "/runs?view=timeline"
	requireHTTP(t, teamRequest(t, f, nil, "GET", url, nil), http.StatusUnauthorized)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", url, nil), http.StatusOK)
}
