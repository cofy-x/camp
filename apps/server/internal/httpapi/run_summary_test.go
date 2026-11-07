package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestRunSummaryAndReplyKeepOriginalDetailsExplicit(t *testing.T) {
	h, store, room := roomAPI(t)
	sent := roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", `{"requestId":"single-summary","content":"@assistant Work"}`)
	requireHTTP(t, sent, http.StatusOK)
	var result storage.SendResult
	if err := json.Unmarshal(sent.Body.Bytes(), &result); err != nil || len(result.Runs) != 1 {
		t.Fatal(err)
	}
	run := result.Runs[0]
	run.Output = strings.Repeat("PRIVATE_OUTPUT", 18000)
	run.Files = []storage.RunFile{{Path: "app.txt", Status: "added", After: strings.Repeat("PRIVATE_FILE", 50000)}}
	var err error
	if run, err = store.UpdateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	prefix := "/api/runs/" + run.ID
	summary := roomRequest(t, h, "GET", prefix+"?view=summary", "")
	requireHTTP(t, summary, http.StatusOK)
	var body struct{ Run storage.RunTimeline }
	if err = json.Unmarshal(summary.Body.Bytes(), &body); err != nil || body.Run.ID != run.ID || body.Run.FileCount != 1 || body.Run.Files[0].Path != "app.txt" || summary.Body.Len() > 2048 {
		t.Fatal(summary.Body, err)
	}
	for _, field := range []string{"PRIVATE_", `"output":`, `"before":`, `"after":`, `"instructions":`, `"references":`, `"model":`} {
		if strings.Contains(summary.Body.String(), field) {
			t.Fatal("original details leaked into summary", field)
		}
	}
	full := roomRequest(t, h, "GET", prefix, "")
	requireHTTP(t, full, http.StatusOK)
	if !strings.Contains(full.Body.String(), "PRIVATE_OUTPUT") || !strings.Contains(full.Body.String(), "PRIVATE_FILE") {
		t.Fatal("default full response changed")
	}
	for _, query := range []string{"?view=wrong", "?view=", "?view=summary&view=summary", "?view=summary&other=x"} {
		requireHTTP(t, roomRequest(t, h, "GET", prefix+query, ""), http.StatusBadRequest)
	}
	response := roomRequest(t, h, "GET", prefix+"/reply", "")
	requireHTTP(t, response, http.StatusOK)
	if strings.TrimSpace(response.Body.String()) != `{"message":null}` {
		t.Fatal("unanswered Run invented a reply", response.Body)
	}
	run.Status = "succeeded"
	if _, err = store.FinishRun(context.Background(), run, "Saved original reply.", false); err != nil {
		t.Fatal(err)
	}
	response = roomRequest(t, h, "GET", prefix+"/reply", "")
	requireHTTP(t, response, http.StatusOK)
	var reply struct{ Message storage.Message }
	if err = json.Unmarshal(response.Body.Bytes(), &reply); err != nil || reply.Message.RunID != run.ID || reply.Message.Content != "Saved original reply." || reply.Message.AuthorKind != "agent" {
		t.Fatal(response.Body, err)
	}
	for _, path := range []string{"/api/runs/missing?view=summary", "/api/runs/missing/reply", "/api/runs/missing/events"} {
		requireHTTP(t, roomRequest(t, h, "GET", path, ""), http.StatusNotFound)
	}
}

type fullOnlyRoomService struct {
	fixtureRoomService
	getCalls int
}

func (f *fullOnlyRoomService) Get(context.Context, string) (storage.Run, error) {
	f.getCalls++
	return storage.Run{}, nil
}

func TestRunSummaryDoesNotFallBackToManagedFullGet(t *testing.T) {
	_, store, _ := roomAPI(t)
	service := &fullOnlyRoomService{fixtureRoomService: fixtureRoomService{store}}
	h := New(store, nil, service)
	requireHTTP(t, roomRequest(t, h, "GET", "/api/runs/one?view=summary", ""), http.StatusServiceUnavailable)
	if service.getCalls != 0 {
		t.Fatal("summary materialized a managed full Run")
	}
}

func TestRunSummaryAndReplyRequireCurrentEnabledMember(t *testing.T) {
	f := newTeamFixture(t)
	sent, err := f.store.SendMessage(storage.WithUser(context.Background(), f.admin), f.room.ID, storage.SendMessageInput{RequestID: "summary-auth", Content: "Work", RecipientAgentIDs: []string{f.room.DefaultAgentID}}, storage.Budget{MaxTokens: 32, MaxSteps: 2, TimeoutSeconds: 2})
	if err != nil || len(sent.Runs) != 1 {
		t.Fatal(err)
	}
	paths := []string{"/api/runs/" + sent.Runs[0].ID + "?view=summary", "/api/runs/" + sent.Runs[0].ID + "/reply"}
	for _, path := range paths {
		requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), http.StatusUnauthorized)
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusOK)
	}
	disabled := false
	if _, err = f.store.UpdateUserAccess(storage.WithUser(context.Background(), f.admin), f.member.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusUnauthorized)
	}
}
