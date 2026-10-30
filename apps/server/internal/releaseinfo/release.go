// Package releaseinfo owns immutable public release identities and artifact metadata.
package releaseinfo

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

const Repository = "cofy-x/camp"
const InstallerName = "camp-runner-linux-amd64.tar.gz"
const OfflineName = "camp-runner-offline-linux-amd64.tar.gz"
const MaxArtifactBytes = 2 << 30

// Set only by release builds, after the Runner archives have been hashed.
var manifestBase64 string

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$`)
var assetPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Artifact struct {
	Filename       string `json:"filename"`
	SHA256         string `json:"sha256"`
	Bytes          int64  `json:"bytes"`
	UnpackedBytes  int64  `json:"unpackedBytes"`
	ManifestSHA256 string `json:"manifestSHA256,omitempty"`
}

type RunnerArtifacts struct {
	Installer Artifact `json:"installer"`
	Runtime   Artifact `json:"runtime"`
	Offline   Artifact `json:"offline"`
}

type Descriptor struct {
	Schema          int                        `json:"schema"`
	Version         string                     `json:"version,omitempty"`
	SourceCommit    string                     `json:"sourceCommit,omitempty"`
	ProtocolVersion int                        `json:"protocolVersion,omitempty"`
	Runners         map[string]RunnerArtifacts `json:"runners"`
}

func ValidVersion(value string) bool { return versionPattern.MatchString(value) }

func ValidDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func RuntimeName(digest string) string { return "camp-runtime-linux-amd64-" + digest + ".tar.gz" }

func Parse(data []byte) (Descriptor, error) {
	var descriptor Descriptor
	if len(data) > 16<<10 {
		return descriptor, errors.New("The Runner release descriptor is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return descriptor, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || descriptor.Schema != 2 || len(descriptor.Runners) == 0 || len(descriptor.Runners) > len(Platforms()) {
		return descriptor, errors.New("Unsupported Runner release descriptor")
	}
	for platform, runner := range descriptor.Runners {
		if _, ok := PlatformFor(platform); !ok {
			return descriptor, errors.New("Unsupported Runner release platform")
		}
		for _, artifact := range []Artifact{runner.Installer, runner.Runtime, runner.Offline} {
			if !ValidDigest(artifact.SHA256) || artifact.Bytes <= 0 || artifact.Bytes >= MaxArtifactBytes || artifact.UnpackedBytes <= 0 || artifact.UnpackedBytes > 8<<30 {
				return descriptor, errors.New("Invalid Runner release artifact")
			}
		}
		if runner.Installer.Filename != InstallerFor(platform) || runner.Offline.Filename != OfflineFor(platform) || runner.Runtime.Filename != RuntimeFor(platform, runner.Runtime.SHA256) || !ValidDigest(runner.Runtime.ManifestSHA256) || runner.Installer.ManifestSHA256 != "" || runner.Offline.ManifestSHA256 != "" {
			return descriptor, errors.New("Invalid Runner release filename")
		}
	}
	if descriptor.Version != "" {
		commit, err := hex.DecodeString(descriptor.SourceCommit)
		if !ValidVersion(descriptor.Version) || err != nil || len(commit) != 20 || strings.ToLower(descriptor.SourceCommit) != descriptor.SourceCommit || descriptor.ProtocolVersion != p.Version {
			return descriptor, errors.New("The Runner release identity or protocol is incompatible")
		}
	} else if descriptor.SourceCommit != "" || descriptor.ProtocolVersion != 0 {
		return descriptor, errors.New("The Runner release identity is incomplete")
	}
	return descriptor, nil
}

func Embedded() (Descriptor, bool, error) {
	if manifestBase64 == "" {
		return Descriptor{}, false, nil
	}
	data, err := base64.StdEncoding.DecodeString(manifestBase64)
	if err != nil {
		return Descriptor{}, true, errors.New("Invalid embedded Runner release")
	}
	descriptor, err := Parse(data)
	if err == nil && !ValidVersion(descriptor.Version) {
		err = errors.New("An embedded Runner release requires a published version")
	}
	return descriptor, true, err
}

func DownloadURL(version, filename string) (string, error) {
	if !ValidVersion(version) || !assetPattern.MatchString(filename) {
		return "", errors.New("Invalid fixed GitHub release asset")
	}
	return "https://github.com/" + Repository + "/releases/download/" + version + "/" + filename, nil
}
