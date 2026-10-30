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
	for _, platform := range []string{"linux-amd64", "darwin-arm64"} {
		t.Run(platform, func(t *testing.T) { testPlatformRunnerPackage(t, platform) })
	}
}

func testPlatformRunnerPackage(t *testing.T, platform string) {
	f := newTeamFixture(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	artifacts := map[string]any{}
	descriptor := map[string]any{"schema": 2, "runners": map[string]any{platform: artifacts}}
	for kind, filename := range map[string]string{"installer": "camp-runner-" + platform + ".tar.gz", "offline": "camp-runner-offline-" + platform + ".tar.gz", "runtime": "camp-runtime-" + platform + "-" + strings.Repeat("a", 64) + ".tar.gz"} {
		if err := os.WriteFile(filepath.Join(dir, "packages", filename), []byte("0123456789"), 0600); err != nil {
			t.Fatal(err)
		}
		artifacts[kind] = map[string]any{"filename": filename, "sha256": strings.Repeat("a", 64), "bytes": 10, "unpackedBytes": 20}
	}
	artifacts["runtime"].(map[string]any)["manifestSHA256"] = strings.Repeat("b", 64)
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
	for _, route := range []string{"/api/runner-package?platform=" + platform + "&variant=offline", "/api/runner-package/checksum?platform=" + platform + "&variant=offline", "/api/runner-installation?platform=" + platform} {
		requireHTTP(t, teamRequest(t, f, nil, "GET", route, nil), http.StatusUnauthorized)
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", route, nil), http.StatusOK)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/runner-package?variant=unknown", nil), http.StatusBadRequest)
	for _, route := range []string{"/api/runner-package", "/api/runner-package/checksum", "/api/runner-installation"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", route+"?platform=windows-amd64", nil), http.StatusBadRequest)
	}
	metadata := teamRequest(t, f, f.memberCookie, "GET", "/api/runner-installation?platform="+platform, nil)
	var installation remoteruns.RunnerInstallation
	if err := json.Unmarshal(metadata.Body.Bytes(), &installation); err != nil || installation.Target != platform || !installation.Available || !strings.Contains(installation.DownloadURL, "platform="+platform) && platform != "linux-amd64" {
		t.Fatal("platform selection did not reach the authenticated package", installation, err)
	}

	checksum := teamRequest(t, f, f.memberCookie, "GET", "/api/runner-package/checksum?platform="+platform+"&variant=offline", nil)
	if !strings.HasSuffix(checksum.Body.String(), "  camp-runner-offline-"+platform+".tar.gz\n") {
		t.Fatal("offline checksum does not name the offline archive")
	}
	request := httptest.NewRequest("GET", "http://localhost/api/runner-package?platform="+platform, nil)
	request.RemoteAddr = "127.0.0.1:1000"
	request.AddCookie(f.memberCookie)
	request.Header.Set("Range", "bytes=3-6")
	response := httptest.NewRecorder()
	f.api.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "3456" || response.Header().Get("Content-Range") != "bytes 3-6/10" {
		t.Fatal("authenticated range was not preserved", response.Code, response.Body.String(), response.Header())
	}
}
