package resources

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadFilenameAndSizeBoundary(t *testing.T) {
	for _, name := range []string{"", " ", ".", "..", "/brief.md", "../brief.md", "folder/brief.md", `folder\brief.md`, "a\nb", "a\x00b", "bad\xff", strings.Repeat("a", 256), ".env", ".ENV.local", ".npmrc", "id_ed25519", "credentials.json", "private.PEM"} {
		if err := ValidateUploadName(name); err == nil {
			t.Errorf("unsafe filename accepted: %q", name)
		}
	}
	for _, name := range []string{"brief.md", "Café notes.txt", "archive.zip", "photo.png", "dist", "coverage", strings.Repeat("a", 255)} {
		if err := ValidateUploadName(name); err != nil {
			t.Errorf("ordinary filename rejected: %q %v", name, err)
		}
	}
	manager := New(filepath.Join(t.TempDir(), "resources"))
	if _, err := manager.Upload(context.Background(), strings.Repeat("a", 32), "too-large.bin", make([]byte, MaxFileBytes+1)); err == nil {
		t.Fatal("oversized file accepted")
	}
}

func TestUploadPreservesBinaryEmptyAndMaximumBytesWithoutExtraction(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	manager := New(filepath.Join(dir, "resources"))
	for i, item := range []struct {
		name string
		data []byte
	}{{"empty.txt", []byte{}}, {"archive.zip", []byte{'P', 'K', 0, 1, 255}}, {"large.bin", bytes.Repeat([]byte{255}, MaxFileBytes)}} {
		id := strings.Repeat(string(rune('a'+i)), 32)
		result, err := manager.Upload(ctx, id, item.name, item.data)
		if err != nil {
			t.Fatal(err)
		}
		files, err := manager.ListFiles(ctx, id, result.Commit)
		if err != nil || len(files) != 1 || files[0] != item.name {
			t.Fatalf("unexpected archive extraction: %v %v", files, err)
		}
		got, err := manager.ReadFile(ctx, id, result.Commit, item.name)
		if err != nil || !bytes.Equal(got, item.data) {
			t.Fatalf("byte preservation %s: %v", item.name, err)
		}
		working := filepath.Join(dir, "read-"+id)
		if err := manager.Prepare(ctx, id, result.Commit, working); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(working, "project", item.name))
		if err != nil || info.Mode()&0111 != 0 {
			t.Fatalf("uploaded file executable: %v %v", info, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "resources"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("temporary files retained: %v %v", entries, err)
	}
}

func TestFailedGitUploadRemovesTemporaryRepository(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	manager := New(dir)
	if _, err := manager.Upload(context.Background(), strings.Repeat("a", 32), "note.txt", []byte("note")); err == nil {
		t.Fatal("upload unexpectedly succeeded without Git")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary repository retained: %v %v", entries, err)
	}
}
