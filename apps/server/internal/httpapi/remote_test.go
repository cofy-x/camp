package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/remoteruns"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func remoteTeamFixture(t *testing.T) (teamFixture, storage.Runner) {
	t.Helper()
	f := newTeamFixture(t)
	manager, err := remoteruns.New(context.Background(), f.store, remoteruns.Config{DataDir: t.TempDir(), AllowFixture: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	f.api.EnableRemote(manager)
	response := teamRequest(t, f, f.adminCookie, "POST", "/api/runner-enrollments", map[string]any{"name": "Review machine"})
	requireHTTP(t, response, http.StatusOK)
	var enrollment struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &enrollment); err != nil || enrollment.Token == "" {
		t.Fatalf("invalid enrollment: %v", err)
	}
	runner, err := f.store.JoinRunner(context.Background(), storage.JoinRunnerInput{Token: enrollment.Token, RegistrationID: "http-registration", Credential: strings.Repeat("private-credential", 3), Name: "Review machine", StorageID: "private-storage-identity"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err = f.store.ConnectRunner(context.Background(), runner.ID, storage.RunnerConnection{
		StorageID: runner.StorageID, Runtime: "fixture", Slots: 1,
		Capabilities: json.RawMessage(`{"executor":"fixture","ready":true,"slots":1,"fingerprint":"private-fingerprint","private":"private-runtime-field"}`),
		Sources:      []storage.RunnerSource{{ID: "notes", Alias: "Team notes", Revision: "mapping-v1", Available: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, runner
}

func TestRemoteManagementAuthorityAndSafeRunnerView(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	for _, path := range []string{"/api/runners", "/api/runner-enrollments", "/api/source-imports", "/api/runners/" + runner.ID + "/sources"} {
		requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), http.StatusUnauthorized)
	}
	for _, route := range []struct{ method, path string }{
		{"PATCH", "/api/runners/" + runner.ID}, {"GET", "/api/runners/" + runner.ID + "/sources"},
		{"PUT", "/api/rooms/" + f.room.ID + "/runner"},
	} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, route.method, route.path, map[string]any{}), http.StatusForbidden)
	}
	for _, path := range []string{"/api/runner-enrollments", "/api/source-imports"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), http.StatusOK)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", "/api/runner-enrollments", map[string]any{"name": "Personal machine"}), http.StatusOK)

	response := teamRequest(t, f, f.memberCookie, "GET", "/api/runners", nil)
	requireHTTP(t, response, http.StatusOK)
	for _, private := range []string{"private-credential", "private-storage-identity", "private-fingerprint", "private-runtime-field", "sources", "connectionGeneration"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("runner view leaked private field %q", private)
		}
	}
	var list struct {
		Runners []browserRunner `json:"runners"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || len(list.Runners) != 1 || list.Runners[0].Online || list.Runners[0].Capabilities.Executor != "fixture" {
		t.Fatalf("runner view was not accurate: %s, %v", response.Body.String(), err)
	}
	response = teamRequest(t, f, f.adminCookie, "GET", "/api/runner-enrollments", nil)
	requireHTTP(t, response, http.StatusOK)
	if strings.Contains(response.Body.String(), `"token"`) || strings.Contains(response.Body.String(), "tokenHash") {
		t.Fatal("enrollment listing exposed a credential")
	}
}

func TestRoomRunnerRevisionAndSourceImportReceipt(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	route := "/api/rooms/" + f.room.ID + "/runner"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", route, map[string]any{"runnerId": runner.ID}), http.StatusBadRequest)
	response := teamRequest(t, f, f.adminCookie, "PUT", route, map[string]any{"runnerId": runner.ID, "revision": 0})
	requireHTTP(t, response, http.StatusOK)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", route, map[string]any{"runnerId": runner.ID, "revision": 0}), http.StatusConflict)
	input := storage.SourceImportInput{RequestID: "import-receipt", RunnerID: runner.ID, SourceID: "notes", ExpectedSourceRevision: "mapping-v1", RoomName: "Shared notes"}
	response = teamRequest(t, f, f.adminCookie, "POST", "/api/source-imports", input)
	requireHTTP(t, response, http.StatusOK)
	var first, replay struct {
		Import storage.SourceImport `json:"sourceImport"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil || first.Import.ID == "" {
		t.Fatalf("missing source import receipt: %v", err)
	}
	response = teamRequest(t, f, f.adminCookie, "POST", "/api/source-imports", input)
	requireHTTP(t, response, http.StatusOK)
	_ = json.Unmarshal(response.Body.Bytes(), &replay)
	if first.Import.ID != replay.Import.ID {
		t.Fatal("retry created a second source import")
	}
	input.RoomName = "Different room"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/source-imports", input), http.StatusConflict)
	input.RequestID = "stale-source"
	input.ExpectedSourceRevision = "mapping-v0"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/source-imports", input), http.StatusConflict)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/source-imports", map[string]any{"sourcePath": "/private/host/path"}), http.StatusBadRequest)
	response = teamRequest(t, f, f.adminCookie, "POST", "/api/source-imports/"+first.Import.ID+"/cancel", map[string]any{})
	requireHTTP(t, response, http.StatusOK)
	_ = json.Unmarshal(response.Body.Bytes(), &replay)
	if replay.Import.Status != "cancelled" {
		t.Fatalf("unstarted import did not cancel: %s", replay.Import.Status)
	}
}

func TestRunnerCredentialRevocationCannotBeReversedByEnable(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	path := "/api/runners/" + runner.ID
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", path, map[string]any{"revoke": true}), http.StatusOK)
	response := teamRequest(t, f, f.adminCookie, "PATCH", path, map[string]any{"enabled": true})
	requireHTTP(t, response, http.StatusOK)
	var result struct {
		Runner browserRunner `json:"runner"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &result)
	if !result.Runner.Revoked || result.Runner.Enabled || result.Runner.Online {
		t.Fatalf("revoked credential was revived: %+v", result.Runner)
	}
}

func TestRunnerHTTPPatchPreservesOmittedFields(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	path := "/api/runners/" + runner.ID
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", path, map[string]any{"enabled": false}), http.StatusOK)
	response := teamRequest(t, f, f.adminCookie, "PATCH", path, map[string]any{"name": "Renamed offline machine"})
	requireHTTP(t, response, http.StatusOK)
	var result struct {
		Runner browserRunner `json:"runner"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Runner.Enabled || result.Runner.Name != "Renamed offline machine" {
		t.Fatalf("name patch changed authority: %s %v", response.Body.String(), err)
	}
	response = teamRequest(t, f, f.adminCookie, "PATCH", path, map[string]any{"enabled": true})
	requireHTTP(t, response, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.Runner.Enabled || result.Runner.Name != "Renamed offline machine" {
		t.Fatalf("enable patch changed name: %s %v", response.Body.String(), err)
	}
}
