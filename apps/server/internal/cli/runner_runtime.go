package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/releaseinfo"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

// Set by the release build after hashing its immutable runtime archive. The
// Server cannot select another runtime or redirect this installer elsewhere.
var runtimePinJSON string

type runtimePin struct {
	Schema          int    `json:"schema"`
	ReleaseVersion  string `json:"releaseVersion,omitempty"`
	Platform        string `json:"platform"`
	ProtocolVersion int    `json:"protocolVersion"`
	SHA256          string `json:"sha256"`
	ManifestSHA256  string `json:"manifestSHA256"`
	Bytes           int64  `json:"bytes"`
	UnpackedBytes   int64  `json:"unpackedBytes"`
}

func compiledRuntimePin() (runtimePin, error) {
	var pin runtimePin
	if runtimePinJSON == "" {
		release, present, err := releaseinfo.Embedded()
		if err != nil {
			return pin, err
		}
		platform, supported := releaseinfo.HostPlatform()
		if !present || !supported {
			return pin, errors.New("Use a supported Camp release or Runner installer; source builds require --development-runtime")
		}
		runner, ok := release.Runners[platform.ID]
		if !ok {
			return pin, errors.New("This release does not include a Runner runtime for this platform")
		}
		pin = runtimePin{Schema: 1, ReleaseVersion: release.Version, Platform: platform.RuntimePlatform, ProtocolVersion: release.ProtocolVersion, SHA256: runner.Runtime.SHA256, ManifestSHA256: runner.Runtime.ManifestSHA256, Bytes: runner.Runtime.Bytes, UnpackedBytes: runner.Runtime.UnpackedBytes}
	} else if err := json.Unmarshal([]byte(runtimePinJSON), &pin); err != nil {
		return pin, errors.New("The Camp Runner installer has an invalid runtime pin")
	}
	platform, supported := releaseinfo.HostPlatform()
	if !supported || pin.Schema != 1 || pin.Platform != platform.RuntimePlatform || pin.ProtocolVersion != p.Version || !digestPattern.MatchString(pin.SHA256) || !digestPattern.MatchString(pin.ManifestSHA256) || pin.Bytes <= 0 || pin.Bytes >= releaseinfo.MaxArtifactBytes || pin.UnpackedBytes <= 0 || pin.UnpackedBytes > 8<<30 {
		return pin, errors.New("The Camp Runner installer has an unsupported runtime pin")
	}
	if pin.ReleaseVersion != "" && !releaseinfo.ValidVersion(pin.ReleaseVersion) {
		return pin, errors.New("The Camp Runner installer has an invalid release version")
	}
	return pin, nil
}

func hasRuntimePin() bool {
	_, present, _ := releaseinfo.Embedded()
	return runtimePinJSON != "" || present
}

func (pin runtimePin) target() string {
	platform, _ := releaseinfo.PlatformForRuntime(pin.Platform)
	return platform.ID
}
func (pin runtimePin) filename() string { return releaseinfo.RuntimeFor(pin.target(), pin.SHA256) }
func (pin runtimePin) cache(directory string) string {
	return filepath.Join(directory, "runtime", "cache", pin.SHA256)
}

func verifyServiceRuntime(directory, savedBundle string, pin runtimePin) error {
	repair := errors.New("Complete setup with this installer before installing its service")
	if !filepath.IsAbs(savedBundle) {
		return repair
	}
	// Setup may save /var/... while launchd uses /private/var/.... Compare the
	// same physical cache without rewriting configuration or Runner identity.
	installed, err := filepath.EvalSymlinks(savedBundle)
	if err != nil {
		return errors.Join(repair, err)
	}
	expected, err := filepath.EvalSymlinks(pin.cache(directory))
	if err != nil || installed != expected {
		return errors.Join(repair, err)
	}
	_, err = verifyPinnedRuntime(expected, pin)
	if err != nil {
		return errors.Join(repair, err)
	}
	return nil
}

func verifyPinnedRuntime(directory string, pin runtimePin) (bundleManifest, error) {
	return verifyPinnedRuntimeContext(context.Background(), directory, pin, nil)
}

func verifyPinnedRuntimeContext(ctx context.Context, directory string, pin runtimePin, progress *installPhase) (bundleManifest, error) {
	if err := ctx.Err(); err != nil {
		return bundleManifest{}, err
	}
	data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return bundleManifest{}, err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != pin.ManifestSHA256 {
		return bundleManifest{}, errors.New("The installed runtime manifest differs from this installer’s fixed release")
	}
	manifest, err := verifyRunnerBundleContext(ctx, directory, progress)
	if err == nil && (manifest.Role != "runner-runtime" || manifest.Platform != pin.Platform) {
		err = errors.New("The pinned artifact is not a Camp execution runtime")
	}
	return manifest, err
}

func installRunnerRuntime(ctx context.Context, config daemon.Config, pin runtimePin, output io.Writer) (string, bundleManifest, error) {
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		return "", bundleManifest{}, err
	}
	if lock != nil {
		defer lock.Close()
	}
	var report p.ReadinessReport
	err = installationPhase(ctx, output, "Checking Runner host prerequisites", 0, func(_ *installPhase) error {
		report = nativeexecutor.HostChecks(nativeexecutor.Config{DockerSocket: config.DockerSocket, DockerBinary: config.DockerBinary, WorkspaceBackend: config.WorkspaceBackend})
		for _, check := range report.Checks {
			if check.Status != "passed" {
				return errors.New("Runner host prerequisites need attention")
			}
		}
		return nil
	})
	if ctx.Err() != nil {
		return "", bundleManifest{}, ctx.Err()
	}
	if err != nil {
		for _, check := range report.Checks {
			if check.Status != "passed" {
				fmt.Fprintf(output, "%s: %s\n", check.Label, check.Detail)
			}
		}
		return "", bundleManifest{}, errors.Join(errors.New("Runner registration is saved. Correct the host requirements and repeat setup; no runtime download was started"), daemon.WriteReadiness(config.DataDir, report))
	}
	executable, err := os.Executable()
	if err != nil {
		return "", bundleManifest{}, err
	}
	installed, err := installRuntimePackage(ctx, config, pin, output, filepath.Join(filepath.Dir(executable), pin.filename()))
	if err != nil {
		report := p.ReadinessReport{Ready: false, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Checks: []p.ReadinessCheck{{ID: "runtime", Label: "Fixed execution runtime", Status: "failed", Detail: err.Error()}}}
		err = errors.Join(err, daemon.WriteReadiness(config.DataDir, report))
	}
	return installed.directory, installed.manifest, err
}

type runtimeInstallation struct {
	directory string
	manifest  bundleManifest
}

func installRuntime(ctx context.Context, config daemon.Config, pin runtimePin, output io.Writer, offline string) (string, error) {
	result, err := installRuntimePackage(ctx, config, pin, output, offline)
	return result.directory, err
}

func installRuntimePackage(ctx context.Context, config daemon.Config, pin runtimePin, output io.Writer, offline string) (result runtimeInstallation, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	fmt.Fprintf(output, "Fixed runtime: %.1f MiB compressed download; %.1f MiB unpacked files. Download completion does not finish setup.\n", float64(pin.Bytes)/(1<<20), float64(pin.UnpackedBytes)/(1<<20))
	destination := pin.cache(config.DataDir)
	quarantine := ""
	if _, checkErr := os.Lstat(destination); checkErr == nil {
		var manifest bundleManifest
		err = installationPhase(ctx, output, "Checking installed runtime cache", 0, func(progress *installPhase) error {
			var e error
			manifest, e = verifyPinnedRuntimeContext(ctx, destination, pin, progress)
			return e
		})
		if err == nil {
			fmt.Fprintln(output, "Using the verified installed runtime. No download or extraction is needed.")
			return runtimeInstallation{destination, manifest}, nil
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		quarantine = destination + fmt.Sprintf(".invalid-%d", time.Now().UnixNano())
		if err = os.Rename(destination, quarantine); err != nil {
			return result, err
		}
		fmt.Fprintln(output, "Replacing a damaged runtime cache. Runner identity and work are preserved.")
	} else if !errors.Is(checkErr, os.ErrNotExist) {
		return result, checkErr
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return result, err
	}
	downloads := filepath.Join(config.DataDir, "runtime", "downloads")
	if err = os.MkdirAll(downloads, 0700); err != nil {
		return result, err
	}
	archivePath := filepath.Join(downloads, pin.SHA256+".part")
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	defer archive.Close()
	archiveInfo, err := archive.Stat()
	if err != nil {
		return result, err
	}
	if archiveInfo.Size() == pin.Bytes {
		fmt.Fprintln(output, "Using the saved runtime download; every byte will be verified before installation.")
	} else if offlineInfo, checkErr := os.Lstat(offline); checkErr == nil {
		if !offlineInfo.Mode().IsRegular() || offlineInfo.Size() != pin.Bytes {
			return result, errors.New("The offline runtime archive has an unexpected size or type")
		}
		err = installationPhase(ctx, output, "Copying included offline runtime", pin.Bytes, func(progress *installPhase) error {
			source, e := os.Open(offline)
			if e != nil {
				return e
			}
			if e = archive.Truncate(0); e != nil {
				_ = source.Close()
				return e
			}
			_, copyErr := io.Copy(archive, installReader{ctx: ctx, input: source, phase: progress})
			return errors.Join(copyErr, source.Close(), archive.Sync())
		})
		if err != nil {
			return result, err
		}
	} else if !errors.Is(checkErr, os.ErrNotExist) {
		return result, checkErr
	} else {
		if archiveInfo.Size() > 0 && archiveInfo.Size() < pin.Bytes {
			fmt.Fprintf(output, "Resuming the saved runtime transfer (%s received). Runner registration is unchanged.\n", installationSize(archiveInfo.Size()))
		}
		public := config.RuntimeSource == "github" || (config.RuntimeSource == "" && pin.ReleaseVersion != "")
		if public {
			fmt.Fprintf(output, "Downloading GitHub release %s\n", pin.ReleaseVersion)
		} else {
			fmt.Fprintln(output, "Downloading the explicitly staged Camp Server runtime.")
		}
		err = installationPhase(ctx, output, "Runtime download", pin.Bytes, func(progress *installPhase) error {
			offset := archiveInfo.Size()
			if offset > pin.Bytes {
				offset = 0
			}
			progress.transferred(offset)
			if public {
				return daemon.DownloadPublicRuntime(ctx, pin.ReleaseVersion, pin.SHA256, pin.Bytes, archive, progress.transferred)
			}
			return daemon.DownloadRuntime(ctx, config, pin.SHA256, pin.Bytes, archive, progress.transferred)
		})
		if err != nil {
			return result, fmt.Errorf("Runtime download did not finish; repeat setup with the same data directory to resume: %w", err)
		}
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	err = installationPhase(ctx, output, "Verifying runtime archive", pin.Bytes, func(progress *installPhase) error {
		hash := sha256.New()
		size, e := io.Copy(hash, installReader{ctx: ctx, input: archive, phase: progress})
		if e != nil {
			return e
		}
		if size != pin.Bytes || hex.EncodeToString(hash.Sum(nil)) != pin.SHA256 {
			_ = archive.Close()
			_ = os.Remove(archivePath)
			return errors.New("Runtime checksum failed; no runtime was installed. Repeat setup to download it again")
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".install-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(stage)
	err = installationPhase(ctx, output, "Validating and extracting runtime archive", pin.UnpackedBytes, func(progress *installPhase) error {
		return extractRuntimeProgress(ctx, archive, stage, pin.UnpackedBytes, pin.target(), progress)
	})
	if err != nil {
		return result, err
	}
	var manifest bundleManifest
	err = installationPhase(ctx, output, "Validating fixed runtime files", 0, func(progress *installPhase) error {
		var e error
		manifest, e = verifyPinnedRuntimeContext(ctx, stage, pin, progress)
		return e
	})
	if err != nil {
		return result, err
	}
	err = installationPhase(ctx, output, "Committing verified runtime", 0, func(_ *installPhase) error {
		if e := os.Rename(stage, destination); e != nil {
			return e
		}
		parent, e := os.Open(filepath.Dir(destination))
		if e != nil {
			return e
		}
		return errors.Join(parent.Sync(), parent.Close())
	})
	if err != nil {
		return result, err
	}
	_ = archive.Close()
	_ = os.Remove(archivePath)
	if quarantine != "" {
		_ = os.RemoveAll(quarantine)
	}
	fmt.Fprintln(output, "Runtime installation verified. Setup continues with host preparation and self-test.")
	return runtimeInstallation{destination, manifest}, nil
}

func extractRuntime(ctx context.Context, input io.Reader, directory string, expectedBytes int64, platform string) error {
	return extractRuntimeProgress(ctx, input, directory, expectedBytes, platform, nil)
}

func extractRuntimeProgress(ctx context.Context, input io.Reader, directory string, expectedBytes int64, platform string, progress *installPhase) error {
	if _, supported := releaseinfo.PlatformFor(platform); !supported {
		return errors.New("Unsupported runtime archive platform")
	}
	prefix := "camp-runtime-" + platform
	compressed, err := gzip.NewReader(installReader{ctx: ctx, input: input})
	if err != nil {
		return err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	seen := map[string]bool{}
	links := map[string]string{}
	var unpacked int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == prefix && header.Typeflag == tar.TypeDir {
			continue
		}
		name, ok := strings.CutPrefix(name, prefix+"/")
		if !ok || !filepath.IsLocal(name) || path.Clean(name) != name || strings.Contains(name, "\\") || seen[name] || len(seen) >= 100000 {
			return errors.New("The runtime archive contains an invalid or duplicate path")
		}
		seen[name] = true
		progress.advance(0, 1)
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > expectedBytes-unpacked {
				return errors.New("The runtime archive exceeds its pinned expanded size")
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0755)
			if err != nil {
				return err
			}
			n, copyErr := io.CopyN(file, installReader{ctx: ctx, input: archive, phase: progress}, header.Size)
			err = errors.Join(copyErr, file.Sync(), file.Close())
			if err != nil {
				return err
			}
			unpacked += n
		case tar.TypeSymlink:
			resolved := path.Clean(path.Join(path.Dir(name), header.Linkname))
			if header.Linkname == "" || path.IsAbs(header.Linkname) || !filepath.IsLocal(resolved) || strings.Contains(header.Linkname, "\\") {
				return errors.New("The runtime archive contains an escaping link")
			}
			links[target] = header.Linkname
		default:
			return errors.New("The runtime archive contains an unsupported entry")
		}
	}
	if unpacked != expectedBytes {
		return errors.New("The runtime archive does not match its pinned expanded size")
	}
	// Links are created last: archive writes can never traverse a symlink.
	for name, target := range links {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Symlink(target, name); err != nil {
			return err
		}
	}
	return filepath.WalkDir(directory, func(name string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		return errors.Join(file.Sync(), file.Close())
	})
}
