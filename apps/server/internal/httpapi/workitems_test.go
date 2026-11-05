package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestWorkItemHTTPAuthorityEvidenceDecisionsAndRecovery(t *testing.T) {
	f := newTeamFixture(t)
	roomPath := "/api/rooms/" + f.room.ID
	posted := teamRequest(t, f, f.adminCookie, "POST", roomPath+"/messages", map[string]any{"requestId": "work-evidence", "content": "The customer accepted the revised proposal.", "postOnly": true})
	requireHTTP(t, posted, http.StatusOK)
	var message struct{ Message storage.Message }
	if err := json.Unmarshal(posted.Body.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	eventID := "room:" + message.Message.ID
	create := map[string]any{"requestId": "work-create", "goal": "Confirm customer acceptance.", "completionCriteria": "A customer confirmation is recorded.", "evidenceIds": []string{eventID}}
	path := roomPath + "/work-items"
	requireHTTP(t, teamRequest(t, f, nil, "POST", path, create), http.StatusUnauthorized)
	created := teamRequest(t, f, f.adminCookie, "POST", path, create)
	requireHTTP(t, created, http.StatusOK)
	var result struct{ Item orchestration.WorkItem }
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || result.Item.ID == "" || len(result.Item.Evidence) != 1 {
		t.Fatal(created.Body.String(), err)
	}
	item := result.Item
	itemPath := "/api/work-items/" + item.ID
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", itemPath, nil), http.StatusOK)
	decision := map[string]any{"requestId": "work-judgment", "revision": item.Revision, "kind": "request_judgment", "reason": "Ask the owner to validate the acceptance.", "question": "Does this confirmation satisfy the acceptance criteria?"}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", itemPath+"/decisions", decision), http.StatusForbidden)
	saved := teamRequest(t, f, f.adminCookie, "POST", itemPath+"/decisions", decision)
	requireHTTP(t, saved, http.StatusOK)
	if err := json.Unmarshal(saved.Body.Bytes(), &result); err != nil || result.Item.Status != "waiting_for_person" || result.Item.Question == "" {
		t.Fatal(saved.Body.String(), err)
	}
	// Exact replay resolves before later versions and does not duplicate Inbox.
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", itemPath+"/decisions", decision), http.StatusOK)
	receipt := teamRequest(t, f, f.adminCookie, "GET", "/api/work-item-requests/work-judgment", nil)
	requireHTTP(t, receipt, http.StatusOK)
	if receipt.Body.String() != saved.Body.String() {
		t.Fatal("receipt changed accepted result", receipt.Body.String(), saved.Body.String())
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/work-item-requests/work-judgment", nil), http.StatusNotFound)
	inbox := teamRequest(t, f, f.adminCookie, "GET", "/api/me/inbox", nil)
	requireHTTP(t, inbox, http.StatusOK)
	var notifications struct{ Items []storage.InboxItem }
	if err := json.Unmarshal(inbox.Body.Bytes(), &notifications); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, notification := range notifications.Items {
		if notification.WorkItemID == item.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatal("judgment receipt duplicated or omitted Inbox", notifications)
	}
	decision["reason"] = "Changed meaning under an existing identity"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", itemPath+"/decisions", decision), http.StatusConflict)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", itemPath+"/decisions", map[string]any{"requestId": "stale", "revision": item.Revision, "kind": "complete", "reason": "Accepted"}), http.StatusConflict)
	answer := map[string]any{"requestId": "work-answer", "revision": result.Item.Revision, "kind": "answer", "reason": "The acceptance is confirmed by the contract owner."}
	answered := teamRequest(t, f, f.adminCookie, "POST", itemPath+"/decisions", answer)
	requireHTTP(t, answered, http.StatusOK)
	if err := json.Unmarshal(answered.Body.Bytes(), &result); err != nil || result.Item.Status != "open" || result.Item.ActiveTaskID != "" {
		t.Fatal("answer should reopen the matter without executing a task", answered.Body.String(), err)
	}
	finished := teamRequest(t, f, f.adminCookie, "POST", itemPath+"/decisions", map[string]any{"requestId": "work-finish", "revision": result.Item.Revision, "kind": "complete", "reason": "The recorded customer acceptance satisfies the completion criteria."})
	requireHTTP(t, finished, http.StatusOK)
	if err := json.Unmarshal(finished.Body.Bytes(), &result); err != nil || result.Item.Status != "completed" {
		t.Fatal(finished.Body.String(), err)
	}
	original := teamRequest(t, f, f.memberCookie, "GET", "/api/automation-events/"+eventID, nil)
	requireHTTP(t, original, http.StatusOK)
	var evidence struct{ Event orchestration.SourceEvent }
	if err := json.Unmarshal(original.Body.Bytes(), &evidence); err != nil || evidence.Event.Content != "The customer accepted the revised proposal." {
		t.Fatal(original.Body.String(), err)
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/automation-events/"+eventID, nil), http.StatusUnauthorized)
}

func TestWorkItemHTTPLimitsAndCancelledUnknownRequests(t *testing.T) {
	f := newTeamFixture(t)
	path := "/api/rooms/" + f.room.ID + "/work-items"
	for _, query := range []string{"after=-1", "after=bad", "after=0&after=1", "limit=21", "limit=1&limit=2"} {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", path+"?"+query, nil), http.StatusBadRequest)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", path+"?limit=1", nil), http.StatusOK)
	cancelled := teamRequest(t, f, f.adminCookie, "POST", "/api/work-item-requests/late-create/cancel", map[string]any{})
	requireHTTP(t, cancelled, http.StatusOK)
	var state struct{ Cancelled bool }
	if err := json.Unmarshal(cancelled.Body.Bytes(), &state); err != nil || !state.Cancelled {
		t.Fatal(cancelled.Body.String(), err)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, map[string]any{"requestId": "late-create", "goal": "Preserve late writes.", "completionCriteria": "No cancelled write can admit work."}), http.StatusConflict)
	for _, body := range []map[string]any{
		{"requestId": "bad-evidence", "goal": "Goal", "completionCriteria": "Criteria", "evidenceIds": []string{"missing"}},
		{"requestId": "bad-deadline", "goal": "Goal", "completionCriteria": "Criteria", "deadline": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
		{"requestId": "missing-goal"},
	} {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, body), http.StatusBadRequest)
	}
}
