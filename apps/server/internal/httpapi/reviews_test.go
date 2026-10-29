package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/collaboration"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestReviewHTTPUsesRealImmutableFilesAndHumanAuthority(t *testing.T) {
	f := newTeamFixture(t)
	owner := storage.WithUser(context.Background(), f.admin)
	dir := t.TempDir()
	files := resources.New(filepath.Join(dir, "resources"))
	id := strings.Repeat("a", 32)
	empty, err := files.Create(owner, id)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := files.WriteFile(owner, id, empty.Commit, "proposal.md", []byte("# Proposal\nFor the launch team.\n"))
	if err != nil {
		t.Fatal(err)
	}
	resource, err := f.store.CreateResource(owner, storage.Resource{ID: id, Name: "Shared proposal", CurrentCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	f.room.ResourceID = resource.ID
	f.room, err = f.store.UpdateRoom(owner, f.room)
	if err != nil {
		t.Fatal(err)
	}
	service, err := collaboration.New(owner, f.store, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	f.api.rooms = service
	path := "/api/rooms/" + f.room.ID + "/reviews"
	input := storage.CreateReviewInput{RequestID: "create-review", Title: "Launch proposal", ResourceID: resource.ID, Path: "proposal.md", Commit: commit, ReviewerUserID: f.member.ID}
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/reviews", nil), 401)
	requireHTTP(t, teamRequest(t, f, nil, "POST", path, input), 401)
	missing := input
	missing.Path = "missing.md"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, missing), 404)
	traversal := input
	traversal.Path = "../secret"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, traversal), 400)
	response := teamRequest(t, f, f.adminCookie, "POST", path, input)
	requireHTTP(t, response, 200)
	var requested storage.ReviewResult
	if err = json.Unmarshal(response.Body.Bytes(), &requested); err != nil {
		t.Fatal(err)
	}
	actionPath := "/api/reviews/" + requested.Review.ID + "/actions"
	action := storage.ReviewActionInput{RequestID: "approve", Revision: 1, Action: "approve"}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", actionPath, action), 403)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", actionPath, action), 200)
	repeat := teamRequest(t, f, f.memberCookie, "POST", actionPath, action)
	requireHTTP(t, repeat, 200)
	if !strings.Contains(repeat.Body.String(), `"replayed":true`) {
		t.Fatal("approval receipt not replayed")
	}
	result := teamRequest(t, f, f.memberCookie, "GET", "/api/reviews?roomId="+f.room.ID+"&resourceId="+resource.ID, nil)
	requireHTTP(t, result, 200)
	if !strings.Contains(result.Body.String(), `"status":"approved"`) {
		t.Fatal(result.Body.String())
	}
	action = storage.ReviewActionInput{RequestID: "deliver", Revision: 2, Action: "deliver"}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", actionPath, action), 403)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", actionPath, action), 200)
	download := teamRequest(t, f, f.memberCookie, "GET", "/api/resources/"+resource.ID+"/file?path=proposal.md&commit="+commit, nil)
	requireHTTP(t, download, 200)
	if download.Body.String() != "# Proposal\nFor the launch team.\n" {
		t.Fatal("delivered bytes differ")
	}
	for _, cookie := range []*http.Cookie{f.adminCookie, f.memberCookie} {
		inbox := teamRequest(t, f, cookie, "GET", "/api/me/inbox", nil)
		requireHTTP(t, inbox, 200)
		if !strings.Contains(inbox.Body.String(), `"reviewId":"`+requested.Review.ID+`"`) {
			t.Fatal("review notification lacks navigation", inbox.Body.String())
		}
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/reviews?roomId=x&roomId=y", nil), 400)
}

func TestReviewOriginalUploadCannotBeSubmittedAndActionsStayExplicit(t *testing.T) {
	f := newTeamFixture(t)
	owner := storage.WithUser(context.Background(), f.admin)
	source, err := f.store.RegisterRoomUpload(owner, f.room.ID, "original", strings.Repeat("b", 32), strings.Repeat("c", 40), "brief.md", 3, strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	input := storage.CreateReviewInput{RequestID: "original-review", Title: "Brief", ResourceID: source.Reference.ResourceID, Path: source.Reference.Path, Commit: source.Reference.Commit, ReviewerUserID: f.member.ID}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/reviews", input), 400)
	input.ResourceID = f.room.ResourceID
	input.Commit = strings.Repeat("a", 40)
	response := teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/reviews", input)
	requireHTTP(t, response, 200)
	var created storage.ReviewResult
	_ = json.Unmarshal(response.Body.Bytes(), &created)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/reviews/"+created.Review.ID+"/actions", map[string]any{"requestId": "agent", "revision": 1, "action": "approve", "agentId": "assistant"}), 400)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", "/api/me/drafts/review", map[string]any{"key": "decision", "revision": 0, "value": map[string]any{"comment": "Private notes"}}), 200)
	other := teamRequest(t, f, f.memberCookie, "GET", "/api/me/drafts/review?key=decision", nil)
	requireHTTP(t, other, 200)
	if strings.Contains(other.Body.String(), "Private notes") {
		t.Fatal("private review notes leaked")
	}
}
