package documenttext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Set only while building the matching complete Server package.
var packagedManifestSHA256 string

type runtimeFile struct {
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	Executable bool   `json:"executable"`
}

type installation struct {
	Schema       int                    `json:"schema"`
	Platform     string                 `json:"platform"`
	NodeVersion  string                 `json:"nodeVersion"`
	PDFJSVersion string                 `json:"pdfjsVersion"`
	Files        map[string]runtimeFile `json:"files"`
}

// PackagedConfig verifies the runtime identity compiled into this Server.
// Source builds have no package pin and retain explicit development setup.
func PackagedConfig(program string) (Config, bool, error) {
	if packagedManifestSHA256 == "" {
		return Config{}, false, nil
	}
	program, err := filepath.EvalSymlinks(program)
	if err != nil {
		return Config{}, true, err
	}
	root := filepath.Join(filepath.Dir(program), "documents")
	return verifyInstallation(root, packagedManifestSHA256)
}

func verifyInstallation(root, expected string) (Config, bool, error) {
	bad := errors.New("The fixed Server document runtime failed verification. Reinstall this complete Camp version; team data is unchanged.")
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return Config{}, true, bad
	}
	info, err = os.Lstat(filepath.Join(root, "manifest.json"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 4<<20 {
		return Config{}, true, bad
	}
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	digest := sha256.Sum256(data)
	if err != nil || hex.EncodeToString(digest[:]) != expected {
		return Config{}, true, bad
	}
	var manifest installation
	if json.Unmarshal(data, &manifest) != nil || manifest.Schema != 1 || manifest.Platform != runtime.GOOS+"/"+runtime.GOARCH || manifest.NodeVersion != "24.16.0" || manifest.PDFJSVersion != "6.4.299" || len(manifest.Files) == 0 || len(manifest.Files) > 20000 {
		return Config{}, true, bad
	}
	for _, name := range []string{"bin/node", "bin/camp-sandbox", "runtime/extract.mjs", "runtime/node_modules/pdfjs-dist/package.json"} {
		if _, ok := manifest.Files[name]; !ok {
			return Config{}, true, bad
		}
	}
	seen := 0
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return bad
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return bad
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm()&0022 != 0 {
			return bad
		}
		if relative == "." || entry.IsDir() {
			return nil
		}
		if relative == "manifest.json" {
			return nil
		}
		expected, ok := manifest.Files[filepath.ToSlash(relative)]
		if !ok || !info.Mode().IsRegular() || info.Size() != expected.Bytes || expected.Bytes < 0 || expected.Bytes > 256<<20 || strings.Contains(relative, "\\") || (info.Mode().Perm()&0111 != 0) != expected.Executable {
			return bad
		}
		file, err := os.Open(name)
		if err != nil {
			return bad
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, io.LimitReader(file, expected.Bytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return bad
		}
		seen++
		return nil
	})
	if err != nil || seen != len(manifest.Files) {
		return Config{}, true, bad
	}
	return Config{NodePath: filepath.Join(root, "bin/node"), HelperPath: filepath.Join(root, "bin/camp-sandbox"), RuntimeDir: filepath.Join(root, "runtime")}, true, nil
}
