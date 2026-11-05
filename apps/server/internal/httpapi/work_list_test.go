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

func TestWorkDesktopHTTPDefaultsCursorAndCurrentAuthority(t *testing.T) {
	f := newTeamFixture(t)
	memberContext := storage.WithUser(context.Background(), f.member)
	for index := range 2 {
		if _, err := f.store.CreateWorkItem(memberContext, f.room.ID, orchestration.WorkItemInput{RequestID: fmt.Sprintf("member-work-%d", index), Goal: fmt.Sprintf("Member work %d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 3 {
		requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/work-items", map[string]any{"requestId": fmt.Sprintf("admin-work-%d", index), "goal": fmt.Sprintf("Admin work %d", index)}), 200)
	}
	read := func(cookie *http.Cookie, query string) storage.WorkListPage {
		t.Helper()
		response := teamRequest(t, f, cookie, "GET", "/api/work-items"+query, nil)
		requireHTTP(t, response, 200)
		var page storage.WorkListPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/work-items", nil), 401)
	first := read(f.memberCookie, "?limit=1")
	if len(first.Items) != 1 || first.Items[0].Goal != "Member work 1" || first.Items[0].RoomName != f.room.Name || first.Items[0].NeedsYou || !first.HasMore {
		t.Fatal(first)
	}
	second := read(f.memberCookie, "?limit=1&cursor="+url.QueryEscape(first.NextCursor))
	if len(second.Items) != 1 || second.Items[0].Goal != "Member work 0" || second.HasMore || second.NextCursor != "" {
		t.Fatal(second)
	}
	if needs := read(f.memberCookie, "?view=needs_you"); len(needs.Items) != 0 {
		t.Fatal("open was treated as requiring judgment", needs)
	}
	if all := read(f.memberCookie, "?scope=team&view=all&roomId="+f.room.ID); len(all.Items) != 5 {
		t.Fatal(all)
	}
	for _, query := range []string{
		"limit=0", "limit=21", "limit=-1", "limit=bad", "limit=1&limit=2",
		"view=unknown", "view=", "view=all&view=done", "scope=all", "scope=", "scope=mine&scope=team",
		"roomId=a&roomId=b", "cursor=bad", "cursor=a&cursor=b", "state=all", "after=1",
		"scope=team&cursor=" + url.QueryEscape(first.NextCursor),
		"view=all&cursor=" + url.QueryEscape(first.NextCursor),
		"roomId=" + f.room.ID + "&cursor=" + url.QueryEscape(first.NextCursor),
	} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/work-items?"+query, nil), 400)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/work-items?cursor="+url.QueryEscape(first.NextCursor), nil), 400)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/work-items?roomId=missing", nil), 404)
	disabled := false
	if _, err := f.store.UpdateUserAccess(storage.WithUser(context.Background(), f.admin), f.member.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/work-items?cursor="+url.QueryEscape(first.NextCursor), nil), 401)
	if all := read(f.adminCookie, "?scope=team&view=all"); len(all.Items) != 5 {
		t.Fatal("disabled owner’s retained matters disappeared", all)
	}
}
