package releaseinfo

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func descriptorFixture() Descriptor {
	digest := strings.Repeat("a", 64)
	return Descriptor{Schema: 2, Version: "v0.1.0-alpha.1", SourceCommit: strings.Repeat("b", 40), ProtocolVersion: p.Version, Runners: map[string]RunnerArtifacts{LinuxAMD64: {
		Installer: Artifact{Filename: InstallerName, SHA256: digest, Bytes: 10, UnpackedBytes: 20},
		Runtime:   Artifact{Filename: RuntimeName(digest), SHA256: digest, ManifestSHA256: digest, Bytes: 30, UnpackedBytes: 40},
		Offline:   Artifact{Filename: OfflineName, SHA256: digest, Bytes: 50, UnpackedBytes: 60}}}}
}

func TestDescriptorRejectsUnboundAndIncompatibleReleaseIdentities(t *testing.T) {
	valid := descriptorFixture()
	for _, change := range []func(*Descriptor){
		func(d *Descriptor) { d.Version = "latest" },
		func(d *Descriptor) { d.Version = "v0.01.0-alpha.1" },
		func(d *Descriptor) { d.SourceCommit = "main" },
		func(d *Descriptor) { d.ProtocolVersion++ },
		func(d *Descriptor) { d.Version = "" },
		func(d *Descriptor) {
			runner := d.Runners[LinuxAMD64]
			runner.Runtime.Filename = "../private"
			d.Runners[LinuxAMD64] = runner
		},
		func(d *Descriptor) {
			runner := d.Runners[LinuxAMD64]
			runner.Offline.Bytes = MaxArtifactBytes
			d.Runners[LinuxAMD64] = runner
		},
	} {
		invalid := descriptorFixture()
		change(&invalid)
		data, _ := json.Marshal(invalid)
		if _, err := Parse(data); err == nil {
			t.Fatal("Invalid release accepted", invalid)
		}
	}
	data, _ := json.Marshal(valid)
	if parsed, err := Parse(data); err != nil || !reflect.DeepEqual(parsed, valid) {
		t.Fatal(parsed, err)
	}
	for _, extra := range []string{` {}`, ` {"version":"v9.0.0"}`} {
		if _, err := Parse(append(append([]byte(nil), data...), extra...)); err == nil {
			t.Fatal("Trailing release content accepted")
		}
	}
}

func TestEmbeddedReleaseFailsClosedAndAssetURLsAreVersioned(t *testing.T) {
	old := manifestBase64
	t.Cleanup(func() { manifestBase64 = old })
	for _, encoded := range []string{"not-base64", base64.StdEncoding.EncodeToString([]byte(`{}`))} {
		manifestBase64 = encoded
		if _, present, err := Embedded(); !present || err == nil {
			t.Fatal("Invalid embedded release treated as a development build")
		}
	}
	descriptor := descriptorFixture()
	data, _ := json.Marshal(descriptor)
	manifestBase64 = base64.StdEncoding.EncodeToString(data)
	if got, present, err := Embedded(); !present || err != nil || !reflect.DeepEqual(got, descriptor) {
		t.Fatal(got, present, err)
	}
	address, err := DownloadURL(descriptor.Version, InstallerName)
	if err != nil || address != "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.1/camp-runner-linux-amd64.tar.gz" {
		t.Fatal(address, err)
	}
	for _, filename := range []string{"../file", "?token=private", "installer\n", "//other.invalid/file"} {
		if _, err := DownloadURL(descriptor.Version, filename); err == nil {
			t.Fatal("Unsafe release asset accepted", filename)
		}
	}
}

func TestDescriptorBindsEachPlatformToItsRuntimeAndManifest(t *testing.T) {
	descriptor := descriptorFixture()
	mac := descriptor.Runners[LinuxAMD64]
	mac.Installer.Filename = InstallerFor(DarwinARM64)
	mac.Offline.Filename = OfflineFor(DarwinARM64)
	mac.Runtime.Filename = RuntimeFor(DarwinARM64, mac.Runtime.SHA256)
	descriptor.Runners[DarwinARM64] = mac
	data, _ := json.Marshal(descriptor)
	if _, err := Parse(data); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []RunnerArtifacts{descriptor.Runners[LinuxAMD64], {Installer: mac.Installer, Offline: mac.Offline, Runtime: Artifact{Filename: mac.Runtime.Filename, SHA256: mac.Runtime.SHA256, Bytes: 30, UnpackedBytes: 40}}} {
		descriptor.Runners[DarwinARM64] = invalid
		data, _ = json.Marshal(descriptor)
		if _, err := Parse(data); err == nil {
			t.Fatal("foreign or unpinned platform runtime was accepted")
		}
	}
}
