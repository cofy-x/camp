package runinput

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type fileReaderFunc func(context.Context, string, string, string) ([]byte, error)

func (f fileReaderFunc) ReadFile(ctx context.Context, resource, commit, path string) ([]byte, error) {
	return f(ctx, resource, commit, path)
}

func TestPrepareReferencesUsesExactImmutableOriginals(t *testing.T) {
	originals := map[string][]byte{
		"nested/attachment.bin": {0, 255, '\n'},
		"brief.md":              []byte("# Selected brief\n\nThe saved decision.\n"),
	}
	reads := 0
	files := fileReaderFunc(func(_ context.Context, resource, commit, path string) ([]byte, error) {
		if resource != "source" || commit != "selected" || originals[path] == nil {
			t.Fatalf("read another version: %s %s %s", resource, commit, path)
		}
		reads++
		return originals[path], nil
	})
	run := storage.Run{ResourceID: "working", BaselineCommit: "current", References: []storage.Reference{
		{Kind: "message", Content: "Quoted context."},
		{Kind: "file", ResourceID: "source", Commit: "selected", Path: "nested/attachment.bin"},
		{Kind: "file", ResourceID: "source", Commit: "selected", Path: "brief.md"},
	}}
	directory, err := PrepareReferences(context.Background(), files, run, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, original := range originals {
		index := "2"
		if name == "brief.md" {
			index = "3"
		}
		file := filepath.Join(directory, index, name)
		data, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatal("immutable original changed", file, data, err)
		}
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("evidence file permissions changed", info, err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 || entries[0].Name() != "2" || entries[1].Name() != "3" || reads != 2 {
		t.Fatal("reference preparation produced more than the selected originals", entries, reads, err)
	}
}

func TestPrepareReferencesReturnsPartialDirectoryOnFailure(t *testing.T) {
	files := fileReaderFunc(func(_ context.Context, _, _, path string) ([]byte, error) {
		if path == "missing.txt" {
			return nil, errors.New("immutable file unavailable")
		}
		return []byte("Retain the confirmed original."), nil
	})
	run := storage.Run{References: []storage.Reference{
		{Kind: "file", Path: "first.txt"},
		{Kind: "file", Path: "missing.txt"},
	}}
	directory, err := PrepareReferences(context.Background(), files, run, t.TempDir())
	if err == nil || directory == "" {
		t.Fatal("partial evidence failure was not returned", directory, err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "1/first.txt"))
	if err != nil || string(data) != "Retain the confirmed original." {
		t.Fatal("caller lost its partial evidence directory", string(data), err)
	}
	if err = os.RemoveAll(directory); err != nil {
		t.Fatal("caller cannot clean partial evidence", err)
	}
}

func TestPrepareReferencesRejectsEscapingPath(t *testing.T) {
	files := fileReaderFunc(func(context.Context, string, string, string) ([]byte, error) { return []byte("original"), nil })
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("Preserve outside bytes."), 0600); err != nil {
		t.Fatal(err)
	}
	directory, err := PrepareReferences(context.Background(), files, storage.Run{References: []storage.Reference{{Kind: "file", Path: "../../outside.txt"}}}, parent)
	if err == nil || directory == "" {
		t.Fatal("escaping evidence path accepted", directory, err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "Preserve outside bytes." {
		t.Fatal("evidence copy escaped its owned directory", string(data), err)
	}
}
