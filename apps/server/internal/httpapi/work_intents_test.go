package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestWorkExecutionReadUsesCurrentRouteAndGrant(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	room := "/api/rooms/" + f.room.ID
	agent := f.room.DefaultAgentID
	check := room + "/work-execution?agentId=" + agent + "&completion="
	requireHTTP(t, teamRequest(t, f, nil, "GET", check+"human", nil), 401)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", check+"typo", nil), 400)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", room+"/work-execution?completion=human", nil), 400)
	requireIssue := func(mode, want string) {
		t.Helper()
		response := teamRequest(t, f, f.memberCookie, "GET", check+mode, nil)
		requireHTTP(t, response, 200)
		var result struct{ Issue *storage.WorkExecutionIssue }
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if want == "" && result.Issue == nil {
			return
		}
		if result.Issue == nil || result.Issue.Code != want || !strings.HasPrefix(result.Issue.Message, "Assistant — ") {
			t.Fatalf("check %s: want %q, got %s", mode, want, response.Body)
		}
	}
	requireIssue("human", "runner_required")
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", room+"/runner", map[string]any{"runnerId": runner.ID, "revision": 0}), 200)
	requireIssue("human", "runner_forbidden")
	grant := "/api/runners/" + runner.ID + "/agents/" + agent
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", grant, map[string]any{}), 200)
	requireIssue("human", "")
	requireIssue("automatic", "runner_unavailable")

	// A successful observation does not authorize the later write.
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "DELETE", grant, map[string]any{}), 200)
	requireIssue("human", "runner_forbidden")
	create := map[string]any{"requestId": "start-after-revocation", "goal": "Summarize the plan.", "start": true, "participantAgentIds": []string{agent}, "firstAgentId": agent, "completionMode": "human"}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", room+"/work-items", create), 403)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/work-item-requests/start-after-revocation", nil), 404)
	for _, path := range []string{room + "/work-items", room + "/tasks", room + "/runs"} {
		response := teamRequest(t, f, f.adminCookie, "GET", path, nil)
		requireHTTP(t, response, 200)
		var lists map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &lists); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"items", "tasks", "runs"} {
			if raw := lists[key]; raw != nil && string(raw) != "[]" {
				t.Fatalf("preflight created work: %s", response.Body)
			}
		}
	}
}

func TestWorkIntentHTTPDefaultsAndTypedRecordCompletion(t *testing.T) {
	f := newTeamFixture(t)
	defaultsPath := "/api/rooms/" + f.room.ID + "/work-item-defaults"
	requireHTTP(t, teamRequest(t, f, nil, "GET", defaultsPath, nil), 401)
	response := teamRequest(t, f, f.memberCookie, "GET", defaultsPath, nil)
	requireHTTP(t, response, 200)
	var proposal struct {
		Defaults orchestration.WorkItemDefaults `json:"defaults"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &proposal); err != nil || proposal.Defaults.ExpiresAt == "" || proposal.Defaults.Execution.MaxTurns != 12 || proposal.Defaults.Execution.MaxModelRequests != 96 {
		t.Fatal(response.Body.String(), err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", defaultsPath+"?policyId=one&policyId=two", nil), 400)
	created := teamRequest(t, f, f.memberCookie, "POST", "/api/rooms/"+f.room.ID+"/work-items", map[string]any{"requestId": "record-only", "goal": "Follow the customer request", "dueAt": "2026-01-01", "expiresAt": proposal.Defaults.ExpiresAt})
	requireHTTP(t, created, 200)
	var result struct {
		Item orchestration.WorkItem `json:"item"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || result.Item.CompletionMode != "human" || result.Item.CompletionCriteria != "" || result.Item.DueAt != "2026-01-01" {
		t.Fatal(created.Body.String(), err)
	}
	path := "/api/work-items/" + result.Item.ID + "/actions/complete"
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", path, map[string]any{"requestId": "untyped", "revision": result.Item.Revision, "kind": "complete"}), 400)
	finished := teamRequest(t, f, f.memberCookie, "POST", path, map[string]any{"requestId": "human-completion", "revision": result.Item.Revision})
	requireHTTP(t, finished, 200)
	if err := json.Unmarshal(finished.Body.Bytes(), &result); err != nil || result.Item.Status != "completed" {
		t.Fatal(finished.Body.String(), err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/work-items/"+result.Item.ID+"/actions/unknown", map[string]any{"requestId": "unknown", "revision": result.Item.Revision}), 400)
}

func TestWorkIntentHTTPAtomicStartFollowAndDurableStop(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/runners/"+runner.ID, map[string]any{"teamShared": true}), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/execution-defaults", map[string]any{"scope": "team", "runnerId": runner.ID, "revision": 0}), 200)
	message := teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/messages", map[string]any{"requestId": "origin", "content": "Follow this request", "postOnly": true})
	requireHTTP(t, message, 200)
	var source struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	if err := json.Unmarshal(message.Body.Bytes(), &source); err != nil || source.Message.ID == "" {
		t.Fatal(message.Body.String(), err)
	}
	body := map[string]any{"requestId": "atomic-intent", "goal": "Follow the request to a reviewed result", "messageId": source.Message.ID, "follow": true, "start": true, "participantAgentIds": []string{f.room.DefaultAgentID}, "firstAgentId": f.room.DefaultAgentID}
	path := "/api/rooms/" + f.room.ID + "/work-items"
	created := teamRequest(t, f, f.adminCookie, "POST", path, body)
	requireHTTP(t, created, 200)
	var result struct {
		Item orchestration.WorkItem `json:"item"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || result.Item.ActiveTaskID == "" || result.Item.Execution == nil || len(result.Item.Watches) != 1 || result.Item.Watches[0].ThreadKey != f.room.ID+":"+source.Message.ID {
		t.Fatal(created.Body.String(), err)
	}
	first := result.Item
	replay := teamRequest(t, f, f.adminCookie, "POST", path, body)
	requireHTTP(t, replay, 200)
	if err := json.Unmarshal(replay.Body.Bytes(), &result); err != nil || result.Item.ID != first.ID || result.Item.ActiveTaskID != first.ActiveTaskID {
		t.Fatal(replay.Body.String(), err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/work-items/"+first.ID+"/actions/stop", map[string]any{"requestId": "other-stop", "revision": first.Revision}), 403)
	stopped := teamRequest(t, f, f.adminCookie, "POST", "/api/work-items/"+first.ID+"/actions/stop", map[string]any{"requestId": "stop", "revision": first.Revision})
	requireHTTP(t, stopped, http.StatusOK)
	if err := json.Unmarshal(stopped.Body.Bytes(), &result); err != nil || result.Item.Status != "cancelled" || !result.Item.StopRequested {
		t.Fatal(stopped.Body.String(), err)
	}
	current := teamRequest(t, f, f.memberCookie, "GET", "/api/work-items/"+first.ID, nil)
	requireHTTP(t, current, 200)
}
