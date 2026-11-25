package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestCurrentWorkHTTPKeepsPresentFactsAndReadsHistorySeparately(t *testing.T) {
	f := newTeamFixture(t)
	response := teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/work-items", map[string]any{"requestId": "current-create", "goal": "Record progress"})
	requireHTTP(t, response, http.StatusOK)
	var out struct{ Item orchestration.WorkItem }
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	path := "/api/work-items/" + out.Item.ID
	for index := range 2 {
		response := teamRequest(t, f, f.adminCookie, "POST", path+"/actions/wait", map[string]any{
			"requestId": fmt.Sprintf("current-wait-%d", index), "revision": out.Item.Revision,
			"waitCondition": "Check review", "resumeAt": time.Now().Add(time.Duration(index+1) * time.Hour).UTC().Format(time.RFC3339Nano),
		})
		requireHTTP(t, response, http.StatusOK)
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	var current struct{ Item orchestration.WorkItem }
	response = teamRequest(t, f, f.adminCookie, "GET", path+"?view=current", nil)
	requireHTTP(t, response, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &current); err != nil || current.Item.Revision != out.Item.Revision || len(current.Item.Timers) != 1 || current.Item.Timers[0].Status != "pending" || len(current.Item.Decisions) != 0 {
		t.Fatal(response.Body.String(), err)
	}
	response = teamRequest(t, f, f.adminCookie, "GET", path+"/history/timers", nil)
	requireHTTP(t, response, http.StatusOK)
	var history struct{ Items []orchestration.WorkTimer }
	if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil || len(history.Items) != 2 {
		t.Fatal(response.Body.String(), err)
	}
	response = teamRequest(t, f, f.adminCookie, "POST", path+"/actions/complete", map[string]any{"requestId": "current-close", "revision": out.Item.Revision, "comment": "Reviewed by the owner."})
	requireHTTP(t, response, http.StatusOK)
	response = teamRequest(t, f, f.adminCookie, "GET", path+"?view=current", nil)
	requireHTTP(t, response, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &current); err != nil || current.Item.Status != "completed" || len(current.Item.Decisions) != 1 || current.Item.Decisions[0].Reason != "Reviewed by the owner." || len(current.Item.Timers) != 0 {
		t.Fatal(response.Body.String(), err)
	}
	response = teamRequest(t, f, f.adminCookie, "GET", path, nil)
	requireHTTP(t, response, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || len(out.Item.Decisions) <= 1 || len(out.Item.Timers) != 2 {
		t.Fatal(response.Body.String(), err)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", path+"?view=current&view=current", nil), http.StatusBadRequest)
}

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
