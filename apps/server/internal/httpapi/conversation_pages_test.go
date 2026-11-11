package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestConversationWindowAndActivityAPI(t *testing.T) {
	h, _, room := roomAPI(t)
	prefix := "/api/rooms/" + room.ID
	sent := roomRequest(t, h, "POST", prefix+"/messages", `{"requestId":"window","content":"@assistant Work"}`)
	requireHTTP(t, sent, http.StatusOK)
	var result storage.SendResult
	if err := json.Unmarshal(sent.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	response := roomRequest(t, h, "GET", prefix+"/messages?view=window&around="+url.QueryEscape(result.Message.ID), "")
	requireHTTP(t, response, http.StatusOK)
	var page storage.MessageWindow
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Messages) != 1 || page.HasOlder || page.HasNewer || page.Through != result.Message.Seq {
		t.Fatal(response.Body, err)
	}
	response = roomRequest(t, h, "GET", prefix+"/runs?view=conversation&messageIds="+url.QueryEscape(result.Message.ID), "")
	requireHTTP(t, response, http.StatusOK)
	var activity storage.ConversationActivity
	if err := json.Unmarshal(response.Body.Bytes(), &activity); err != nil || len(activity.Items) != 1 || activity.Items[0].ID != result.Runs[0].ID {
		t.Fatal(response.Body, err)
	}
	for _, query := range []string{"view=window&before=-1", "view=window&before=01", "view=window&before=0", "view=window&after=1&before=2", "view=window&around=", "view=window&around=a&around=b", "view=window&extra=1", "view=window&view=window"} {
		requireHTTP(t, roomRequest(t, h, "GET", prefix+"/messages?"+query, ""), 400)
	}
	for _, query := range []string{"view=conversation&messageIds=a,,b", "view=conversation&messageIds=a&messageIds=b", "view=conversation&cursor=x"} {
		requireHTTP(t, roomRequest(t, h, "GET", prefix+"/runs?"+query, ""), 400)
	}
	requireHTTP(t, roomRequest(t, h, "GET", prefix+"/messages?view=window&around=missing", ""), 404)
	requireHTTP(t, roomRequest(t, h, "GET", prefix+"/tasks?view=page", ""), 200)
	requireHTTP(t, roomRequest(t, h, "GET", prefix+"/tasks?view=page&attention=1", ""), 200)
	requireHTTP(t, roomRequest(t, h, "GET", prefix+"/tasks?view=page&cursor=bad", ""), 400)
}

func TestConversationPagesRequireEnabledMember(t *testing.T) {
	f := newTeamFixture(t)
	for _, suffix := range []string{"/messages?view=window", "/runs?view=conversation&messageIds=missing", "/tasks?view=page"} {
		path := "/api/rooms/" + f.room.ID + suffix
		requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), http.StatusUnauthorized)
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusOK)
	}
}
