package httpapi

import (
	"net/http"
	"testing"
)

func TestAutomationEvaluationHistoryRequiresEnabledMembershipAndIsReadOnly(t *testing.T) {
	f := newTeamFixture(t)
	path := "/api/automation-evaluations/missing"
	requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), http.StatusUnauthorized)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusNotFound)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, map[string]any{"outcome": "accept"}), http.StatusNotFound)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/team/members/"+f.member.ID, map[string]any{"enabled": false}), http.StatusOK)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusUnauthorized)
}
