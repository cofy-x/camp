package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestReviewListHTTPPagesFilesAndRejectsMismatchedQueries(t *testing.T) {
	f := newTeamFixture(t)
	owner := storage.WithUser(context.Background(), f.admin)
	resource, err := f.store.GetResource(owner, f.room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 35 {
		path := "proposal.md"
		if i%5 == 0 {
			path = "context.md"
		}
		if _, err := f.store.CreateReview(owner, f.room.ID, storage.CreateReviewInput{RequestID: fmt.Sprintf("list-%d", i), Title: fmt.Sprintf("Proposal %d", i), ResourceID: resource.ID, Path: path, Commit: resource.CurrentCommit, ReviewerUserID: f.member.ID}); err != nil {
			t.Fatal(err)
		}
	}
	query := url.Values{"resourceId": {resource.ID}, "path": {"proposal.md"}}
	read := func(query url.Values) storage.ReviewListPage {
		t.Helper()
		response := teamRequest(t, f, f.memberCookie, "GET", "/api/reviews?"+query.Encode(), nil)
		requireHTTP(t, response, 200)
		if response.Body.Len() > 320<<10 {
			t.Fatal("unbounded HTTP review response", response.Body.Len())
		}
		var page storage.ReviewListPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, review := range page.Reviews {
			if review.Path != "proposal.md" {
				t.Fatal("another file crossed the HTTP page filter")
			}
		}
		return page
	}
	first := read(query)
	if len(first.Reviews) != 20 || first.NextCursor == "" {
		t.Fatal("first page did not carry bounded navigation", len(first.Reviews))
	}
	query.Set("cursor", first.NextCursor)
	second := read(query)
	if len(second.Reviews) != 8 || second.NextCursor != "" {
		t.Fatal("second page did not complete its file scope", len(second.Reviews))
	}
	for _, suffix := range []string{
		"limit=21", "limit=0", "limit=-1", "limit=1.5", "limit=", "limit=1&limit=2",
		"roomId=one&roomId=two", "resourceId=one&resourceId=two",
		"path=one&path=two", "cursor=one&cursor=two", "unexpected=one",
		"path=proposal.md", "path=%xx", "cursor=invalid",
		"resourceId=" + resource.ID + "&path=../private",
		"roomId=" + f.room.ID + "&cursor=" + url.QueryEscape(first.NextCursor),
	} {
		response := teamRequest(t, f, f.memberCookie, "GET", "/api/reviews?"+suffix, nil)
		requireHTTP(t, response, 400)
		var body struct{ Code string }
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Code != "invalid_page" {
			t.Fatal("invalid review page did not retain its protocol error", suffix, response.Body.String(), err)
		}
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/reviews?roomId=missing", nil), 404)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/reviews?resourceId=missing", nil), 404)
}
