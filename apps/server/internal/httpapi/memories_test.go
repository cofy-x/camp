package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestMemoryHTTPRevisionsCurrentAccessAndRecovery(t *testing.T) {
	f := newTeamFixture(t)
	path := "/api/rooms/" + f.room.ID + "/memories"
	for _, p := range []string{path, "/api/memories/missing", "/api/memory-requests/save"} {
		requireHTTP(t, teamRequest(t, f, nil, "GET", p, nil), http.StatusUnauthorized)
	}
	input := orchestration.MemoryInput{RequestID: "save", Content: "Launch in November", Provenance: orchestration.MemoryProvenance{Kind: "human_note"}}
	response := teamRequest(t, f, f.adminCookie, "POST", path, input)
	requireHTTP(t, response, 200)
	var result struct {
		Memory orchestration.Memory `json:"memory"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Memory.AuthorUserID != f.admin.ID || result.Memory.Revision != 1 {
		t.Fatal(response.Body.String(), err)
	}
	id := result.Memory.ID
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"?q=NOVEMBER", nil), 200)
	edit := input
	edit.RequestID, edit.Revision, edit.Content = "edit", 1, "Launch in December"
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "PUT", "/api/memories/"+id, edit), 403)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", "/api/memories/"+id, edit), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", "/api/memories/"+id, edit), 200)
	history := teamRequest(t, f, f.memberCookie, "GET", "/api/memories/"+id+"?revision=1", nil)
	requireHTTP(t, history, 200)
	if !strings.Contains(history.Body.String(), "November") || !strings.Contains(history.Body.String(), `"current":false`) {
		t.Fatal(history.Body.String())
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/memories/"+id+"/retract", orchestration.MemoryRetraction{RequestID: "retract", Revision: 2}), 200)
	retracted := teamRequest(t, f, f.memberCookie, "GET", path, nil)
	requireHTTP(t, retracted, 200)
	if !strings.Contains(retracted.Body.String(), `"memories":[]`) {
		t.Fatal(retracted.Body.String())
	}
	replay := teamRequest(t, f, f.adminCookie, "POST", path, input)
	requireHTTP(t, replay, 200)
	if replay.Body.String() != response.Body.String() {
		t.Fatal("replay lost original result", replay.Body.String(), response.Body.String())
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/memory-requests/save", nil), 200)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/memory-requests/save", nil), 404)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/memory-requests/abandon/cancel", struct{}{}), 200)
	input.RequestID = "abandon"
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", path, input), 409)
	for _, query := range []string{"q=x&q=y", "limit=21", "limit=-1", "limit=1&limit=2", "q=" + strings.Repeat("x", 513)} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"?"+query, nil), 400)
	}
	for _, query := range []string{"revision=0", "revision=-1", "revision=1&revision=2"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/memories/"+id+"?"+query, nil), 400)
	}
	disabled := false
	if _, err := f.store.UpdateUserAccess(storage.WithUser(context.Background(), f.admin), f.member.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), 401)
}

type memoryFileService struct {
	fixtureRoomService
	calls int
}

func (s *memoryFileService) ResourceFile(_ context.Context, _, path, _ string) ([]byte, error) {
	s.calls++
	if path != "report.md" {
		return nil, &storage.Error{Code: "not_found", Message: "File not found"}
	}
	return []byte("Original report"), nil
}

func TestMemoryHTTPExactFileProvenanceAndReplayPreflight(t *testing.T) {
	f := newTeamFixture(t)
	files := &memoryFileService{fixtureRoomService: fixtureRoomService{f.store}}
	f.api.rooms = files
	path := "/api/rooms/" + f.room.ID + "/memories"
	in := orchestration.MemoryInput{RequestID: "file", Content: "A summary of the saved report", Provenance: orchestration.MemoryProvenance{Kind: "file", ResourceID: f.room.ResourceID, Commit: strings.Repeat("a", 40), Path: "report.md"}}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, in), 200)
	if files.calls != 1 {
		t.Fatal("file existence was not verified", files.calls)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, in), 200)
	if files.calls != 1 {
		t.Fatal("replay repeated file preflight", files.calls)
	}
	in.RequestID, in.Provenance.Path = "missing", "missing.md"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, in), 404)
	in.RequestID, in.Provenance.Commit = "expression", "HEAD"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", path, in), 400)
	if files.calls != 2 {
		t.Fatal("arbitrary Git expression reached file access", files.calls)
	}
}
