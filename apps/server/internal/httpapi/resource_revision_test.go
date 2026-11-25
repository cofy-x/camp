package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestPublicationReceiptIsActorAndResourceScoped(t *testing.T) {
	f := newTeamFixture(t)
	ctx := storage.WithUser(context.Background(), f.admin)
	resource, err := f.store.GetResource(ctx, f.room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	snapshotKey := "library-publication:" + resource.ID + ":copy-original"
	if _, err := f.store.SaveDraft(ctx, "document", snapshotKey, json.RawMessage(`{"content":"original publication"}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveDraft(ctx, "document", resource.ID+":copy.md", json.RawMessage(`{"path":"copy.md","content":"newer draft"}`), 0); err != nil {
		t.Fatal(err)
	}
	if drafts, err := f.store.ListDrafts(ctx, "document"); err != nil || len(drafts) != 1 || drafts[0].Path != "copy.md" {
		t.Fatalf("frozen publication must stay out of editable drafts: %+v %v", drafts, err)
	}
	first, err := f.store.Publish(ctx, storage.PublishInput{
		ResourceID: resource.ID, ExpectedCommit: resource.CurrentCommit, Commit: strings.Repeat("b", 40),
		RequestID: "copy-original", RequestHash: strings.Repeat("a", 64), Kind: "edit", Path: "copy.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, err := f.store.GetDraft(ctx, "document", snapshotKey); err != nil || string(snapshot.Value) != "null" || snapshot.Revision != 2 {
		t.Fatalf("committing publication did not retire its snapshot: %+v %v", snapshot, err)
	}
	if editable, err := f.store.GetDraft(ctx, "document", resource.ID+":copy.md"); err != nil || !strings.Contains(string(editable.Value), "newer draft") {
		t.Fatalf("committing publication changed the editable draft: %+v %v", editable, err)
	}
	unsentKey := "library-publication:" + resource.ID + ":unsubmitted"
	if tombstone, err := f.store.DeleteDraft(ctx, "document", unsentKey, 0); err != nil || tombstone.Revision != 1 || string(tombstone.Value) != "null" {
		t.Fatalf("absent snapshot was not fenced: %+v %v", tombstone, err)
	}
	if _, err := f.store.SaveDraft(ctx, "document", unsentKey, json.RawMessage(`{"content":"delayed text"}`), 0); err == nil {
		t.Fatal("late preparation revived an unpublished snapshot tombstone")
	}
	if _, err = f.store.Publish(ctx, storage.PublishInput{
		ResourceID: resource.ID, ExpectedCommit: first.Commit, Commit: strings.Repeat("c", 40),
		RequestID: "restore-earlier", RequestHash: strings.Repeat("b", 64), Kind: "restore",
	}); err != nil {
		t.Fatal(err)
	}
	path := "/api/resources/" + resource.ID + "/publication-requests/copy-original"
	requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), 401)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), 404)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/resources/other/publication-requests/copy-original", nil), 404)
	response := teamRequest(t, f, f.adminCookie, "GET", path, nil)
	requireHTTP(t, response, 200)
	var result struct {
		Publication storage.Publication `json:"publication"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Publication != first {
		t.Fatalf("receipt should refer to the original save: %s %v", response.Body.String(), err)
	}
}

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
