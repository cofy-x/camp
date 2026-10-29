package resources

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteArchiveRoundTripPreservesBytesAndMode(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	original := []byte{0, 255, 1, 128}
	if err := os.WriteFile(filepath.Join(source, "binary"), original, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".env"), []byte("must stay private"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot")
	if err := CaptureStableSource(ctx, source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".env")); !os.IsNotExist(err) {
		t.Fatal("credential copied")
	}
	var first, second bytes.Buffer
	if err := WriteArchive(ctx, destination, &first); err != nil {
		t.Fatal(err)
	}
	if err := WriteArchive(ctx, destination, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("archive is not deterministic")
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := ExtractArchive(ctx, bytes.NewReader(first.Bytes()), restored); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(restored, "binary"))
	if err != nil || !bytes.Equal(content, original) {
		t.Fatal("binary changed", err)
	}
	info, err := os.Stat(filepath.Join(restored, "binary"))
	if err != nil || info.Mode()&0111 == 0 {
		t.Fatal("execute bit lost", err)
	}
	manager := New(filepath.Join(t.TempDir(), "resources"))
	id := "01234567890123456789012345678901"
	imported, err := manager.ImportArchive(ctx, id, bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := manager.ImportArchive(ctx, id, bytes.NewReader(first.Bytes()))
	if err != nil || repeated.Commit != imported.Commit {
		t.Fatal("idempotent import failed", repeated, err)
	}
	var changed bytes.Buffer
	tw := tar.NewWriter(&changed)
	_ = tw.WriteHeader(&tar.Header{Name: "different", Typeflag: tar.TypeReg, Mode: 0644})
	_ = tw.Close()
	if _, err = manager.ImportArchive(ctx, id, bytes.NewReader(changed.Bytes())); err == nil {
		t.Fatal("different import replaced reserved resource")
	}
	var exported bytes.Buffer
	if err = manager.ExportArchive(ctx, id, imported.Commit, &exported); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), exported.Bytes()) {
		t.Fatal("immutable transport changed bytes")
	}
}

func TestRemoteArchiveRejectsLinksAndTraversalBeforeWriting(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "../escape", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink},
		{Name: ".env", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "special", Typeflag: tar.TypeFifo},
	} {
		t.Run(header.Name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := tar.NewWriter(&archive)
			if err := writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "extracted")
			if err := ExtractArchive(context.Background(), &archive, destination); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("partial destination created")
			}
		})
	}
}

func TestStableSourceRejectsSymlinkWithoutChangingOriginal(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(source, "link")); err != nil {
		t.Skip(err)
	}
	if err := CaptureStableSource(context.Background(), source, filepath.Join(t.TempDir(), "snapshot")); err == nil {
		t.Fatal("symlink captured")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "outside" {
		t.Fatal("source modified", err)
	}
}

func TestSourceVerificationRejectsReplacementWithIdenticalContents(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	name := filepath.Join(source, "note.txt")
	if err := os.WriteFile(name, []byte("Stable content"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := readSnapshot(ctx, source, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(name, filepath.Join(t.TempDir(), "previous.txt")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, first["note.txt"].content, 0644); err != nil {
		t.Fatal(err)
	}
	second, err := readSnapshot(ctx, source, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifySourceSnapshots(first, second); err == nil {
		t.Fatal("replacing a captured file was invisible to source stability verification")
	}
	if err = verifySourceSnapshots(second, second); err != nil {
		t.Fatal("stable source was rejected", err)
	}
}
