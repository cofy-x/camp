package nativeexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/layerfs"
)

func TestPersistExportVerifiesBeforeRecordingManifest(t *testing.T) {
	directory := t.TempDir()
	name := filepath.Join(directory, "result.tar")
	content := []byte("immutable artifact")
	if err := os.WriteFile(name, content, 0600); err != nil {
		t.Fatal(err)
	}
	archive := layerfs.ExportedArchive{ArchivePath: name, Bytes: int64(len(content)), SHA256: "invalid"}
	if err := persistExport(archive); err == nil {
		t.Fatal("corrupt export accepted")
	}
	manifestName := filepath.Join(directory, "artifact.json")
	if _, err := os.Stat(manifestName); !os.IsNotExist(err) {
		t.Fatal("corrupt archive produced a manifest")
	}
	sum := sha256.Sum256(content)
	archive.SHA256 = hex.EncodeToString(sum[:])
	if err := persistExport(archive); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(manifestName)
	var saved layerfs.ExportedArchive
	if err != nil || json.Unmarshal(manifest, &saved) != nil || saved.ArchivePath != archive.ArchivePath || saved.SHA256 != archive.SHA256 || saved.Bytes != archive.Bytes {
		t.Fatalf("invalid durable manifest: %s %v", manifest, err)
	}
}
