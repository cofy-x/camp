package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestWorkFilteredHTTPAndConcreteInbox(t *testing.T) {
	f := newTeamFixture(t)
	path := "/api/rooms/" + f.room.ID + "/work-items"
	var created struct{ Item orchestration.WorkItem }
	for index := range 3 {
		response := teamRequest(t, f, f.adminCookie, "POST", path, map[string]any{"requestId": fmt.Sprintf("view-create-%d", index), "goal": fmt.Sprintf("Release %d", index), "completionCriteria": "Owner confirms the release."})
		requireHTTP(t, response, http.StatusOK)
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
	}
	response := teamRequest(t, f, f.adminCookie, "POST", "/api/work-items/"+created.Item.ID+"/decisions", map[string]any{"requestId": "view-question", "revision": created.Item.Revision, "kind": "request_judgment", "reason": "Choose the audience.", "question": "Include the preview feature?"})
	requireHTTP(t, response, http.StatusOK)
	var notifications struct{ Items []storage.InboxItem }
	response = teamRequest(t, f, f.adminCookie, "GET", "/api/me/inbox", nil)
	requireHTTP(t, response, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &notifications); err != nil || len(notifications.Items) != 1 || notifications.Items[0].Title != "Release 2" || notifications.Items[0].Question != "Include the preview feature?" {
		t.Fatal(response.Body.String(), err)
	}
	var first orchestration.WorkItemPage
	response = teamRequest(t, f, f.adminCookie, "GET", path+"?state=needs_attention&limit=2", nil)
	requireHTTP(t, response, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil || len(first.Items) != 2 || !first.HasMore || first.Items[0].Goal != "Release 2" {
		t.Fatal(response.Body.String(), err)
	}
	var second orchestration.WorkItemPage
	response = teamRequest(t, f, f.adminCookie, "GET", fmt.Sprintf("%s?state=needs_attention&limit=2&after=%d", path, first.NextAfter), nil)
	requireHTTP(t, response, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil || len(second.Items) != 1 || second.HasMore || second.Items[0].Goal != "Release 0" {
		t.Fatal(response.Body.String(), err)
	}
	for _, query := range []string{"state=unknown", "state=all&state=done"} {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", path+"?"+query, nil), http.StatusBadRequest)
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", path+"?state=all", nil), http.StatusUnauthorized)
}
