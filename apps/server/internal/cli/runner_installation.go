package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/releaseinfo"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type runnerInstallationReceipt struct {
	Schema         int        `json:"schema"`
	RunnerID       string     `json:"runnerId"`
	StorageID      string     `json:"storageId"`
	RegistrationID string     `json:"registrationId"`
	BundleDir      string     `json:"bundleDir"`
	Pin            runtimePin `json:"pin"`
	VerifiedAt     string     `json:"verifiedAt"`
}

// Called only after the immutable runtime passed setup's complete validation.
// This receipt records that scope; ordinary status never repeats that scan.
func saveRunnerInstallation(config daemon.Config, pin runtimePin) error {
	if installationFromPin(pin) == nil {
		return nil
	}
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		return err
	}
	if lock != nil {
		defer lock.Close()
	}
	current, err := daemon.LoadConfig(config.DataDir)
	if err != nil {
		return err
	}
	if current.RunnerID != config.RunnerID || current.StorageID != config.StorageID || current.RegistrationID != config.RegistrationID || current.BundleDir != config.BundleDir {
		return errors.New("Runner installation changed before recording its verification")
	}
	receipt := runnerInstallationReceipt{1, current.RunnerID, current.StorageID, current.RegistrationID, current.BundleDir, pin, time.Now().UTC().Format(time.RFC3339Nano)}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(config.DataDir, ".installation-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(file.Name(), filepath.Join(config.DataDir, "installation.json")); err != nil {
		return err
	}
	parent, err := os.Open(config.DataDir)
	if err != nil {
		return err
	}
	return errors.Join(parent.Sync(), parent.Close())
}

func readInstalledRunnerStatus(ctx context.Context, directory string) installedRunnerStatus {
	config, err := daemon.LoadConfig(directory)
	if err != nil {
		return installedRunnerStatus{State: "unknown"}
	}
	name := filepath.Join(directory, "installation.json")
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if config.BundleDir == "" && !hasRuntimePin() {
			return installedRunnerStatus{State: "development"}
		}
		return installedRunnerStatus{State: "unknown", Reason: "No package verification receipt is recorded. Repeat stopped setup to record this installation."}
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 {
		return installedRunnerStatus{State: "unknown", Reason: "The installation receipt is unavailable or invalid."}
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return installedRunnerStatus{State: "unknown"}
	}
	var record runnerInstallationReceipt
	if json.Unmarshal(data, &record) != nil || record.Schema != 1 || record.RunnerID != config.RunnerID || record.StorageID != config.StorageID || record.RegistrationID != config.RegistrationID || record.BundleDir != config.BundleDir || !validRecordedRuntimePin(record.Pin) {
		return installedRunnerStatus{State: "unknown", Reason: "The installation receipt does not match this Runner and runtime."}
	}
	if _, err = time.Parse(time.RFC3339Nano, record.VerifiedAt); err != nil {
		return installedRunnerStatus{State: "unknown"}
	}
	saved, err := filepath.EvalSymlinks(config.BundleDir)
	expected, e := filepath.EvalSymlinks(record.Pin.cache(directory))
	if err != nil || e != nil || saved != expected {
		return installedRunnerStatus{State: "unavailable", Reason: "The recorded runtime is missing. Repeat stopped setup with the same data directory."}
	}
	manifest := filepath.Join(saved, "manifest.json")
	info, err = os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return installedRunnerStatus{State: "invalid", Reason: "The recorded runtime manifest is unavailable or invalid."}
	}
	file, err := os.Open(manifest)
	if err != nil {
		return installedRunnerStatus{State: "unavailable"}
	}
	defer file.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, installReader{ctx: ctx, input: file})
	if err != nil || hex.EncodeToString(hash.Sum(nil)) != record.Pin.ManifestSHA256 {
		return installedRunnerStatus{State: "invalid", Reason: "The runtime manifest changed. Repeat stopped setup with the same data directory."}
	}
	return installedRunnerStatus{State: "recorded", Installation: installationFromPin(record.Pin), VerifiedAt: record.VerifiedAt}
}

func validRecordedRuntimePin(pin runtimePin) bool {
	platform, supported := releaseinfo.HostPlatform()
	return supported && pin.Schema == 1 && pin.Platform == platform.RuntimePlatform && pin.ProtocolVersion == p.Version && releaseinfo.ValidVersion(pin.ReleaseVersion) && installationFromPin(pin) != nil && digestPattern.MatchString(pin.ManifestSHA256) && pin.Bytes > 0 && pin.Bytes < releaseinfo.MaxArtifactBytes && pin.UnpackedBytes > 0 && pin.UnpackedBytes <= 8<<30
}
