package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestOrchestrationHTTPVisibilityPagingAndSourcePrivacy(t *testing.T) {
	f := newTeamFixture(t)
	path := "/api/rooms/" + f.room.ID + "/orchestration"
	requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), http.StatusUnauthorized)
	for i := range 3 {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/messages", map[string]any{"requestId": fmt.Sprint(i), "content": "Source text stays with its Message.", "postOnly": true}), 200)
	}
	response := teamRequest(t, f, f.memberCookie, "GET", path+"?limit=1", nil)
	requireHTTP(t, response, 200)
	var page storage.OrchestrationPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || !page.HasMore || page.NextAfter == nil || page.Through == 0 {
		t.Fatal(page, err)
	}
	if response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "Source text") || strings.Contains(response.Body.String(), "runtimeKey") || strings.Contains(response.Body.String(), "instructions") {
		t.Fatal("journal copied source text or private execution details", response.Body.String())
	}
	first := page.Items[0]
	if first.Trigger.ActorID != f.admin.ID || first.Trigger.MessageID == "" || first.Decision.Reason != "post_only" || len(first.Mandates) != 0 {
		t.Fatal(first)
	}
	next := teamRequest(t, f, f.adminCookie, "GET", path+fmt.Sprintf("?after=%d&through=%d", *page.NextAfter, page.Through), nil)
	requireHTTP(t, next, 200)
	if err := json.Unmarshal(next.Body.Bytes(), &page); err != nil || len(page.Items) != 2 || page.HasMore || page.NextAfter != nil {
		t.Fatal(page, err)
	}
	for _, query := range []string{"after=-1", "after=no", "after=1&after=2", "through=", "through=999", "after=2&through=1", "limit=21", "limit=9223372036854775807"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"?"+query, nil), 400)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/rooms/missing/orchestration", nil), 404)
	// No endpoint can inject a Trigger or replace a stored Decision.
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, map[string]any{"source": "github", "outcome": "accept"}), 404)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/team/members/"+f.member.ID, map[string]any{"enabled": false}), 200)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusUnauthorized)
}
