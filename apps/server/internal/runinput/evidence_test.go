package runinput

import (
	"bytes"
	"context"
	"encoding/json"
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

type documentReaderFunc func(context.Context, string) (storage.Document, error)

func (f documentReaderFunc) GetDocument(ctx context.Context, id string) (storage.Document, error) {
	return f(ctx, id)
}

func TestPrepareReferencesUsesExactImmutableBytesAndDocument(t *testing.T) {
	original := []byte{'P', 'D', 'F', 0, 255, '\n'}
	document := storage.Document{ID: "prepared", ResourceID: "source", Commit: "selected", Path: "nested/brief.pdf", Status: "ready", Sections: []storage.DocumentSection{{ID: "p1", Label: "Page 1", Text: "The saved decision."}}}
	files := fileReaderFunc(func(_ context.Context, resource, commit, path string) ([]byte, error) {
		if resource != document.ResourceID || commit != document.Commit || path != document.Path {
			t.Fatalf("read another version: %s %s %s", resource, commit, path)
		}
		return original, nil
	})
	documents := documentReaderFunc(func(_ context.Context, id string) (storage.Document, error) {
		if id != document.ID {
			t.Fatal("read another document", id)
		}
		return document, nil
	})
	run := storage.Run{ResourceID: "working", BaselineCommit: "current", References: []storage.Reference{
		{Kind: "message", Content: "Quoted context."},
		{Kind: "file", ResourceID: document.ResourceID, Commit: document.Commit, Path: document.Path, Document: document.Summary()},
	}}
	directory, err := PrepareReferences(context.Background(), documents, files, run, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "2", document.Path))
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("immutable original changed", data, err)
	}
	info, err := os.Stat(filepath.Join(directory, "2", document.Path))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("evidence file permissions changed", info, err)
	}
	text, err := os.ReadFile(filepath.Join(directory, ".documents/2/text.txt"))
	if err != nil || string(text) != "## Page 1 (p1)\nThe saved decision.\n\n" {
		t.Fatal("prepared text changed", string(text), err)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, ".documents/2/manifest.json"))
	expected, _ := json.Marshal(document)
	if err != nil || !bytes.Equal(manifest, expected) {
		t.Fatal("prepared manifest changed", string(manifest), err)
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
	directory, err := PrepareReferences(context.Background(), nil, files, run, t.TempDir())
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

func TestPrepareReferencesRejectsMismatchedDocumentAndEscapingPath(t *testing.T) {
	files := fileReaderFunc(func(context.Context, string, string, string) ([]byte, error) { return []byte("original"), nil })
	for _, change := range []func(*storage.Document){
		func(d *storage.Document) { d.ResourceID = "other" },
		func(d *storage.Document) { d.Commit = "newer" },
		func(d *storage.Document) { d.Path = "other.pdf" },
		func(d *storage.Document) { d.Status = "failed" },
	} {
		document := storage.Document{ResourceID: "source", Commit: "selected", Path: "brief.pdf", Status: "ready"}
		change(&document)
		documents := documentReaderFunc(func(context.Context, string) (storage.Document, error) { return document, nil })
		run := storage.Run{References: []storage.Reference{{Kind: "file", ResourceID: "source", Commit: "selected", Path: "brief.pdf", Document: &storage.DocumentSummary{ID: "prepared", Status: "ready"}}}}
		directory, err := PrepareReferences(context.Background(), documents, files, run, t.TempDir())
		if err == nil || directory == "" {
			t.Fatal("mismatched document accepted", document, err)
		}
		if _, err = os.Stat(filepath.Join(directory, ".documents")); !os.IsNotExist(err) {
			t.Fatal("mismatched prepared text was written", err)
		}
	}
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("Preserve outside bytes."), 0600); err != nil {
		t.Fatal(err)
	}
	directory, err := PrepareReferences(context.Background(), nil, files, storage.Run{References: []storage.Reference{{Kind: "file", Path: "../../outside.txt"}}}, parent)
	if err == nil || directory == "" {
		t.Fatal("escaping evidence path accepted", directory, err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "Preserve outside bytes." {
		t.Fatal("evidence copy escaped its owned directory", string(data), err)
	}
}
