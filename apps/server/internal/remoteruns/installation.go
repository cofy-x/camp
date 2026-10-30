package remoteruns

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/httptransfer"
	"github.com/cofy-x/camp/apps/server/internal/releaseinfo"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

const runnerPackageName = releaseinfo.InstallerName
const runnerOfflinePackageName = releaseinfo.OfflineName
const maxRunnerPackageBytes = releaseinfo.MaxArtifactBytes

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
	Version   string                 `json:"version,omitempty"`
	Source    string                 `json:"source,omitempty"`
	Platform  string                 `json:"platform"`
	Target    string                 `json:"target"`
	Platforms []releaseinfo.Platform `json:"platforms"`
	Offline   RunnerPackage          `json:"offline"`
	Runtime   struct {
		Bytes         int64 `json:"bytes"`
		UnpackedBytes int64 `json:"unpackedBytes"`
	} `json:"runtime"`
}

type releaseArtifact = releaseinfo.Artifact
type runnerRelease = releaseinfo.Descriptor

func validReleaseDigest(value string) bool { return releaseinfo.ValidDigest(value) }

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
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil {
		return release, err
	}
	release, err = releaseinfo.Parse(data)
	if err != nil {
		return release, err
	}
	compiled, published, err := releaseinfo.Embedded()
	if err != nil || (published && !reflect.DeepEqual(compiled, release)) {
		return release, errors.New("The staged Runner release differs from this Server build")
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
	return m.RunnerInstallationFor(releaseinfo.LinuxAMD64)
}
func (m *Manager) RunnerInstallationFor(platform string) RunnerInstallation {
	compiled, published, err := releaseinfo.Embedded()
	if err != nil {
		return installationPlatform(platform)
	}
	return m.runnerInstallationFor(compiled, published, platform)
}

func (m *Manager) runnerInstallation(compiled runnerRelease, published bool) RunnerInstallation {
	return m.runnerInstallationFor(compiled, published, releaseinfo.LinuxAMD64)
}
func installationPlatform(platform string) RunnerInstallation {
	definition, _ := releaseinfo.PlatformFor(platform)
	return RunnerInstallation{Target: platform, Platforms: releaseinfo.Platforms(), Platform: definition.Name, Source: "server", RunnerPackage: RunnerPackage{Filename: releaseinfo.InstallerFor(platform)}, Offline: RunnerPackage{Filename: releaseinfo.OfflineFor(platform)}}
}
func (m *Manager) runnerInstallationFor(compiled runnerRelease, published bool, platform string) RunnerInstallation {
	out := installationPlatform(platform)
	local, localErr := m.runnerRelease()
	release := local
	if published {
		release = compiled
		out.Source = "github"
	} else if localErr != nil {
		return out
	}
	runner, ok := release.Runners[platform]
	if !ok {
		return out
	}
	if published {
		out.RunnerPackage = githubPackageView(runner.Installer, release.Version)
		out.Offline = githubPackageView(runner.Offline, release.Version)
	}
	out.Version = release.Version
	out.Runtime.Bytes, out.Runtime.UnpackedBytes = runner.Runtime.Bytes, runner.Runtime.UnpackedBytes
	if localErr == nil && (!published || reflect.DeepEqual(local, compiled)) {
		localRunner, ok := local.Runners[platform]
		if !ok {
			return out
		}
		if _, err := m.releaseArtifactPath(localRunner.Runtime); err == nil {
			if _, err := m.releaseArtifactPath(localRunner.Installer); err == nil {
				out.RunnerPackage = platformPackageView(localRunner.Installer, platform, false)
				out.Source = "server"
			}
		}
		if _, err := m.releaseArtifactPath(localRunner.Offline); err == nil {
			out.Offline = platformPackageView(localRunner.Offline, platform, true)
		}
	}
	return out
}

func githubPackageView(artifact releaseArtifact, version string) RunnerPackage {
	address, err := releaseinfo.DownloadURL(version, artifact.Filename)
	if err != nil {
		return RunnerPackage{Filename: artifact.Filename}
	}
	return RunnerPackage{Available: true, Filename: artifact.Filename, SHA256: artifact.SHA256, Bytes: artifact.Bytes, DownloadURL: address, ChecksumURL: address + ".sha256"}
}

func packageView(artifact releaseArtifact, query string) RunnerPackage {
	return RunnerPackage{Available: true, Filename: artifact.Filename, SHA256: artifact.SHA256, Bytes: artifact.Bytes,
		DownloadURL: "/api/runner-package" + query, ChecksumURL: "/api/runner-package/checksum" + query}
}
func platformPackageView(artifact releaseArtifact, platform string, offline bool) RunnerPackage {
	query := ""
	if platform != releaseinfo.LinuxAMD64 {
		query = "?platform=" + platform
	}
	if offline {
		if query == "" {
			query = "?variant=offline"
		} else {
			query += "&variant=offline"
		}
	}
	return packageView(artifact, query)
}

func (m *Manager) RunnerPackagePath(offline bool) (string, RunnerPackage, error) {
	return m.RunnerPackageFor(releaseinfo.LinuxAMD64, offline)
}
func (m *Manager) RunnerPackageFor(platform string, offline bool) (string, RunnerPackage, error) {
	release, err := m.runnerRelease()
	if err != nil {
		return "", RunnerPackage{}, err
	}
	runner, ok := release.Runners[platform]
	if !ok {
		return "", RunnerPackage{}, errors.New("This Runner platform has no staged package")
	}
	artifact := runner.Installer
	if offline {
		artifact = runner.Offline
	} else if _, err := m.releaseArtifactPath(runner.Runtime); err != nil {
		return "", RunnerPackage{}, err
	}
	name, err := m.releaseArtifactPath(artifact)
	return name, platformPackageView(artifact, platform, offline), err
}

// Runtime archives remain content-addressed across releases so a previously
// downloaded installer never silently resolves to a newer execution environment.
func (m *Manager) runtimeDownload(w http.ResponseWriter, r *http.Request, _ storage.Runner) {
	digest := r.PathValue("sha256")
	if !validReleaseDigest(digest) {
		machineError(w, 404, "The pinned Camp runtime is unavailable.")
		return
	}
	filename, name := "", ""
	var st os.FileInfo
	var err error
	for _, platform := range releaseinfo.Platforms() {
		candidate := releaseinfo.RuntimeFor(platform.ID, digest)
		candidatePath := filepath.Join(m.config.DataDir, "packages", candidate)
		info, statErr := os.Lstat(candidatePath)
		if statErr == nil {
			filename, name, st = candidate, candidatePath, info
			break
		}
		err = statErr
	}
	if st != nil {
		err = nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() >= maxRunnerPackageBytes {
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
