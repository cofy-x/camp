package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type bundleManifest struct {
	Schema   int    `json:"schema"`
	Role     string `json:"role"`
	Platform string `json:"platform"`
	LayerFS  struct {
		Image string `json:"image"`
	} `json:"layerfs"`
	CheckpointWorker checkpointImagePin `json:"checkpointWorker"`
	Files            map[string]struct {
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	} `json:"files"`
	Symlinks map[string]string `json:"symlinks"`
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Verify every shipped regular file and relative link before loading the image.
// Checksums establish integrity; publisher authenticity is a release-channel concern.
func verifyRunnerBundle(directory string) (bundleManifest, error) {
	var manifest bundleManifest
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return manifest, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return manifest, err
	}
	content, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return manifest, err
	}
	sums, err := os.Open(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		return manifest, err
	}
	defer sums.Close()
	expected := ""
	scanner := bufio.NewScanner(sums)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "  ", 2)
		if len(parts) == 2 && parts[1] == "manifest.json" {
			if expected != "" {
				return manifest, errors.New("Duplicate manifest checksum")
			}
			expected = parts[0]
		}
	}
	if err = scanner.Err(); err != nil {
		return manifest, err
	}
	digest := sha256.Sum256(content)
	if !digestPattern.MatchString(expected) || hex.EncodeToString(digest[:]) != expected {
		return manifest, errors.New("Runner manifest checksum failed; download the release again")
	}
	if err = json.Unmarshal(content, &manifest); err != nil {
		return manifest, err
	}
	legacyBundle := manifest.Schema == 1 && manifest.Role == "runner"
	managedRuntime := manifest.Schema == 2 && manifest.Role == "runner-runtime"
	linux := manifest.Platform == "linux/x64"
	mac := manifest.Platform == "darwin/arm64" && managedRuntime
	if (!legacyBundle && !managedRuntime) || (!linux && !mac) || (linux && (!strings.HasPrefix(manifest.LayerFS.Image, "sha256:") || !digestPattern.MatchString(strings.TrimPrefix(manifest.LayerFS.Image, "sha256:")))) || (mac && manifest.LayerFS.Image != "") {
		return manifest, errors.New("This is not a supported Camp Runner bundle")
	}
	if manifest.CheckpointWorker.Image != "" && (!mac || !manifest.CheckpointWorker.valid()) {
		return manifest, errors.New("Invalid optional checkpoint worker image pin")
	}
	inside := func(relative string) (string, error) {
		if !filepath.IsLocal(relative) || filepath.ToSlash(filepath.Clean(relative)) != relative {
			return "", fmt.Errorf("Invalid bundle path %q", relative)
		}
		name := filepath.Join(root, relative)
		parent, err := filepath.EvalSymlinks(filepath.Dir(name))
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, parent)
		if err != nil || (!filepath.IsLocal(rel) && rel != ".") {
			return "", fmt.Errorf("Bundle path escapes installation: %s", relative)
		}
		return name, nil
	}
	for relative, entry := range manifest.Files {
		name, err := inside(relative)
		if err != nil {
			return manifest, err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return manifest, err
		}
		if !info.Mode().IsRegular() || info.Size() != entry.Bytes || !digestPattern.MatchString(entry.SHA256) {
			return manifest, fmt.Errorf("Invalid bundle file: %s", relative)
		}
		file, err := os.Open(name)
		if err != nil {
			return manifest, err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		err = errors.Join(err, file.Close())
		if err != nil {
			return manifest, err
		}
		if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return manifest, fmt.Errorf("Bundle checksum failed: %s", relative)
		}
	}
	for relative, target := range manifest.Symlinks {
		name, err := inside(relative)
		if err != nil {
			return manifest, err
		}
		actual, err := os.Readlink(name)
		if err != nil || actual != target || filepath.IsAbs(target) {
			return manifest, fmt.Errorf("Invalid bundle link: %s", relative)
		}
		resolved, err := filepath.EvalSymlinks(name)
		if err != nil {
			return manifest, err
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || !filepath.IsLocal(rel) {
			return manifest, fmt.Errorf("Bundle link escapes installation: %s", relative)
		}
	}
	requiredFiles := []string{"bin/node", "bin/camp-sandbox", "runtime/dsh-runtime/package.json"}
	if linux {
		requiredFiles = append(requiredFiles, "images/layerfs.tar", "runtime/layerfs-runtime/installed.json")
	} else {
		requiredFiles = append(requiredFiles, "runtime/project-runtime/package.json")
		for name := range manifest.Files {
			if strings.HasPrefix(name, "images/") || strings.HasPrefix(name, "runtime/layerfs-runtime/") {
				return manifest, errors.New("The Mac native runtime must not include a Linux execution backend")
			}
		}
	}
	if legacyBundle {
		requiredFiles = append(requiredFiles, "bin/camp")
	}
	for _, required := range requiredFiles {
		if _, ok := manifest.Files[required]; !ok {
			return manifest, fmt.Errorf("Runner bundle is missing %s", required)
		}
	}
	if err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "manifest.json" || relative == "SHA256SUMS" {
			return nil
		}
		if _, ok := manifest.Files[relative]; ok {
			return nil
		}
		if _, ok := manifest.Symlinks[relative]; ok {
			return nil
		}
		return fmt.Errorf("Unrecorded file in Runner installation: %s", relative)
	}); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func loadRunnerImage(ctx context.Context, bundle, dockerSocket, image string) error {
	prefix := []string{"--host", "unix://" + dockerSocket}
	inspect := func() error {
		return exec.CommandContext(ctx, "docker", append(prefix, "image", "inspect", image)...).Run()
	}
	if inspect() == nil {
		return nil
	}
	output, err := exec.CommandContext(ctx, "docker", append(prefix, "image", "load", "--input", filepath.Join(bundle, "images", "layerfs.tar"))...).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if len(text) > 2048 {
			text = text[:2048]
		}
		return fmt.Errorf("Cannot load the bundled execution image: %w: %s", err, text)
	}
	if err = inspect(); err != nil {
		return errors.New("Docker did not confirm the bundled execution image")
	}
	return nil
}

func runnerBundleDirectory() string {
	if value := os.Getenv("CAMP_BUNDLE_DIR"); value != "" {
		return value
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	root := filepath.Dir(filepath.Dir(exe))
	if _, err = os.Stat(filepath.Join(root, "manifest.json")); err == nil {
		return root
	}
	return ""
}
