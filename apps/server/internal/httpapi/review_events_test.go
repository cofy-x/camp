package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestReviewSummaryHTTPPreservesLegacyReceiptAndExplicitAudit(t *testing.T) {
	f := newTeamFixture(t)
	owner := storage.WithUser(context.Background(), f.admin)
	resource, err := f.store.GetResource(owner, f.room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	create := storage.CreateReviewInput{RequestID: "small-create", Title: "Decision", ResourceID: resource.ID, Path: "proposal.md", Commit: resource.CurrentCommit, ReviewerUserID: f.member.ID}
	original, err := f.store.CreateReview(owner, f.room.ID, create)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/reviews/" + original.Review.ID
	action := storage.ReviewActionInput{RequestID: "changes", Revision: 1, Action: "request_changes", Comment: "Keep this original feedback."}
	response := teamRequest(t, f, f.memberCookie, "POST", path+"/actions?view=summary", action)
	requireHTTP(t, response, 200)
	var small struct {
		Review   storage.Review
		Feedback *storage.ReviewEvent
		Replayed bool
	}
	if err = json.Unmarshal(response.Body.Bytes(), &small); err != nil || small.Feedback == nil || small.Feedback.Comment != action.Comment {
		t.Fatal("feedback not independently readable", err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(response.Body.Bytes(), &fields); err != nil || fields["events"] != nil {
		t.Fatal("summary carried audit", err)
	}
	legacy := teamRequest(t, f, f.memberCookie, "POST", path+"/actions", action)
	requireHTTP(t, legacy, 200)
	var full storage.ReviewResult
	if err = json.Unmarshal(legacy.Body.Bytes(), &full); err != nil || !full.Replayed || len(full.Events) != 2 {
		t.Fatal("view changed receipt identity", err)
	}
	replay := teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/reviews?view=summary", create)
	requireHTTP(t, replay, 200)
	if err = json.Unmarshal(replay.Body.Bytes(), &small); err != nil || !small.Replayed || small.Review.Revision != 1 {
		t.Fatal("original creation replay lost", err)
	}
	events := teamRequest(t, f, f.memberCookie, "GET", path+"/events?through=2&limit=1", nil)
	requireHTTP(t, events, 200)
	var page storage.ReviewEventPage
	if err = json.Unmarshal(events.Body.Bytes(), &page); err != nil || len(page.Events) != 1 || page.Events[0].Comment != action.Comment || page.NextCursor == "" || page.Cursor == "" || events.Body.Len() > 128<<10 {
		t.Fatal("audit page contract failed", err)
	}
	for _, query := range []string{"view=full", "view=summary&view=summary", "view=", "unknown=1"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"?"+query, nil), 400)
	}
	for _, query := range []string{"through=0", "through=3", "through=1&through=2", "limit=0", "limit=21", "cursor=bad", "through=-1", "unknown=1"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"/events?"+query, nil), 400)
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", path+"/events", nil), 401)
}
