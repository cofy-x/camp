package remoteruns

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/releaseinfo"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestPublishedInstallationNeedsNoServerPackagesAndRejectsOtherMirrors(t *testing.T) {
	directory := t.TempDir()
	m := &Manager{config: Config{DataDir: directory}}
	linux := releaseinfo.RunnerArtifacts{
		Installer: releaseArtifact{Filename: runnerPackageName, SHA256: strings.Repeat("a", 64), Bytes: 10, UnpackedBytes: 20},
		Runtime:   releaseArtifact{Filename: "camp-runtime-linux-amd64-" + strings.Repeat("c", 64) + ".tar.gz", SHA256: strings.Repeat("c", 64), ManifestSHA256: strings.Repeat("d", 64), Bytes: 20, UnpackedBytes: 30},
		Offline:   releaseArtifact{Filename: runnerOfflinePackageName, SHA256: strings.Repeat("b", 64), Bytes: 30, UnpackedBytes: 40},
	}
	release := runnerRelease{Schema: 2, Version: "v0.1.0-alpha.1", SourceCommit: strings.Repeat("a", 40), ProtocolVersion: p.Version, Runners: map[string]releaseinfo.RunnerArtifacts{releaseinfo.LinuxAMD64: linux}}
	assertPublic := func() {
		t.Helper()
		meta := m.runnerInstallation(release, true)
		if !meta.Available || !meta.Offline.Available || meta.Source != "github" || meta.Version != release.Version || meta.Runtime.Bytes != 20 || meta.DownloadURL != "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.1/"+runnerPackageName {
			t.Fatalf("incorrect fixed public installation: %+v", meta)
		}
	}
	assertPublic()
	packages := filepath.Join(directory, "packages")
	if err := os.Mkdir(packages, 0700); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []releaseArtifact{linux.Installer, linux.Runtime, linux.Offline} {
		if err := os.WriteFile(filepath.Join(packages, artifact.Filename), []byte(strings.Repeat("x", int(artifact.Bytes))), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeDescriptor := func(value runnerRelease) {
		t.Helper()
		data, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(packages, "runner-release.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	other := release
	other.Version = "v0.1.0-alpha.2"
	writeDescriptor(other)
	assertPublic()
	writeDescriptor(release)
	meta := m.runnerInstallation(release, true)
	if meta.Source != "server" || meta.DownloadURL != "/api/runner-package" || !meta.Offline.Available {
		t.Fatalf("matching explicit mirror unavailable: %+v", meta)
	}
}

func TestRunnerReleasePublicationRequiresCompleteInstallerAndRuntime(t *testing.T) {
	directory := t.TempDir()
	packages := filepath.Join(directory, "packages")
	if err := os.Mkdir(packages, 0700); err != nil {
		t.Fatal(err)
	}
	m := &Manager{config: Config{DataDir: directory}}
	linux := releaseinfo.RunnerArtifacts{
		Installer: releaseArtifact{Filename: runnerPackageName, SHA256: strings.Repeat("a", 64), Bytes: 10, UnpackedBytes: 20},
		Offline:   releaseArtifact{Filename: runnerOfflinePackageName, SHA256: strings.Repeat("b", 64), Bytes: 30, UnpackedBytes: 40},
		Runtime:   releaseArtifact{Filename: "camp-runtime-linux-amd64-" + strings.Repeat("c", 64) + ".tar.gz", SHA256: strings.Repeat("c", 64), ManifestSHA256: strings.Repeat("d", 64), Bytes: 20, UnpackedBytes: 30},
	}
	release := runnerRelease{Schema: 2, Runners: map[string]releaseinfo.RunnerArtifacts{releaseinfo.LinuxAMD64: linux}}
	publish := func() {
		t.Helper()
		release.Runners[releaseinfo.LinuxAMD64] = linux
		raw, _ := json.Marshal(release)
		if err := os.WriteFile(filepath.Join(packages, "runner-release.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write := func(a releaseArtifact) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(packages, a.Filename), []byte(strings.Repeat("x", int(a.Bytes))), 0600); err != nil {
			t.Fatal(err)
		}
	}
	publish()
	write(linux.Installer)
	if m.RunnerInstallation().Available {
		t.Fatal("installer advertised without runtime")
	}
	write(linux.Runtime)
	meta := m.RunnerInstallation()
	if !meta.Available || meta.Offline.Available || meta.Bytes != 10 || meta.Runtime.Bytes != 20 || meta.Runtime.UnpackedBytes != 30 {
		t.Fatalf("wrong sizes or availability: %+v", meta)
	}
	write(linux.Offline)
	if !m.RunnerInstallation().Offline.Available {
		t.Fatal("offline archive not advertised")
	}
	if err := os.Remove(filepath.Join(packages, linux.Runtime.Filename)); err != nil {
		t.Fatal(err)
	}
	if m.RunnerInstallation().Available || !m.RunnerInstallation().Offline.Available {
		t.Fatal("offline option should work independently of runtime hosting")
	}
	write(linux.Runtime)
	if err := os.WriteFile(filepath.Join(packages, linux.Runtime.Filename), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if m.RunnerInstallation().Available {
		t.Fatal("partial runtime advertised")
	}
	write(linux.Runtime)
	linux.Installer.Filename = "../secret"
	publish()
	if m.RunnerInstallation().Available || m.RunnerInstallation().Offline.Available {
		t.Fatal("unsafe descriptor accepted")
	}
	linux.Installer.Filename = runnerPackageName
	publish()
	if err := os.Remove(filepath.Join(packages, runnerPackageName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(packages, linux.Runtime.Filename), filepath.Join(packages, runnerPackageName)); err != nil {
		t.Fatal(err)
	}
	if m.RunnerInstallation().Available {
		t.Fatal("symlinked package advertised")
	}
}

func TestRuntimeDownloadRequiresLiveMachineAuthorization(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	config := h.join(t, "Installer", "fixture", nil, nativeexecutor.Config{})
	sha := strings.Repeat("a", 64)
	packages := filepath.Join(h.data, "packages")
	if err := os.Mkdir(packages, 0700); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(packages, "camp-runtime-linux-amd64-"+sha+".tar.gz")
	if err := os.WriteFile(filename, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	request := func(token, cookie, origin, hash string) (int, string) {
		t.Helper()
		req, err := http.NewRequest("GET", h.server.URL+"/api/daemon/runtime/"+hash, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Origin", origin)
		req.Header.Set("Range", "bytes=3-6")
		res, err := h.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(data)
	}
	if status, _ := request("", "", "", sha); status != 401 {
		t.Fatal("unauthenticated download", status)
	}
	for _, headers := range [][2]string{{"session=browser", ""}, {"", "https://browser.example"}} {
		if status, _ := request(config.Credential, headers[0], headers[1], sha); status != 403 {
			t.Fatal("browser credentials accepted", status)
		}
	}
	if status, body := request(config.Credential, "", "", sha); status != 206 || body != "3456" {
		t.Fatal("authenticated range failed", status, body)
	}
	if status, _ := request(config.Credential, "", "", "invalid"); status != 404 {
		t.Fatal("invalid runtime digest accepted", status)
	}
	enabled := false
	if _, err := h.store.UpdateRunner(h.actor, config.RunnerID, storage.RunnerUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if status, _ := request(config.Credential, "", "", sha); status != 403 {
		t.Fatal("disabled machine downloaded runtime", status)
	}
	if _, err := h.store.UpdateRunner(h.actor, config.RunnerID, storage.RunnerUpdate{Revoke: true}); err != nil {
		t.Fatal(err)
	}
	if status, _ := request(config.Credential, "", "", sha); status != 401 {
		t.Fatal("revoked machine downloaded runtime", status)
	}
}
