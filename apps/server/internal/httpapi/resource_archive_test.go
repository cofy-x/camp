package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type archiveRoomService struct {
	fixtureRoomService
	archive []byte
	err     error
	calls   []string
}

func (service *archiveRoomService) ResourceArchive(ctx context.Context, resourceID, commit string) ([]byte, error) {
	service.calls = append(service.calls, commit)
	if _, err := service.store.ValidateResourceRevision(ctx, resourceID, commit); err != nil {
		return nil, err
	}
	return service.archive, service.err
}

func TestResourceArchiveRequiresOneRetainedRevisionAndCurrentReadAccess(t *testing.T) {
	f := newTeamFixture(t)
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	file, err := writer.Create("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("# Saved project\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	service := &archiveRoomService{fixtureRoomService: fixtureRoomService{f.store}, archive: content.Bytes()}
	f.api.rooms = service
	path := "/api/resources/" + f.room.ResourceID + "/archive"
	commit := strings.Repeat("a", 40)
	for _, query := range []string{"", "?commit=", "?commit=HEAD", "?commit=" + strings.Repeat("b", 40), "?commit=" + strings.Repeat("z", 40), "?commit=" + commit + "&commit=" + commit} {
		response := teamRequest(t, f, f.memberCookie, "GET", path+query, nil)
		requireHTTP(t, response, http.StatusBadRequest)
		if response.Header().Get("Content-Disposition") != "" {
			t.Fatal("Rejected archive looked like a successful download")
		}
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", path+"?commit="+commit, nil), http.StatusUnauthorized)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/resources/missing/archive?commit="+commit, nil), http.StatusNotFound)
	response := teamRequest(t, f, f.memberCookie, "GET", path+"?commit="+commit, nil)
	requireHTTP(t, response, http.StatusOK)
	_, disposition, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || disposition["filename"] != "camp-files-aaaaaaaaaaaa.zip" || response.Header().Get("Content-Type") != "application/zip" || response.Header().Get("Cache-Control") != "no-store" || !bytes.Equal(response.Body.Bytes(), content.Bytes()) {
		t.Fatal("Archive headers or saved bytes changed", response.Header(), err)
	}
	service.err = &storage.Error{Code: "invalid_resource", Message: "The saved files could not be exported."}
	response = teamRequest(t, f, f.memberCookie, "GET", path+"?commit="+commit, nil)
	requireHTTP(t, response, http.StatusBadRequest)
	if response.Header().Get("Content-Disposition") != "" || bytes.Contains(response.Body.Bytes(), content.Bytes()) {
		t.Fatal("Archive construction failure returned a partial download")
	}
	disabled := false
	if _, err := f.store.UpdateUserAccess(storage.WithUser(context.Background(), f.admin), f.member.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	before := len(service.calls)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"?commit="+commit, nil), http.StatusUnauthorized)
	if len(service.calls) != before {
		t.Fatal("Disabled member reached resource export")
	}
}
