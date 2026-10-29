package remoteruns

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/httptransfer"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

const runnerPackageName = "camp-runner-linux-amd64.tar.gz"
const runnerOfflinePackageName = "camp-runner-offline-linux-amd64.tar.gz"
const maxRunnerPackageBytes = 2 << 30

type RunnerPackage struct {
	SHA256      string `json:"sha256"`
	ChecksumURL string `json:"checksumUrl"`
	Available   bool   `json:"available"`
	DownloadURL string `json:"downloadUrl"`
	Filename    string `json:"filename"`
	Bytes       int64  `json:"bytes"`
}

type RunnerInstallation struct {
	RunnerPackage
	Platform string        `json:"platform"`
	Offline  RunnerPackage `json:"offline"`
	Runtime  struct {
		Bytes         int64 `json:"bytes"`
		UnpackedBytes int64 `json:"unpackedBytes"`
	} `json:"runtime"`
}

type releaseArtifact struct {
	Filename      string `json:"filename"`
	SHA256        string `json:"sha256"`
	Bytes         int64  `json:"bytes"`
	UnpackedBytes int64  `json:"unpackedBytes"`
}

type runnerRelease struct {
	Schema    int             `json:"schema"`
	Installer releaseArtifact `json:"installer"`
	Runtime   releaseArtifact `json:"runtime"`
	Offline   releaseArtifact `json:"offline"`
}

func validReleaseDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

// Publish the descriptor last, after staging verified archives atomically. Its
// strict filenames cannot turn the authenticated download into a file browser.
func (m *Manager) runnerRelease() (runnerRelease, error) {
	var release runnerRelease
	name := filepath.Join(m.config.DataDir, "packages", "runner-release.json")
	st, err := os.Lstat(name)
	if err != nil {
		return release, err
	}
	if !st.Mode().IsRegular() || st.Size() > 16<<10 {
		return release, errors.New("Invalid Runner release descriptor")
	}
	file, err := os.Open(name)
	if err != nil {
		return release, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, (16<<10)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&release); err != nil {
		return release, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || release.Schema != 1 {
		return release, errors.New("Unsupported Runner release descriptor")
	}
	for _, artifact := range []releaseArtifact{release.Installer, release.Runtime, release.Offline} {
		if !validReleaseDigest(artifact.SHA256) || artifact.Bytes <= 0 || artifact.Bytes > maxRunnerPackageBytes || artifact.UnpackedBytes <= 0 || artifact.UnpackedBytes > 8<<30 {
			return release, errors.New("Invalid Runner release artifact")
		}
	}
	if release.Installer.Filename != runnerPackageName || release.Offline.Filename != runnerOfflinePackageName || release.Runtime.Filename != "camp-runtime-linux-amd64-"+release.Runtime.SHA256+".tar.gz" {
		return release, errors.New("Invalid Runner release filename")
	}
	return release, nil
}

func (m *Manager) releaseArtifactPath(artifact releaseArtifact) (string, error) {
	name := filepath.Join(m.config.DataDir, "packages", artifact.Filename)
	st, err := os.Lstat(name)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() != artifact.Bytes {
		return "", errors.New("The staged Runner artifact is incomplete")
	}
	return name, nil
}

func (m *Manager) RunnerInstallation() RunnerInstallation {
	out := RunnerInstallation{Platform: "Ubuntu 24.04 amd64", RunnerPackage: RunnerPackage{Filename: runnerPackageName}, Offline: RunnerPackage{Filename: runnerOfflinePackageName}}
	release, err := m.runnerRelease()
	if err != nil {
		return out
	}
	out.Runtime.Bytes, out.Runtime.UnpackedBytes = release.Runtime.Bytes, release.Runtime.UnpackedBytes
	if _, err := m.releaseArtifactPath(release.Runtime); err == nil {
		if _, err := m.releaseArtifactPath(release.Installer); err == nil {
			out.RunnerPackage = packageView(release.Installer, "")
		}
	}
	if _, err := m.releaseArtifactPath(release.Offline); err == nil {
		out.Offline = packageView(release.Offline, "?variant=offline")
	}
	return out
}

func packageView(artifact releaseArtifact, query string) RunnerPackage {
	return RunnerPackage{Available: true, Filename: artifact.Filename, SHA256: artifact.SHA256, Bytes: artifact.Bytes,
		DownloadURL: "/api/runner-package" + query, ChecksumURL: "/api/runner-package/checksum" + query}
}

func (m *Manager) RunnerPackagePath(offline bool) (string, RunnerPackage, error) {
	release, err := m.runnerRelease()
	if err != nil {
		return "", RunnerPackage{}, err
	}
	artifact, query := release.Installer, ""
	if offline {
		artifact, query = release.Offline, "?variant=offline"
	} else if _, err := m.releaseArtifactPath(release.Runtime); err != nil {
		return "", RunnerPackage{}, err
	}
	name, err := m.releaseArtifactPath(artifact)
	return name, packageView(artifact, query), err
}

// Runtime archives remain content-addressed across releases so a previously
// downloaded installer never silently resolves to a newer execution environment.
func (m *Manager) runtimeDownload(w http.ResponseWriter, r *http.Request, _ storage.Runner) {
	digest := r.PathValue("sha256")
	if !validReleaseDigest(digest) {
		machineError(w, 404, "The pinned Camp runtime is unavailable.")
		return
	}
	filename := "camp-runtime-linux-amd64-" + digest + ".tar.gz"
	name := filepath.Join(m.config.DataDir, "packages", filename)
	st, err := os.Lstat(name)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxRunnerPackageBytes {
		machineError(w, 404, "The pinned Camp runtime is unavailable. Ask an administrator to publish this release, then repeat setup.")
		return
	}
	file, err := os.Open(name)
	if err != nil {
		machineError(w, 503, "The Camp runtime could not be opened.")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Camp-SHA256", digest)
	download, err := httptransfer.DownloadWriter(w, time.Minute)
	if err != nil {
		return
	}
	http.ServeContent(download, r, filename, st.ModTime(), file)
}
