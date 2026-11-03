package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/releaseinfo"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestInstallationReceiptRecordsOnlyItsVerifiedScope(t *testing.T) {
	platform, supported := releaseinfo.HostPlatform()
	if !supported {
		t.Skip("Supported Runner host")
	}
	directory := t.TempDir()
	manifest := []byte(`{"schema":2}`)
	sum := sha256.Sum256(manifest)
	pin := runtimePin{Schema: 1, ReleaseVersion: "v0.1.0-alpha.10", SourceCommit: strings.Repeat("a", 40), Platform: platform.RuntimePlatform, ProtocolVersion: p.Version, SHA256: strings.Repeat("b", 64), ManifestSHA256: hex.EncodeToString(sum[:]), Bytes: 1, UnpackedBytes: 1}
	bundle := pin.cache(directory)
	if err := os.MkdirAll(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	// This opaque payload deliberately differs from any runtime manifest. Status
	// checks metadata provenance, never claims a new complete file verification.
	payload := filepath.Join(bundle, "payload")
	if err := os.WriteFile(payload, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	config := daemon.Config{DataDir: directory, RunnerID: "runner", StorageID: "storage", RegistrationID: "registration", BundleDir: bundle, Executor: "fixture", Slots: 1, Credential: "do-not-display"}
	raw, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(directory, "daemon.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveRunnerInstallation(config, pin); err != nil {
		t.Fatal(err)
	}
	got := readInstalledRunnerStatus(context.Background(), directory)
	if got.State != "recorded" || got.VerifiedAt == "" || got.Installation == nil || got.Installation.SourceCommit != pin.SourceCommit {
		t.Fatal(got)
	}
	if err := os.WriteFile(payload, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if got = readInstalledRunnerStatus(context.Background(), directory); got.State != "recorded" {
		t.Fatal("Ordinary status rechecked payloads outside its declared scope", got)
	}
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if got = readInstalledRunnerStatus(context.Background(), directory); got.State != "invalid" || got.Installation != nil {
		t.Fatal("Changed metadata remained verified", got)
	}
	config.StorageID = "foreign"
	raw, _ = json.Marshal(config)
	if err := os.WriteFile(filepath.Join(directory, "daemon.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if got = readInstalledRunnerStatus(context.Background(), directory); got.State != "unknown" || got.Installation != nil {
		t.Fatal("Foreign receipt attributed to current installation", got)
	}
}
