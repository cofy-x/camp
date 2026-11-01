package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestResourceConfigurationRequiresRevisionAndRejectsStaleAdministrators(t *testing.T) {
	f := newTeamFixture(t)
	role := "admin"
	if _, err := f.store.UpdateUserAccess(storage.WithUser(context.Background(), f.admin), f.member.ID, &role, nil); err != nil {
		t.Fatal(err)
	}
	url := "/api/resources/" + f.room.ResourceID
	response := teamRequest(t, f, f.adminCookie, "GET", url, nil)
	requireHTTP(t, response, 200)
	var baseline struct {
		Resource storage.Resource `json:"resource"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &baseline); err != nil || baseline.Resource.Revision != 1 {
		t.Fatal(response.Body.String(), err)
	}
	for _, body := range []map[string]any{{"name": "Missing precondition"}, {"expectedRevision": nil, "name": "Missing precondition"}} {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", url, body), http.StatusPreconditionRequired)
	}
	for _, revision := range []int{0, -1} {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", url, map[string]any{"name": "Invalid revision", "expectedRevision": revision}), 400)
	}
	results := make(chan int, 2)
	start := make(chan struct{})
	for i, cookie := range []*http.Cookie{f.adminCookie, f.memberCookie} {
		go func() {
			<-start
			r := teamRequest(t, f, cookie, "PATCH", url, map[string]any{"name": []string{"First admin", "Second admin"}[i], "expectedRevision": baseline.Resource.Revision, "environment": map[string]any{"kind": "command", "checks": []any{}}})
			results <- r.Code
		}()
	}
	close(start)
	statuses := map[int]int{}
	for range 2 {
		statuses[<-results]++
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatal(statuses)
	}
	current, err := f.store.GetResource(context.Background(), f.room.ResourceID)
	if err != nil || current.Revision != 2 || current.CurrentCommit != baseline.Resource.CurrentCommit {
		t.Fatal(current, err)
	}
	conflict := teamRequest(t, f, f.adminCookie, "PATCH", url, map[string]any{"name": "Stale overwrite", "expectedRevision": 1})
	requireHTTP(t, conflict, 409)
	if !containsCode(conflict.Body.Bytes(), "resource_conflict") {
		t.Fatal(conflict.Body.String())
	}
}

func containsCode(body []byte, code string) bool {
	var value struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(body, &value) == nil && value.Code == code
}
