package documenttext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func installationFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	manifest := installation{Schema: 1, Platform: runtime.GOOS + "/" + runtime.GOARCH, NodeVersion: "24.16.0", PDFJSVersion: "6.4.299", Files: map[string]runtimeFile{}}
	for _, name := range []string{"bin/node", "bin/camp-sandbox", "runtime/extract.mjs", "runtime/node_modules/pdfjs-dist/package.json"} {
		data := []byte(name)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		manifest.Files[name] = runtimeFile{SHA256: hex.EncodeToString(hash[:]), Bytes: int64(len(data))}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return root, hex.EncodeToString(digest[:])
}

func TestPackagedReadersVerifyEveryFixedInput(t *testing.T) {
	for _, mutate := range []string{"none", "changed file", "extra file", "manifest", "symlink", "writable", "group-writable directory", "world-writable directory", "missing"} {
		t.Run(mutate, func(t *testing.T) {
			root, digest := installationFixture(t)
			filename := filepath.Join(root, "runtime/extract.mjs")
			switch mutate {
			case "changed file":
				_ = os.WriteFile(filename, []byte("changed"), 0600)
			case "extra file":
				_ = os.WriteFile(filepath.Join(root, "unexpected"), nil, 0600)
			case "manifest":
				_ = os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{}`), 0600)
			case "symlink":
				_ = os.Remove(filename)
				_ = os.Symlink(filepath.Join(root, "bin/node"), filename)
			case "writable":
				_ = os.Chmod(filename, 0666)
			case "group-writable directory":
				if err := os.Chmod(filepath.Join(root, "runtime/node_modules/pdfjs-dist"), 0770); err != nil {
					t.Fatal(err)
				}
			case "world-writable directory":
				if err := os.Chmod(filepath.Join(root, "bin"), 0702); err != nil {
					t.Fatal(err)
				}
			case "missing":
				_ = os.Remove(filename)
			}
			config, packaged, err := verifyInstallation(root, digest)
			if !packaged || (err == nil) != (mutate == "none") {
				t.Fatalf("verified damaged runtime %s: %v", mutate, err)
			}
			if err == nil && config.NodePath != filepath.Join(root, "bin/node") {
				t.Fatal("runtime was not resolved relative to its verified installation")
			}
		})
	}
}
