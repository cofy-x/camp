package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func fixtureBundle(t *testing.T) (string, bundleManifest) {
	t.Helper()
	directory := t.TempDir()
	manifest := bundleManifest{Schema: 1, Role: "runner", Platform: "linux/x64", Files: map[string]struct {
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	}{}, Symlinks: map[string]string{}}
	manifest.LayerFS.Image = "sha256:" + fmt.Sprintf("%064d", 1)
	for _, name := range []string{"bin/camp", "bin/node", "bin/camp-sandbox", "images/layerfs.tar", "runtime/dsh-runtime/package.json", "runtime/project-runtime/package.json", "runtime/layerfs-runtime/installed.json"} {
		content := []byte("fixture:" + name)
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		manifest.Files[name] = struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		}{hex.EncodeToString(sum[:]), int64(len(content))}
	}
	return directory, manifest
}
func writeBundleManifest(t *testing.T, dir string, manifest bundleManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if err = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(fmt.Sprintf("%x  manifest.json\n", sum)), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestBundleIntegrityAndRelativeLinks(t *testing.T) {
	directory, manifest := fixtureBundle(t)
	manifest.Symlinks["bin/camp-link"] = "camp"
	if err := os.Symlink("camp", filepath.Join(directory, "bin/camp-link")); err != nil {
		t.Fatal(err)
	}
	writeBundleManifest(t, directory, manifest)
	if _, err := verifyRunnerBundle(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bin/node"), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRunnerBundle(directory); err == nil {
		t.Fatal("Corrupted runtime was accepted")
	}
}
func TestBundleRejectsEscapingLinksAndMissingInventory(t *testing.T) {
	for _, kind := range []string{"link", "missing", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			directory, manifest := fixtureBundle(t)
			switch kind {
			case "link":
				manifest.Symlinks["escape"] = "/etc/passwd"
				if err := os.Symlink("/etc/passwd", filepath.Join(directory, "escape")); err != nil {
					t.Fatal(err)
				}
			case "missing":
				delete(manifest.Files, "bin/camp-sandbox")
			}
			writeBundleManifest(t, directory, manifest)
			if kind == "manifest" {
				if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := verifyRunnerBundle(directory); err == nil {
				t.Fatal("Invalid release was accepted", kind)
			}
		})
	}
}
