package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type fixtureRoomService struct{ store *storage.Store }

func (f fixtureRoomService) CreateRoom(ctx context.Context, name, description string) (storage.Room, error) {
	rooms, _ := f.store.ListRooms(ctx)
	return f.store.CreateRoom(ctx, storage.Room{Name: name, Description: description, ResourceID: rooms[0].ResourceID})
}
func (f fixtureRoomService) ImportResource(ctx context.Context, name, path string, env json.RawMessage) (storage.Resource, error) {
	return f.store.CreateResource(ctx, storage.Resource{Name: name, SourcePath: path, CurrentCommit: strings.Repeat("b", 40), Environment: env})
}
func (f fixtureRoomService) Send(ctx context.Context, id string, in storage.SendMessageInput) (storage.SendResult, error) {
	return f.store.SendMessage(ctx, id, in, storage.Budget{MaxTokens: 4096, MaxSteps: 8, TimeoutSeconds: 180})
}
func (f fixtureRoomService) Cancel(ctx context.Context, id string) (storage.Run, error) {
	r, e := f.store.GetRun(ctx, id)
	if e != nil {
		return r, e
	}
	r.Status = "cancelled"
	return f.store.FinishRun(ctx, r, "Cancelled.", false)
}
func (f fixtureRoomService) Leave(ctx context.Context, room, agent string) error {
	return f.store.LeaveRoom(ctx, room, agent)
}
func (f fixtureRoomService) SaveAgent(ctx context.Context, a storage.Agent) (storage.Agent, error) {
	return f.store.SaveAgent(ctx, a)
}
func (f fixtureRoomService) CreateRoomAgent(ctx context.Context, room string, a storage.Agent) (storage.Agent, error) {
	return f.store.CreateRoomAgent(ctx, room, a)
}
func (f fixtureRoomService) ResourceFiles(ctx context.Context, id, commit string) ([]string, string, error) {
	r, err := f.store.ValidateResourceRevision(ctx, id, commit)
	if err != nil {
		return nil, "", err
	}
	if commit == "" {
		commit = r.CurrentCommit
	}
	return []string{"report.md"}, commit, nil
}
func (f fixtureRoomService) ResourceFile(ctx context.Context, id, path, commit string) ([]byte, error) {
	_, err := f.store.ValidateResourceRevision(ctx, id, commit)
	return []byte("Report"), err
}
func (f fixtureRoomService) RunPatch(context.Context, string) ([]byte, error) {
	return []byte("diff --git a/report.md b/report.md\n"), nil
}
func (f fixtureRoomService) Status() any { return map[string]any{"available": true} }
func roomAPI(t *testing.T) (http.Handler, *storage.Store, storage.Room) {
	t.Helper()
	s, e := storage.Open(filepath.Join(t.TempDir(), "rooms.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	r, e := s.SeedRoom(context.Background(), storage.Resource{ID: "files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if e != nil {
		t.Fatal(e)
	}
	return New(s, nil, fixtureRoomService{s}), s, r
}
func roomRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://localhost:3001"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:40000"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestRoomAPIImmediateConversationAndMembership(t *testing.T) {
	h, _, room := roomAPI(t)
	w := roomRequest(t, h, "GET", "/api/rooms", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "general") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", `{"requestId":"first","content":"@assistant Write a short note."}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var sent storage.SendResult
	if e := json.Unmarshal(w.Body.Bytes(), &sent); e != nil {
		t.Fatal(e)
	}
	if len(sent.Runs) != 1 || sent.Runs[0].Status != "queued" {
		t.Fatal(sent)
	}
	repeat := roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", `{"requestId":"first","content":"@assistant Write a short note."}`)
	if !strings.Contains(repeat.Body.String(), `"replayed":true`) {
		t.Fatal(repeat.Body)
	}
	w = roomRequest(t, h, "POST", "/api/agents", `{"name":"Reviewer","handle":"reviewer","access":"read"}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	var response struct{ Agent storage.Agent }
	if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
		t.Fatal(e)
	}
	w = roomRequest(t, h, "PUT", "/api/rooms/"+room.ID+"/members/"+response.Agent.ID, `{}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	w = roomRequest(t, h, "PATCH", "/api/rooms/"+room.ID, `{"defaultAgentId":"`+response.Agent.ID+`"}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	w = roomRequest(t, h, "DELETE", "/api/rooms/"+room.ID+"/members/"+response.Agent.ID, "")
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	w = roomRequest(t, h, "POST", "/api/runs/"+sent.Runs[0].ID+"/cancel", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"cancelled"`) {
		t.Fatal(w.Body)
	}
}

func TestCreateRoomAgentAPI(t *testing.T) {
	h, store, room := roomAPI(t)
	path := "/api/rooms/" + room.ID + "/agents"
	w := roomRequest(t, h, "POST", path, `{"name":"Writer","handle":"writer","instructions":"Write concise documents."}`)
	var response struct{ Agent storage.Agent }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || response.Agent.ID == "" || !response.Agent.Enabled || response.Agent.Access != "write" || response.Agent.Icon != "agent" {
		t.Fatalf("creation: %d %s %v", w.Code, w.Body, err)
	}
	members, err := store.ListMembers(context.Background(), room.ID)
	if err != nil || len(members) != 2 || (members[0].ID != response.Agent.ID && members[1].ID != response.Agent.ID) {
		t.Fatalf("membership: %+v %v", members, err)
	}
	updated, err := store.GetRoom(context.Background(), room.ID)
	if err != nil || updated.DefaultAgentID != room.DefaultAgentID {
		t.Fatalf("default agent changed: %+v %v", updated, err)
	}
	runs, err := store.ListRoomRuns(context.Background(), room.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("creation submitted work: %+v %v", runs, err)
	}
	for _, test := range []struct {
		path, body, code string
		status           int
	}{
		{"/api/rooms/missing/agents", `{"name":"New","handle":"new"}`, "not_found", 404},
		{path, `{"name":"New","handle":"not a handle"}`, "invalid_agent", 400},
		{path, `{"name":"New","handle":"new","enabled":false}`, "invalid_agent", 400},
		{path, `{"name":"Writer","handle":"writer"}`, "name_conflict", 409},
		{path, `{"name":"New","handle":"new","id":"client-id"}`, "invalid_json", 400},
	} {
		w = roomRequest(t, h, "POST", test.path, test.body)
		if w.Code != test.status || !strings.Contains(w.Body.String(), `"code":"`+test.code+`"`) {
			t.Fatalf("%s %s: %d %s", test.path, test.body, w.Code, w.Body)
		}
	}
	agents, err := store.ListAgents(context.Background())
	if err != nil || len(agents) != 2 {
		t.Fatalf("failed request left an agent: %+v %v", agents, err)
	}
}

func TestRoomAPIDisplayNames(t *testing.T) {
	h, _, _ := roomAPI(t)
	w := roomRequest(t, h, "POST", "/api/rooms", `{"name":" Launch studio ","description":"Prepare the next launch."}`)
	var response struct{ Room storage.Room }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || response.Room.Name != "Launch studio" {
		t.Fatalf("display name create: %d %s %v", w.Code, w.Body, err)
	}
	w = roomRequest(t, h, "PATCH", "/api/rooms/"+response.Room.ID, `{"name":" Café planning "}`)
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || response.Room.Name != "Café planning" {
		t.Fatalf("display name rename: %d %s %v", w.Code, w.Body, err)
	}
	for _, method := range []string{"POST", "PATCH"} {
		path := "/api/rooms"
		if method == "PATCH" {
			path += "/" + response.Room.ID
		}
		w = roomRequest(t, h, method, path, `{"name":"Invalid\nroom"}`)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"invalid_room"`) {
			t.Fatalf("invalid display name: %d %s", w.Code, w.Body)
		}
	}
}
func TestRoomHTTPValidationAndLocalBoundary(t *testing.T) {
	h, _, room := roomAPI(t)
	for _, test := range []struct {
		method, path, body string
		want               int
	}{{"POST", "/api/rooms/" + room.ID + "/messages", `{"requestId":"a","content":"x","legacyMode":"build"}`, 400}, {"POST", "/api/rooms/" + room.ID + "/messages", `{"requestId":"b","content":"x","recipientAgentIds":["missing"]}`, 400}, {"GET", "/api/rooms/" + room.ID + "/messages?after=-1", "", 400}, {"GET", "/api/workspace/events?after=bad", "", 400}, {"PATCH", "/api/agents/" + room.DefaultAgentID, `{"name":"New"}`, 400}, {"GET", "/api/projects", "", 404}, {"GET", "/api/channels", "", 404}} {
		w := roomRequest(t, h, test.method, test.path, test.body)
		if w.Code != test.want {
			t.Errorf("%s %s: %d %s", test.method, test.path, w.Code, w.Body)
		}
	}
	for _, origin := range []string{"http://evil.test", "http://localhost:3001/path"} {
		r := httptest.NewRequest("GET", "http://localhost:3001/api/rooms", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(origin, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://localhost:3001/api/rooms", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestWorkspaceSSEReconnectUsesCommittedCursor(t *testing.T) {
	h, s, room := roomAPI(t)
	server := httptest.NewServer(h)
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	events, e := s.ListWorkspaceEvents(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	last := events[len(events)-1].ID
	request, e := http.NewRequest("GET", server.URL+"/api/workspace/events", nil)
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Last-Event-ID", jsonNumber(last))
	response, e := client.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(response.StatusCode)
	}
	if _, e = s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RequestID: "sse", Content: "Saved first", PostOnly: true}, storage.Budget{MaxTokens: 100, MaxSteps: 1, TimeoutSeconds: 10}); e != nil {
		t.Fatal(e)
	}
	scanner := bufio.NewScanner(response.Body)
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var event storage.WorkspaceEvent
			if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); e != nil {
				t.Fatal(e)
			}
			if event.ID <= last || event.Type != "message.created" || event.RoomID != room.ID {
				t.Fatal(event)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing event: %v", scanner.Err())
	}
}
func jsonNumber(v int64) string { b, _ := json.Marshal(v); return string(b) }
func TestResourceDownloadAndPatch(t *testing.T) {
	h, _, room := roomAPI(t)
	for _, path := range []string{"/api/resources/" + room.ResourceID + "/file?path=report.md", "/api/runs/example/patch"} {
		w := roomRequest(t, h, "GET", path, "")
		b, _ := io.ReadAll(w.Result().Body)
		if w.Code != 200 || len(b) == 0 || w.Header().Get("Content-Disposition") == "" {
			t.Fatalf("%s: %d %q", path, w.Code, b)
		}
	}
}

func TestReferenceAPISnapshotsAndRoomScopedSourceNavigation(t *testing.T) {
	h, store, room := roomAPI(t)
	response := roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", `{"requestId":"source","content":"A source discussion.","postOnly":true}`)
	var source storage.SendResult
	if err := json.Unmarshal(response.Body.Bytes(), &source); err != nil || response.Code != 200 {
		t.Fatalf("source %d %s: %v", response.Code, response.Body, err)
	}
	body := `{"requestId":"review","content":"Review the source.","references":[{"kind":"message","messageId":"` + source.Message.ID + `"}]}`
	response = roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", body)
	var accepted storage.SendResult
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil || response.Code != 200 || len(accepted.Message.References) != 1 || accepted.Message.References[0].Content != source.Message.Content || accepted.Message.References[0].Author != "Team member" {
		t.Fatalf("accepted %d %s: %v", response.Code, response.Body, err)
	}
	response = roomRequest(t, h, "GET", "/api/rooms/"+room.ID+"/messages/"+source.Message.ID, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "A source discussion.") {
		t.Fatalf("source lookup %d %s", response.Code, response.Body)
	}
	other, err := store.CreateRoom(context.Background(), storage.Room{Name: "other", ResourceID: room.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	response = roomRequest(t, h, "GET", "/api/rooms/"+other.ID+"/messages/"+source.Message.ID, "")
	if response.Code != 404 {
		t.Fatalf("cross-room lookup %d %s", response.Code, response.Body)
	}
	for _, invalid := range []string{
		`{"requestId":"forged","content":"Review","references":[{"kind":"message","messageId":"` + source.Message.ID + `","content":"Forged evidence"}]}`,
		`{"requestId":"missing","content":"Review","references":[{"kind":"message","messageId":"missing"}]}`,
		`{"requestId":"revision","content":"Review","references":[{"kind":"file","resourceId":"` + room.ResourceID + `","commit":"HEAD","path":"report.md"}]}`,
	} {
		response = roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", invalid)
		if response.Code != 400 {
			t.Fatalf("accepted invalid evidence %d %s", response.Code, response.Body)
		}
	}
}

func TestPinnedResourceFilesRejectUnknownRevisionExpressions(t *testing.T) {
	h, _, room := roomAPI(t)
	response := roomRequest(t, h, "GET", "/api/resources/"+room.ResourceID+"/files", "")
	var listing struct {
		Files  []string `json:"files"`
		Commit string   `json:"commit"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil || response.Code != 200 || listing.Commit != strings.Repeat("a", 40) || len(listing.Files) != 1 {
		t.Fatalf("listing %d %s: %v", response.Code, response.Body, err)
	}
	for _, route := range []string{"/files", "/file?path=report.md"} {
		separator := "?"
		if strings.Contains(route, "?") {
			separator = "&"
		}
		for _, commit := range []string{listing.Commit, "HEAD", strings.Repeat("b", 40), listing.Commit + "&commit=" + listing.Commit} {
			response = roomRequest(t, h, "GET", "/api/resources/"+room.ResourceID+route+separator+"commit="+commit, "")
			want := 400
			if commit == listing.Commit {
				want = 200
			}
			if response.Code != want {
				t.Fatalf("%s %s: %d %s", route, commit, response.Code, response.Body)
			}
		}
	}
}
