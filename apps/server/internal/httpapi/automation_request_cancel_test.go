package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAutomationUnconfirmedOperationCancellationHasDurableActorScopedFence(t *testing.T) {
	f := newTeamFixture(t)
	cancelPath := "/api/automation-requests/never-arrived/cancel"
	requireHTTP(t, teamRequest(t, f, nil, "POST", cancelPath, map[string]any{}), http.StatusUnauthorized)
	for range 2 {
		response := teamRequest(t, f, f.adminCookie, "POST", cancelPath, map[string]any{})
		requireHTTP(t, response, http.StatusOK)
		var receipt storage.AutomationReceipt
		if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || receipt.Kind != "cancelled" {
			t.Fatal(response.Body.String(), err)
		}
	}
	policyPath := "/api/rooms/" + f.room.ID + "/automation-policies/room"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", policyPath, orchestration.PolicyInput{RequestID: "never-arrived", Mode: "observe"}), http.StatusConflict)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/automation-requests/never-arrived", nil), http.StatusOK)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/automation-requests/never-arrived", nil), http.StatusNotFound)
	// The same textual ID under another member is a different operation.
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", cancelPath, map[string]any{}), http.StatusOK)
	input := orchestration.PolicyInput{RequestID: "already-arrived", Mode: "observe"}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", policyPath, input), http.StatusOK)
	response := teamRequest(t, f, f.adminCookie, "POST", "/api/automation-requests/already-arrived/cancel", map[string]any{})
	requireHTTP(t, response, http.StatusOK)
	var saved storage.AutomationReceipt
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil || saved.Kind != "policy" || saved.Policy == nil {
		t.Fatal("cancel lost a committed operation", response.Body.String(), err)
	}
}
