package resources

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestZIPExportPreservesExactFilesModesAndDeterministicBytes(t *testing.T) {
	ctx := context.Background()
	m := New(filepath.Join(t.TempDir(), "resources"))
	id := strings.Repeat("a", 32)
	if _, err := m.Create(ctx, id); err != nil {
		t.Fatal(err)
	}
	repository, _ := m.repository(id)
	if err := materializeEmptyResource(ctx, repository); err != nil {
		t.Fatal(err)
	}
	files := snapshot{
		"README.md":       {content: []byte("# Notes\n")},
		"assets/data.bin": {content: []byte{0, 255, 1, 254}},
		"empty.txt":       {content: []byte{}},
		"scripts/build":   {content: []byte("#!/bin/sh\nexit 0\n"), executable: true},
	}
	commit, err := saveCommit(ctx, repository, EmptyRevision, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.WriteFile(ctx, id, commit, "README.md", []byte("Later version")); err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if err := m.ExportZIP(ctx, id, commit, &first); err != nil {
		t.Fatal(err)
	}
	if err := m.ExportZIP(ctx, id, commit, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("Immutable ZIP bytes changed between reads")
	}
	archive, err := zip.NewReader(bytes.NewReader(first.Bytes()), int64(first.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(archive.File))
	for _, entry := range archive.File {
		names = append(names, entry.Name)
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		original, ok := files[entry.Name]
		if err != nil || !ok || !bytes.Equal(data, original.content) {
			t.Fatalf("Archive file changed: %s %q %v", entry.Name, data, err)
		}
		mode := os.FileMode(0644)
		if original.executable {
			mode = 0755
		}
		if entry.Mode().Perm() != mode || !entry.Mode().IsRegular() {
			t.Fatalf("Archive mode changed: %s %s", entry.Name, entry.Mode())
		}
	}
	if !slices.Equal(names, []string{"README.md", "assets/data.bin", "empty.txt", "scripts/build"}) {
		t.Fatal("Archive paths are incomplete or not ordered", names)
	}
}

type failingZIPWriter struct{ err error }

func (w failingZIPWriter) Write([]byte) (int, error) { return 0, w.err }

func TestZIPExportEmptyCancellationAndOutputFailure(t *testing.T) {
	ctx := context.Background()
	m := New(filepath.Join(t.TempDir(), "resources"))
	id := strings.Repeat("a", 32)
	if _, err := m.Create(ctx, id); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := m.ExportZIP(ctx, id, EmptyRevision, &output); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil || len(archive.File) != 0 {
		t.Fatal("Empty resource did not produce a valid empty ZIP", err)
	}
	failure := errors.New("output unavailable")
	if err := m.ExportZIP(ctx, id, EmptyRevision, failingZIPWriter{failure}); !errors.Is(err, failure) {
		t.Fatal("ZIP finalization failure was ignored", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.ExportZIP(cancelled, id, EmptyRevision, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal("Cancelled export continued", err)
	}
	for _, revision := range []string{"HEAD", strings.Repeat("b", 40)} {
		if err := m.ExportZIP(ctx, id, revision, io.Discard); err == nil {
			t.Fatal("Unknown or symbolic revision exported", revision)
		}
	}
}

func TestZIPExportRejectsUnsafeAndNonportablePaths(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"../escape", ".env", "src\\escape", "C:/escape.txt", "notes:stream"} {
		t.Run(name, func(t *testing.T) {
			m := New(filepath.Join(t.TempDir(), "resources"))
			id := strings.Repeat("a", 32)
			if _, err := m.Create(ctx, id); err != nil {
				t.Fatal(err)
			}
			repository, _ := m.repository(id)
			if err := materializeEmptyResource(ctx, repository); err != nil {
				t.Fatal(err)
			}
			commit, err := saveCommit(ctx, repository, EmptyRevision, snapshot{name: {content: []byte("unsupported")}})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.ExportZIP(ctx, id, commit, io.Discard); err == nil {
				t.Fatal("ZIP exported a dangerous path", name)
			}
		})
	}
}
