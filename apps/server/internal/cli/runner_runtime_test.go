package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/releaseinfo"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func runtimeArchiveFixture(t *testing.T) ([]byte, runtimePin) {
	return runtimeArchiveFor(t, "linux/x64")
}
func runtimeArchiveFor(t *testing.T, platform string) ([]byte, runtimePin) {
	t.Helper()
	directory, manifest := fixtureBundle(t)
	manifest.Schema, manifest.Role, manifest.Platform = 2, "runner-runtime", platform
	definition, ok := releaseinfo.PlatformForRuntime(platform)
	if !ok {
		t.Fatal("unsupported fixture platform")
	}
	if platform == "darwin/arm64" {
		manifest.LayerFS.Image = ""
		for _, name := range []string{"images/layerfs.tar", "runtime/layerfs-runtime/installed.json"} {
			delete(manifest.Files, name)
			if err := os.Remove(filepath.Join(directory, name)); err != nil {
				t.Fatal(err)
			}
		}
	}

	delete(manifest.Files, "bin/camp")
	if err := os.Remove(filepath.Join(directory, "bin/camp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(directory, "bin/node"), 0755); err != nil {
		t.Fatal(err)
	}
	manifest.Symlinks["bin/node-link"] = "node"
	if err := os.Symlink("node", filepath.Join(directory, "bin/node-link")); err != nil {
		t.Fatal(err)
	}
	writeBundleManifest(t, directory, manifest)
	var content bytes.Buffer
	compressed := gzip.NewWriter(&content)
	archive := tar.NewWriter(compressed)
	var size int64
	err := filepath.WalkDir(directory, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := ""
		if entry.Type()&os.ModeSymlink != 0 {
			target, err = os.Readlink(name)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, target)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(directory, name)
		header.Name = filepath.ToSlash(filepath.Join("camp-runtime-"+definition.ID, relative))
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			_, err = archive.Write(data)
			size += int64(len(data))
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content.Bytes())
	manifestData, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifestData)
	return content.Bytes(), runtimePin{Schema: 1, Platform: platform, ProtocolVersion: p.Version, SHA256: hex.EncodeToString(digest[:]), ManifestSHA256: hex.EncodeToString(manifestDigest[:]), Bytes: int64(content.Len()), UnpackedBytes: size}
}

func TestManagedRuntimeInstallsOnceAndKeepsIdentity(t *testing.T) {
	archive, pin := runtimeArchiveFixture(t)
	downloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		if r.URL.Path != "/api/daemon/runtime/"+pin.SHA256 || r.Header.Get("Authorization") != "Bearer saved-credential" {
			t.Error("Wrong runtime identity")
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	directory := t.TempDir()
	identity := filepath.Join(directory, "daemon.json")
	if err := os.WriteFile(identity, []byte("existing-private-identity"), 0600); err != nil {
		t.Fatal(err)
	}
	config := daemon.Config{DataDir: directory, ServerURL: server.URL, Credential: "saved-credential"}
	for range 2 {
		installed, err := installRuntime(context.Background(), config, pin, io.Discard, filepath.Join(directory, "absent-offline"))
		if err != nil || installed != pin.cache(directory) {
			t.Fatal(installed, err)
		}
	}
	if downloads != 1 {
		t.Fatal("Verified runtime was downloaded again", downloads)
	}
	if got, _ := os.ReadFile(identity); string(got) != "existing-private-identity" {
		t.Fatal("Installation replaced saved identity")
	}
	info, err := os.Stat(filepath.Join(pin.cache(directory), "bin/node"))
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("Executable mode was lost", err)
	}
	if _, err := os.Stat(filepath.Join(pin.cache(directory), "bin/node-link")); err != nil {
		t.Fatal("Relative runtime link was lost", err)
	}
}

func TestManagedRuntimeOfflineAndCorruption(t *testing.T) {
	archive, pin := runtimeArchiveFixture(t)
	for _, damaged := range []bool{false, true} {
		directory := t.TempDir()
		offline := filepath.Join(directory, "offline.tar.gz")
		content := append([]byte(nil), archive...)
		if damaged {
			content[len(content)/2] ^= 1
		}
		if err := os.WriteFile(offline, content, 0600); err != nil {
			t.Fatal(err)
		}
		config := daemon.Config{DataDir: directory, ServerURL: "https://must-not-be-contacted.invalid"}
		_, err := installRuntime(context.Background(), config, pin, io.Discard, offline)
		if damaged {
			if err == nil || !strings.Contains(err.Error(), "checksum") {
				t.Fatal("Corrupted runtime accepted", err)
			}
			if _, err := os.Stat(pin.cache(directory)); !os.IsNotExist(err) {
				t.Fatal("Corrupted runtime became installed")
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPinnedRuntimeDetectsCoherentCacheChangesAndRepairsOnlyCache(t *testing.T) {
	archive, pin := runtimeArchiveFixture(t)
	directory := t.TempDir()
	offline := filepath.Join(directory, "offline.tar.gz")
	if err := os.WriteFile(offline, archive, 0600); err != nil {
		t.Fatal(err)
	}
	config := daemon.Config{DataDir: directory}
	installed, err := installRuntime(context.Background(), config, pin, io.Discard, offline)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(installed, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest bundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	changed := []byte("different-executable")
	if err := os.WriteFile(filepath.Join(installed, "bin/node"), changed, 0755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(changed)
	entry := manifest.Files["bin/node"]
	entry.SHA256 = hex.EncodeToString(digest[:])
	entry.Bytes = int64(len(changed))
	manifest.Files["bin/node"] = entry
	writeBundleManifest(t, installed, manifest)
	if _, err := verifyRunnerBundle(installed); err != nil {
		t.Fatal("Test did not construct a coherent local manifest", err)
	}
	if _, err := verifyPinnedRuntime(installed, pin); err == nil {
		t.Fatal("Compiled pin accepted replacement manifest")
	}
	journal := filepath.Join(directory, "journal-sentinel")
	if err := os.WriteFile(journal, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := installRuntime(context.Background(), config, pin, &output, offline); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Replacing a damaged runtime cache") {
		t.Fatal("Damaged cache was not repaired")
	}
	if _, err := verifyPinnedRuntime(installed, pin); err != nil {
		t.Fatal(err)
	}
	if value, _ := os.ReadFile(journal); string(value) != "preserved" {
		t.Fatal("Cache repair touched Runner work")
	}
}

func TestHostPreflightPreservesRegistrationWithoutDownload(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux Docker prerequisite; Mac native execution has no Docker dependency")
	}
	archive, pin := runtimeArchiveFixture(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; _, _ = w.Write(archive) }))
	defer server.Close()
	directory := t.TempDir()
	identity := filepath.Join(directory, "daemon.json")
	if err := os.WriteFile(identity, []byte("saved-identity"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := installRunnerRuntime(context.Background(), daemon.Config{DataDir: directory, ServerURL: server.URL, DockerSocket: filepath.Join(directory, "missing-docker.sock")}, pin, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "host requirements") || requests != 0 {
		t.Fatal("Host failure did not stop before download", requests, err)
	}
	if value, _ := os.ReadFile(identity); string(value) != "saved-identity" {
		t.Fatal("Host failure changed registration")
	}
}

func TestSetupUsesCorrectedDockerSocketBeforePreflight(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux Docker socket repair")
	}
	_, pin := runtimeArchiveFixture(t)
	oldPin := runtimePinJSON
	encoded, _ := json.Marshal(pin)
	runtimePinJSON = string(encoded)
	t.Cleanup(func() { runtimePinJSON = oldPin })
	enrollments := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/enroll" {
			t.Error("Host failure attempted a runtime download")
			w.WriteHeader(500)
			return
		}
		enrollments <- struct{}{}
		_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "preflight-runner"})
	}))
	defer server.Close()
	directory := t.TempDir()
	// This is an already registered Runner repairing host settings. New setup
	// is covered separately and must fail before creating any identity.
	if _, err := daemon.Join(context.Background(), daemon.Config{DataDir: directory, ServerURL: server.URL, Name: "Preflight", Executor: "native", Slots: 1, RuntimeSource: "server"}, "private-code"); err != nil {
		t.Fatal(err)
	}
	var original daemon.Config
	for index, name := range []string{"wrong.sock", "corrected-but-unavailable.sock", "still-unavailable.sock"} {
		socket := filepath.Join(directory, name)
		args := []string{"setup", "--server", server.URL, "--name", "Preflight", "--data-dir", directory, "--docker-socket", socket, "--code-file", "-"}
		if index == 0 {
			args = append(args, "--runtime-source", "server")
		}
		input := runnerInput{reader: strings.NewReader("private-code\n"), isTerminal: func(io.Reader) bool { return false }, readSecret: readConnectionCode}
		err := runRunnerWithInput(context.Background(), args, io.Discard, input)
		if err == nil || !strings.Contains(err.Error(), "host requirements") {
			t.Fatal("Expected preserved host failure", err)
		}
		config, err := daemon.LoadConfig(directory)
		if err != nil || config.DockerSocket != socket || config.RuntimeSource != "server" {
			t.Fatal("Explicit installation settings were not persisted", config.RuntimeSource, err)
		}
		if index == 0 {
			original = config
		} else if config.RegistrationID != original.RegistrationID || config.Credential != original.Credential {
			t.Fatal("Correcting host settings replaced identity")
		}
		data, err := os.ReadFile(filepath.Join(directory, "readiness.json"))
		if err != nil || !strings.Contains(string(data), socket) {
			t.Fatal("Host preflight used the old Docker socket", err)
		}
	}
	if len(enrollments) != 1 {
		t.Fatal("Host retry enrolled another Runner")
	}
}

func TestNewNativeSetupRejectsHostBeforeReadingCodeOrEnrolling(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux Docker prerequisite before enrollment")
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	directory := filepath.Join(t.TempDir(), "not-created")
	input := runnerInput{reader: unexpectedInput{t}, isTerminal: func(io.Reader) bool { return false }, readSecret: func(io.Reader) ([]byte, error) { t.Fatal("Host failure read a connection code"); return nil, nil }}
	err := runRunnerWithInput(context.Background(), []string{"setup", "--server", server.URL, "--name", "Unsupported", "--data-dir", directory, "--docker-socket", filepath.Join(t.TempDir(), "missing.sock"), "--code-file", "-"}, io.Discard, input)
	if err == nil || !strings.Contains(err.Error(), "host requirements") || requests != 0 {
		t.Fatal("Unsuitable host enrolled a Runner", err, requests)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("Unsuitable setup created private identity/storage", err)
	}
}

func TestRuntimeExtractionRejectsUnsafeArchives(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "camp-runtime-linux-amd64/../../escape", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "camp-runtime-linux-amd64/escape", Typeflag: tar.TypeSymlink, Linkname: "../../escape"},
		{Name: "camp-runtime-linux-amd64/hard", Typeflag: tar.TypeLink, Linkname: "bin/node"},
		{Name: "camp-runtime-linux-amd64/device", Typeflag: tar.TypeChar},
	} {
		var content bytes.Buffer
		compressed := gzip.NewWriter(&content)
		archive := tar.NewWriter(compressed)
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		_ = archive.Close()
		_ = compressed.Close()
		if err := extractRuntime(context.Background(), &content, t.TempDir(), 0, "linux-amd64"); err == nil {
			t.Fatal("Unsafe archive was extracted", header.Name)
		}
	}
}

func TestRuntimeFailureClearsPreviousReadinessWithoutReplacingIdentity(t *testing.T) {
	_, pin := runtimeArchiveFixture(t)
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	directory := t.TempDir()
	if err := daemon.WriteReadiness(directory, p.ReadinessReport{Ready: true}); err != nil {
		t.Fatal(err)
	}
	_, err := installRunnerRuntime(context.Background(), daemon.Config{DataDir: directory, ServerURL: server.URL}, pin, io.Discard)
	if err == nil {
		t.Fatal("Missing runtime unexpectedly installed")
	}
	data, err := os.ReadFile(filepath.Join(directory, "readiness.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report p.ReadinessReport
	if json.Unmarshal(data, &report) != nil || report.Ready || report.CheckedAt == "" {
		t.Fatal("Failed installation retained stale readiness")
	}
}

func TestMacRuntimeInstallsWithoutLinuxAndRejectsForeignPin(t *testing.T) {
	content, pin := runtimeArchiveFor(t, "darwin/arm64")
	directory := t.TempDir()
	offline := filepath.Join(directory, pin.filename())
	if err := os.WriteFile(offline, content, 0600); err != nil {
		t.Fatal(err)
	}
	installed, err := installRuntime(t.Context(), daemon.Config{DataDir: directory}, pin, io.Discard, offline)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installed, "runtime/layerfs-runtime/installed.json")); !os.IsNotExist(err) {
		t.Fatal("Mac runtime included Linux backend", err)
	}
	foreign := pin
	foreign.Platform = "linux/x64"
	if _, err := verifyPinnedRuntime(installed, foreign); err == nil {
		t.Fatal("foreign runtime pin was accepted")
	}
	old := runtimePinJSON
	t.Cleanup(func() { runtimePinJSON = old })
	host, _ := releaseinfo.HostPlatform()
	foreign.Platform = "darwin/arm64"
	if host.RuntimePlatform == foreign.Platform {
		foreign.Platform = "linux/x64"
	}
	data, _ := json.Marshal(foreign)
	runtimePinJSON = string(data)
	if _, err := compiledRuntimePin(); err == nil {
		t.Fatal("installer accepted a runtime for another host")
	}
}
