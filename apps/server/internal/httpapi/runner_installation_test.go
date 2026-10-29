package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/remoteruns"
)

func TestRunnerPackageDownloadRetainsAuthenticationAndRanges(t *testing.T) {
	f := newTeamFixture(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	descriptor := map[string]any{"schema": 1}
	for kind, filename := range map[string]string{"installer": "camp-runner-linux-amd64.tar.gz", "offline": "camp-runner-offline-linux-amd64.tar.gz", "runtime": "camp-runtime-linux-amd64-" + strings.Repeat("a", 64) + ".tar.gz"} {
		if err := os.WriteFile(filepath.Join(dir, "packages", filename), []byte("0123456789"), 0600); err != nil {
			t.Fatal(err)
		}
		descriptor[kind] = map[string]any{"filename": filename, "sha256": strings.Repeat("a", 64), "bytes": 10, "unpackedBytes": 20}
	}
	raw, _ := json.Marshal(descriptor)
	if err := os.WriteFile(filepath.Join(dir, "packages", "runner-release.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := remoteruns.New(context.Background(), f.store, remoteruns.Config{DataDir: dir, AllowFixture: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	f.api.EnableRemote(manager)
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/runner-package", nil), http.StatusUnauthorized)
	for _, route := range []string{"/api/runner-package?variant=offline", "/api/runner-package/checksum?variant=offline", "/api/runner-installation"} {
		requireHTTP(t, teamRequest(t, f, nil, "GET", route, nil), http.StatusUnauthorized)
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", route, nil), http.StatusOK)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/runner-package?variant=unknown", nil), http.StatusBadRequest)
	checksum := teamRequest(t, f, f.memberCookie, "GET", "/api/runner-package/checksum?variant=offline", nil)
	if !strings.HasSuffix(checksum.Body.String(), "  camp-runner-offline-linux-amd64.tar.gz\n") {
		t.Fatal("offline checksum does not name the offline archive")
	}
	request := httptest.NewRequest("GET", "http://localhost/api/runner-package", nil)
	request.RemoteAddr = "127.0.0.1:1000"
	request.AddCookie(f.memberCookie)
	request.Header.Set("Range", "bytes=3-6")
	response := httptest.NewRecorder()
	f.api.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "3456" || response.Header().Get("Content-Range") != "bytes 3-6/10" {
		t.Fatal("authenticated range was not preserved", response.Code, response.Body.String(), response.Header())
	}
}
