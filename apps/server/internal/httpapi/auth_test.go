package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
	"github.com/cofy-x/camp/apps/server/internal/team"
)

type teamFixture struct {
	api                       *API
	store                     *storage.Store
	auth                      *team.Service
	admin, member             storage.User
	adminCookie, memberCookie *http.Cookie
	room                      storage.Room
}

func newTeamFixture(t *testing.T) teamFixture {
	t.Helper()
	dir := t.TempDir()
	s, e := storage.Open(filepath.Join(dir, "team.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	auth, e := team.Open(context.Background(), s, dir)
	if e != nil {
		t.Fatal(e)
	}
	token, e := os.ReadFile(auth.SetupPath())
	if e != nil {
		t.Fatal(e)
	}
	admin, session, e := auth.Setup(context.Background(), strings.TrimSpace(string(token)), "owner", "Owner", "a strong test password")
	if e != nil {
		t.Fatal(e)
	}
	_, invite, e := auth.CreateInvite(storage.WithUser(context.Background(), admin), 1)
	if e != nil {
		t.Fatal(e)
	}
	member, session2, e := auth.AcceptInvite(context.Background(), invite, "writer", "Writer", "another good password")
	if e != nil {
		t.Fatal(e)
	}
	room, e := s.SeedRoom(context.Background(), storage.Resource{ID: "files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if e != nil {
		t.Fatal(e)
	}
	api := New(s, nil, fixtureRoomService{s})
	api.EnableTeam(auth, nil)
	t.Cleanup(api.StopStreams)
	return teamFixture{api, s, auth, admin, member, &http.Cookie{Name: sessionCookie, Value: session}, &http.Cookie{Name: sessionCookie, Value: session2}, room}
}
func teamRequest(t *testing.T, f teamFixture, cookie *http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(string(encoded)))
	request.RemoteAddr = "127.0.0.1:1000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	f.api.ServeHTTP(response, request)
	return response
}
func requireHTTP(t *testing.T, r *httptest.ResponseRecorder, status int) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("HTTP %d want %d: %s", r.Code, status, r.Body.String())
	}
}
func TestTeamAuthenticationAttributionAndPermissions(t *testing.T) {
	f := newTeamFixture(t)
	for _, path := range []string{"/api/bootstrap", "/api/rooms", "/api/workspace/events", "/api/resources/files/file?path=a", "/api/me/drafts?kind=document", "/api/team/members"} {
		requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), 401)
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/health", nil), 200)
	for _, item := range []struct {
		method, path string
		body         any
	}{{"POST", "/api/resources", map[string]any{}}, {"PATCH", "/api/resources/files", map[string]any{}}, {"POST", "/api/team/invites", map[string]any{}}, {"PATCH", "/api/team/members/" + f.admin.ID, map[string]any{"enabled": false}}} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, item.method, item.path, item.body), 403)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/runs/x/preview", nil), 404)
	path := "/api/rooms/" + f.room.ID + "/messages"
	first := teamRequest(t, f, f.adminCookie, "POST", path, map[string]any{"requestId": "same", "content": "Hello @writer"})
	requireHTTP(t, first, 200)
	second := teamRequest(t, f, f.memberCookie, "POST", path, map[string]any{"requestId": "same", "content": "Hello @owner"})
	requireHTTP(t, second, 200)
	var a, b storage.SendResult
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(second.Body.Bytes(), &b)
	if a.Message.ID == b.Message.ID || a.Message.AuthorUserID != f.admin.ID || b.Message.AuthorName != "Writer" || len(a.Runs) != 0 || len(b.Runs) != 0 {
		t.Fatalf("attribution or ordinary discussion: %+v %+v", a, b)
	}
	repeat := teamRequest(t, f, f.memberCookie, "POST", path, map[string]any{"requestId": "same", "content": "Hello @owner"})
	requireHTTP(t, repeat, 200)
	if !strings.Contains(repeat.Body.String(), `"replayed":true`) {
		t.Fatal(repeat.Body.String())
	}
	work := teamRequest(t, f, f.memberCookie, "POST", path, map[string]any{"requestId": "work", "content": "@assistant write a note"})
	requireHTTP(t, work, 200)
	var turn storage.SendResult
	_ = json.Unmarshal(work.Body.Bytes(), &turn)
	if len(turn.Runs) != 1 || turn.Runs[0].RequestedByUserID != f.member.ID {
		t.Fatal(work.Body.String())
	}
	ownRun := turn.Runs[0].ID
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/runs/"+ownRun+"/cancel", map[string]any{}), 200)
	adminWork := teamRequest(t, f, f.adminCookie, "POST", path, map[string]any{"requestId": "admin-work", "content": "@assistant inspect files"})
	requireHTTP(t, adminWork, 200)
	_ = json.Unmarshal(adminWork.Body.Bytes(), &turn)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/runs/"+turn.Runs[0].ID+"/cancel", map[string]any{}), 403)
	inbox := teamRequest(t, f, f.memberCookie, "GET", "/api/me/inbox", nil)
	requireHTTP(t, inbox, 200)
	if !strings.Contains(inbox.Body.String(), `"mention"`) {
		t.Fatal(inbox.Body.String())
	}
	request := httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(`{"username":"owner","password":"a strong test password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.api.ServeHTTP(response, request)
	requireHTTP(t, response, 403)
}
func TestTeamPrivateStateAndDisableRevocation(t *testing.T) {
	f := newTeamFixture(t)
	draft := map[string]any{"key": "room:thread", "revision": 0, "value": map[string]any{"content": "private draft"}}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", "/api/me/drafts/conversation", draft), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", "/api/me/drafts/conversation", draft), 409)
	memberDraft := teamRequest(t, f, f.memberCookie, "GET", "/api/me/drafts/conversation?key=room:thread", nil)
	requireHTTP(t, memberDraft, 200)
	if strings.Contains(memberDraft.Body.String(), "private draft") {
		t.Fatal("another member can read private draft")
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", "/api/preferences", map[string]any{"wallpaper": "solid", "iconSnap": false, "controlStrip": false}), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", "/api/layout", map[string]any{"clientId": "same-browser", "layout": map[string]any{"private": "owner layout"}}), 200)
	boot := teamRequest(t, f, f.memberCookie, "GET", "/api/bootstrap?clientId=same-browser", nil)
	requireHTTP(t, boot, 200)
	if strings.Contains(boot.Body.String(), "owner layout") || !strings.Contains(boot.Body.String(), `"wallpaper":"lavender"`) {
		t.Fatal(boot.Body.String())
	}
	stream, stop := f.auth.WatchSession(context.Background(), f.memberCookie.Value)
	defer stop()
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/team/members/"+f.member.ID, map[string]any{"enabled": false}), 200)
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("disable did not revoke stream")
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/rooms", nil), 401)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/team/members/"+f.admin.ID, map[string]any{"enabled": false}), 409)
}
func TestTeamInviteOneUseLogoutAndSecureCookie(t *testing.T) {
	f := newTeamFixture(t)
	invitation := teamRequest(t, f, f.adminCookie, "POST", "/api/team/invites", map[string]any{"expiresInHours": 1})
	requireHTTP(t, invitation, 200)
	var decoded struct{ Token string }
	_ = json.Unmarshal(invitation.Body.Bytes(), &decoded)
	if decoded.Token == "" {
		t.Fatal("missing invitation")
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, username := range []string{"first", "second"} {
		wg.Go(func() {
			w := teamRequest(t, f, nil, "POST", "/api/auth/accept-invite", map[string]any{"token": decoded.Token, "username": username, "name": username, "password": "a sufficiently strong password"})
			codes <- w.Code
		})
	}
	wg.Wait()
	close(codes)
	success := 0
	for status := range codes {
		if status == 200 {
			success++
		} else if status != 400 {
			t.Fatal(status)
		}
	}
	if success != 1 {
		t.Fatal("invite was not consumed once")
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/auth/logout", map[string]any{}), 200)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/rooms", nil), 401)
	api := New(f.store, nil)
	api.EnableTeam(f.auth, nil, "https://camp.example")
	r := httptest.NewRequest("POST", "http://camp.example/api/auth/login", strings.NewReader(`{"username":"writer","password":"another good password"}`))
	r.Header.Set("Origin", "https://camp.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	requireHTTP(t, w, 200)
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal(cookie)
	}
}
func TestTeamLogoutEndsLiveSSEWithoutCancellingRun(t *testing.T) {
	f := newTeamFixture(t)
	work := teamRequest(t, f, f.memberCookie, "POST", "/api/rooms/"+f.room.ID+"/messages", map[string]any{"requestId": "work", "content": "@assistant inspect files"})
	requireHTTP(t, work, 200)
	var turn storage.SendResult
	_ = json.Unmarshal(work.Body.Bytes(), &turn)
	server := httptest.NewServer(f.api)
	defer server.Close()
	request, _ := http.NewRequest("GET", server.URL+"/api/workspace/events", nil)
	request.AddCookie(f.memberCookie)
	response, e := server.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	defer response.Body.Close()
	finished := make(chan error, 1)
	go func() { _, e := io.Copy(io.Discard, response.Body); finished <- e }()
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/auth/logout", map[string]any{}), 200)
	select {
	case e := <-finished:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("logout left the event stream open")
	}
	run, e := f.store.GetRun(context.Background(), turn.Runs[0].ID)
	if e != nil || run.Status != "queued" {
		t.Fatalf("logout cancelled work: %+v %v", run, e)
	}
}

func TestTeamPlainHTTPIsLocalAndLoginIsRateLimited(t *testing.T) {
	f := newTeamFixture(t)
	r := httptest.NewRequest("GET", "http://localhost/api/auth/status", nil)
	r.RemoteAddr = "192.0.2.1:9000"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	w := httptest.NewRecorder()
	f.api.ServeHTTP(w, r)
	requireHTTP(t, w, 403)
	for range 12 {
		requireHTTP(t, teamRequest(t, f, nil, "POST", "/api/auth/setup", map[string]any{"token": "invalid"}), 401)
	}
	limited := teamRequest(t, f, nil, "POST", "/api/auth/setup", map[string]any{"token": "invalid"})
	requireHTTP(t, limited, 429)
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit has no retry interval")
	}
}
