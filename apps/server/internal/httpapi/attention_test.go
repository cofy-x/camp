package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAttentionHTTPBusinessStateSurvivesReadAndCurrentActorControlsVisibility(t *testing.T) {
	f := newTeamFixture(t)
	member := storage.WithUser(context.Background(), f.member)
	for index := range 2 {
		item, err := f.store.CreateWorkItem(member, f.room.ID, orchestration.WorkItemInput{RequestID: fmt.Sprintf("attention-create-%d", index), Goal: "Confirm the release audience"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.OperateWorkItem(member, item.ID, orchestration.WorkItemOperation{RequestID: fmt.Sprintf("attention-ask-%d", index), Revision: item.Revision, Kind: "request_judgment", Question: "Which audience?"}, storage.Budget{}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(cookie *http.Cookie, query string) storage.AttentionPage {
		t.Helper()
		response := teamRequest(t, f, cookie, "GET", "/api/attention"+query, nil)
		requireHTTP(t, response, 200)
		var page storage.AttentionPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/attention", nil), 401)
	first := read(f.memberCookie, "?limit=1")
	if first.Total != 2 || first.Counts.Judgment != 2 || len(first.Items) != 1 || !first.HasMore || first.Items[0].Target.App != "work" {
		t.Fatal(first)
	}
	if admin := read(f.adminCookie, ""); admin.Total != 0 {
		t.Fatal("administrator capability became personal responsibility", admin)
	}
	notices, err := f.store.ListInbox(member)
	if err != nil {
		t.Fatal(err)
	}
	for _, notice := range notices {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/me/inbox/"+notice.ID+"/read", struct{}{}), 200)
	}
	if page := read(f.memberCookie, ""); page.Total != 2 {
		t.Fatal("notification reads resolved unanswered questions", page)
	}
	last := read(f.memberCookie, "?limit=1&cursor="+url.QueryEscape(first.NextCursor))
	if len(last.Items) != 1 || last.Items[0].ID == first.Items[0].ID || last.HasMore {
		t.Fatal(last)
	}
	for _, query := range []string{
		"limit=0", "limit=21", "limit=-1", "limit=invalid", "limit=1&limit=2", "kind=", "kind=unknown", "kind=all&kind=review", "roomId=a&roomId=b", "cursor=a&cursor=b", "cursor=bad", "after=1", "scope=team",
		"kind=judgment&cursor=" + url.QueryEscape(first.NextCursor),
		"roomId=" + f.room.ID + "&cursor=" + url.QueryEscape(first.NextCursor),
	} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/attention?"+query, nil), 400)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/attention?cursor="+url.QueryEscape(first.NextCursor), nil), 400)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/attention?roomId=missing", nil), 404)
	disabled := false
	if _, err = f.store.UpdateUserAccess(storage.WithUser(context.Background(), f.admin), f.member.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/attention", nil), 401)
}
