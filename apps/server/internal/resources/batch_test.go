package resources

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestImmutableExportUsesOneBatchAndPreservesArchive(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	files := snapshot{}
	for i := range 128 {
		name := fmt.Sprintf("file-%03d.bin", i)
		data := []byte{byte(i), 0, 255, '\n'}
		mode := os.FileMode(0644)
		if i%2 == 0 {
			mode = 0755
		}
		if err := os.WriteFile(filepath.Join(source, name), data, mode); err != nil {
			t.Fatal(err)
		}
		files[name] = file{content: data, executable: mode&0111 != 0}
	}
	m := New(filepath.Join(t.TempDir(), "resources"))
	id := strings.Repeat("a", 32)
	imported, err := m.Import(ctx, id, source)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := m.repository(id)
	// Packed objects must obey the same framing and byte/mode contract.
	if _, err = git(ctx, repo, nil, "gc", "--prune=now"); err != nil {
		t.Fatal(err)
	}
	actualGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := t.TempDir()
	log := filepath.Join(wrapper, "calls")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + quote(log) + "\nexec " + quote(actualGit) + " \"$@\"\n"
	if err = os.WriteFile(filepath.Join(wrapper, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
	var archive, expected bytes.Buffer
	if err = m.ExportArchive(ctx, id, imported.Commit, &archive); err != nil {
		t.Fatal(err)
	}
	if err = writeArchive(ctx, files, &expected); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archive.Bytes(), expected.Bytes()) {
		t.Fatal("batch export changed deterministic bytes or executable modes")
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "cat-file --batch") != 1 || len(strings.Split(strings.TrimSpace(string(calls)), "\n")) != 3 {
		t.Fatalf("export should use one ref check, one tree listing and one blob process: %s", calls)
	}
}

func TestBlobBatchRejectsMalformedTruncatedAndOversizedRecords(t *testing.T) {
	object := strings.Repeat("a", 40)
	entries := map[string]treeEntry{"note": {object: object}}
	for _, input := range []string{
		object + " missing\n",
		strings.Repeat("b", 40) + " blob 1\nx\n",
		object + " tree 1\nx\n",
		object + " blob -1\n",
		object + " blob 8388609\n",
		object + " blob 2\nx",
		object + " blob 1\nx!",
		strings.Repeat("x", 4096),
		object + " blob 1\nx\ntrailing",
	} {
		err := readBlobBatch(context.Background(), bufio.NewReader(strings.NewReader(input)), []string{"note"}, entries, func(string, treeEntry, []byte) error { return nil })
		if err == nil {
			t.Fatal("accepted an invalid object stream")
		}
	}
}

func TestBlobBatchCountsRepeatedObjectsAgainstProjectLimit(t *testing.T) {
	object := strings.Repeat("a", 40)
	data := bytes.Repeat([]byte{'x'}, MaxFileBytes)
	var names []string
	entries := map[string]treeEntry{}
	var readers []io.Reader
	for i := range MaxProjectBytes/MaxFileBytes + 1 {
		name := fmt.Sprint(i)
		names = append(names, name)
		entries[name] = treeEntry{object: object}
		readers = append(readers, strings.NewReader(fmt.Sprintf("%s blob %d\n", object, MaxFileBytes)), bytes.NewReader(data), strings.NewReader("\n"))
	}
	visited := 0
	err := readBlobBatch(context.Background(), bufio.NewReader(io.MultiReader(readers...)), names, entries, func(string, treeEntry, []byte) error { visited++; return nil })
	if err == nil || visited != MaxProjectBytes/MaxFileBytes {
		t.Fatal("reused objects bypassed the whole-project byte limit", visited, err)
	}
}

type pausedArchiveWriter struct {
	bytes.Buffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *pausedArchiveWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.Buffer.Write(data)
}

func TestSlowImmutableExportDoesNotHoldWriterLock(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "note.txt"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	m := New(filepath.Join(t.TempDir(), "resources"))
	id := strings.Repeat("a", 32)
	old, err := m.Import(ctx, id, source)
	if err != nil {
		t.Fatal(err)
	}
	output := &pausedArchiveWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	unblock := func() { release.Do(func() { close(output.release) }) }
	defer unblock()
	exported := make(chan error, 1)
	go func() { exported <- m.ExportArchive(ctx, id, old.Commit, output) }()
	select {
	case <-output.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("export never reached its output barrier")
	}
	var next bytes.Buffer
	if err = writeArchive(ctx, snapshot{"note.txt": {content: []byte("changed")}}, &next); err != nil {
		t.Fatal(err)
	}
	captured := make(chan error, 1)
	go func() { _, _, err := m.CaptureArchive(ctx, id, old.Commit, &next, Policy{}); captured <- err }()
	select {
	case err := <-captured:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a slow immutable reader blocked a new capture")
	}
	unblock()
	if err = <-exported; err != nil {
		t.Fatal(err)
	}
	files, err := readArchive(ctx, bytes.NewReader(output.Bytes()), Policy{})
	if err != nil || string(files["note.txt"].content) != "original" {
		t.Fatal("concurrent capture changed the pinned export", err)
	}
}

func TestCancelledBatchExportDrainsBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "note.txt"), bytes.Repeat([]byte{'x'}, 1<<20), 0644); err != nil {
		t.Fatal(err)
	}
	m := New(filepath.Join(t.TempDir(), "resources"))
	id := strings.Repeat("a", 32)
	old, err := m.Import(ctx, id, source)
	if err != nil {
		t.Fatal(err)
	}
	output := &pausedArchiveWriter{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	var release sync.Once
	unblock := func() { release.Do(func() { close(output.release) }) }
	defer unblock()
	go func() { done <- m.ExportArchive(ctx, id, old.Commit, output) }()
	select {
	case <-output.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("export never reached its barrier")
	}
	cancel()
	unblock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled export was accepted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled Git stream did not drain")
	}
}
