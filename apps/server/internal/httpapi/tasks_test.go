package httpapi

import (
	"encoding/json"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"net/http"
	"testing"
)

func TestTaskHTTPAdmissionReplayAndMemberOperations(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/runners/"+runner.ID, map[string]any{"teamShared": true}), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/execution-defaults", map[string]any{"scope": "team", "runnerId": runner.ID, "revision": 0}), 200)
	body := map[string]any{"requestId": "task-create", "goal": "Write and verify a document.", "participantAgentIds": []string{f.room.DefaultAgentID}, "firstAgentId": f.room.DefaultAgentID}
	route := "/api/rooms/" + f.room.ID + "/tasks"
	requireHTTP(t, teamRequest(t, f, nil, "POST", route, body), http.StatusUnauthorized)
	created := teamRequest(t, f, f.memberCookie, "POST", route, body)
	requireHTTP(t, created, 200)
	var response struct {
		Task storage.CollaborationTask `json:"task"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	task := response.Task
	if task.ID == "" || task.MaxTurns != 6 || task.MaxModelRequests != 24 || len(task.Participants) != 1 {
		t.Fatal(task)
	}
	// A retry remains the same accepted request even after a route change.
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/execution-defaults", map[string]any{"scope": "team", "runnerId": "", "revision": 1}), 200)
	replay := teamRequest(t, f, f.memberCookie, "POST", route, body)
	requireHTTP(t, replay, 200)
	if err := json.Unmarshal(replay.Body.Bytes(), &response); err != nil || response.Task.ID != task.ID {
		t.Fatal("retry changed task", err, response)
	}
	body["goal"] = "Different content"
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", route, body), 409)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/tasks/"+task.ID+"/inputs", map[string]any{"requestId": "input", "content": "Use a short title."}), 200)
	got := teamRequest(t, f, f.memberCookie, "GET", "/api/tasks/"+task.ID, nil)
	requireHTTP(t, got, 200)
	if err := json.Unmarshal(got.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/tasks/"+task.ID+"/cancel", map[string]any{"requestId": "cancel", "revision": response.Task.Revision}), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", route, nil), 200)
}
func TestRunnerInstallationAuthenticationAndUnavailablePackage(t *testing.T) {
	f, _ := remoteTeamFixture(t)
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/runner-installation", nil), 401)
	response := teamRequest(t, f, f.memberCookie, "GET", "/api/runner-installation", nil)
	requireHTTP(t, response, 200)
	var data struct {
		Available   bool   `json:"available"`
		DownloadURL string `json:"downloadUrl"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &data); e != nil || data.Available || data.DownloadURL != "" {
		t.Fatal(data, e)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/runner-package", nil), 404)
}

func TestTaskSummaryAndBoundedHistoryHTTP(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/runners/"+runner.ID, map[string]any{"teamShared": true}), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/execution-defaults", map[string]any{"scope": "team", "runnerId": runner.ID, "revision": 0}), 200)
	route := "/api/rooms/" + f.room.ID + "/tasks"
	body := map[string]any{"requestId": "paged-create", "goal": "Preserve the input originals.", "participantAgentIds": []string{f.room.DefaultAgentID}, "firstAgentId": f.room.DefaultAgentID}
	created := teamRequest(t, f, f.adminCookie, "POST", route+"?view=summary", body)
	requireHTTP(t, created, 200)
	var response struct{ Task storage.CollaborationTask }
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(created.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["task"]["inputs"]; present {
		t.Fatal("summary contains input history")
	}
	id := response.Task.ID
	for _, content := range []string{"First input.", "Later input."} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/tasks/"+id+"/inputs?view=summary", map[string]any{"requestId": content, "content": content}), 200)
	}
	path := "/api/tasks/" + id
	for _, suffix := range []string{"?view=summary", "/inputs", "/runs"} {
		requireHTTP(t, teamRequest(t, f, nil, "GET", path+suffix, nil), 401)
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+suffix, nil), 200)
	}
	detail := teamRequest(t, f, f.memberCookie, "GET", path+"?view=summary", nil)
	var detailRaw map[string]json.RawMessage
	if err := json.Unmarshal(detail.Body.Bytes(), &detailRaw); err != nil {
		t.Fatal(err)
	}
	if _, present := detailRaw["runs"]; present {
		t.Fatal("summary contains round history")
	}
	paged := teamRequest(t, f, f.memberCookie, "GET", path+"/inputs?after=0&through=1&limit=1", nil)
	requireHTTP(t, paged, 200)
	var page storage.TaskInputPage
	if err := json.Unmarshal(paged.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Content != "First input." || page.Through != 1 || page.HasMore {
		t.Fatal(page, err)
	}
	for _, query := range []string{"after=-1", "after=no", "after=1&after=2", "through=3", "through=", "after=2&through=1", "limit=21", "limit=9223372036854775807"} {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", path+"/inputs?"+query, nil), 400)
	}
	for _, query := range []string{"cursor=broken", "cursor=a&cursor=b", "limit=21"} {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", path+"/runs?"+query, nil), 400)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", path+"?view=unknown", nil), 400)
	// Old clients keep the old detail response and originals.
	legacy := teamRequest(t, f, f.memberCookie, "GET", path, nil)
	requireHTTP(t, legacy, 200)
	if err := json.Unmarshal(legacy.Body.Bytes(), &response); err != nil || len(response.Task.Inputs) != 2 {
		t.Fatal(response, err)
	}
}
