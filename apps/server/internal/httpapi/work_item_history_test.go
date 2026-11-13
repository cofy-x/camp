package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestWorkItemHistoryHTTPBoundsAndMemberVisibility(t *testing.T) {
	f := newTeamFixture(t)
	item, err := f.store.CreateWorkItem(storage.WithUser(context.Background(), f.admin), f.room.ID, orchestration.WorkItemInput{RequestID: "history", Goal: "Follow the request", CompletionCriteria: "A confirmed response"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/work-items/" + item.ID + "/history/decisions"
	requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), http.StatusUnauthorized)
	for _, kind := range []string{"timeline", "evidence", "decisions", "tasks", "timers", "actions"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/work-items/"+item.ID+"/history/"+kind, nil), 200)
	}
	for _, query := range []string{"limit=21", "limit=-1", "limit=1&limit=2", "cursor=bad", "cursor=one&cursor=two"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"?"+query, nil), 400)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/work-items/"+item.ID+"/history/unknown", nil), 400)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/work-items/missing/history/decisions", nil), 404)
}
