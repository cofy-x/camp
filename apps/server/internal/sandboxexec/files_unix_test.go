//go:build linux || darwin

package sandboxexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func fileRequest(t *testing.T, roots Roots, request Request) Frame {
	t.Helper()
	request.Roots = roots
	frame, err := files(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func requireFailure(t *testing.T, err error, code string) {
	t.Helper()
	var failure *failure
	if !errors.As(err, &failure) || failure.code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestBinaryFilesGuardsAndRanges(t *testing.T) {
	roots := Roots{Workspace: t.TempDir()}
	data := bytes.Repeat([]byte{0, 255, 10, 13, 128, 1}, 18000)
	written := fileRequest(t, roots, Request{Operation: "write", Path: "/workspace/data.bin", Data: data, ExpectedVersion: "missing"})
	read := fileRequest(t, roots, Request{Operation: "read", Path: "/workspace/data.bin"})
	if !bytes.Equal(read.Data, data) || read.Info.Version != written.Info.Version || !strings.HasPrefix(read.Info.Version, "sha256:") {
		t.Fatal("binary bytes or content identity changed")
	}
	length := int64(11)
	part := fileRequest(t, roots, Request{Operation: "read", Path: "/workspace/data.bin", Offset: 7, Length: &length})
	if !bytes.Equal(part.Data, data[7:18]) || part.Info.Version != "" {
		t.Fatal("range read was not exact or claimed a complete content identity")
	}
	for _, version := range []string{"missing", "sha256:stale"} {
		_, err := files(context.Background(), Request{Operation: "write", Roots: roots, Path: "/workspace/data.bin", Data: []byte("wrong"), ExpectedVersion: version})
		requireFailure(t, err, "version_conflict")
	}
	fileRequest(t, roots, Request{Operation: "write", Path: "/workspace/data.bin", Data: []byte("updated"), ExpectedVersion: written.Info.Version})
	if remaining, _ := os.ReadFile(filepath.Join(roots.Workspace, "data.bin")); string(remaining) != "updated" {
		t.Fatal("guarded write did not publish its exact content")
	}
	entries, err := os.ReadDir(roots.Workspace)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary write files leaked: %v %v", entries, err)
	}
}

func TestFilePathsRejectEscapesAndSpecialFiles(t *testing.T) {
	roots := Roots{Workspace: t.TempDir(), References: t.TempDir()}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(roots.Workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots.Workspace, "inside"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside", filepath.Join(roots.Workspace, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/proc/self/fd/1", filepath.Join(roots.Workspace, "magic")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/etc/passwd", "/workspace-other/inside", "/workspace/../secret", "/workspace//inside", "/workspace/./inside", "/workspace/escape/secret", "/workspace/link", "/workspace/magic"} {
		for _, operation := range []string{"read", "stat", "write"} {
			_, err := files(context.Background(), Request{Operation: operation, Roots: roots, Path: name, Data: []byte("tampered")})
			if err == nil {
				t.Fatalf("%s permitted %s", operation, name)
			}
		}
	}
	link := fileRequest(t, roots, Request{Operation: "lstat", Path: "/workspace/link"})
	if link.Info.Kind != "symlink" || link.Info.Version != "" {
		t.Fatal("lstat did not report the link itself")
	}
	listed := fileRequest(t, roots, Request{Operation: "list", Path: "/workspace", Recursive: true})
	if len(listed.Entries) != 4 || listed.Entries[0].Kind != "symlink" {
		t.Fatalf("list followed a link or failed to report metadata: %+v", listed.Entries)
	}
	if err := unix.Mkfifo(filepath.Join(roots.Workspace, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := files(context.Background(), Request{Operation: "read", Roots: roots, Path: "/workspace/fifo"})
	requireFailure(t, err, "unsupported_file")
	if data, _ := os.ReadFile(filepath.Join(outside, "secret")); string(data) != "outside" {
		t.Fatal("outside content changed")
	}
	for _, operation := range []string{"write", "mkdir", "remove", "rename"} {
		_, err := files(context.Background(), Request{Operation: operation, Roots: roots, Path: "/references/source", Destination: "/workspace/moved", Data: []byte("no")})
		requireFailure(t, err, "read_only")
	}
}

func TestFileDirectoryOperationsAndLiteralSearch(t *testing.T) {
	roots := Roots{Workspace: t.TempDir(), References: t.TempDir()}
	fileRequest(t, roots, Request{Operation: "mkdir", Path: "/workspace/a/b", Recursive: true})
	fileRequest(t, roots, Request{Operation: "write", Path: "/workspace/a/b/notes.txt", Data: []byte("one\nneedle.* literal\nthree\n")})
	fileRequest(t, roots, Request{Operation: "write", Path: "/workspace/a/b/raw.bin", Data: []byte{0, 'n', 'e', 'e', 'd', 'l', 'e'}})
	result := fileRequest(t, roots, Request{Operation: "search", Path: "/workspace/a", Pattern: "needle.*"})
	if len(result.Matches) != 1 || result.Matches[0].Line != 2 || result.Matches[0].Path != "/workspace/a/b/notes.txt" {
		t.Fatalf("unexpected literal search: %+v", result)
	}
	fileRequest(t, roots, Request{Operation: "rename", Path: "/workspace/a/b/notes.txt", Destination: "/workspace/a/b/moved.txt"})
	_, err := files(context.Background(), Request{Operation: "rename", Roots: roots, Path: "/workspace/a/b/raw.bin", Destination: "/workspace/a/b/moved.txt"})
	requireFailure(t, err, "already_exists")
	fileRequest(t, roots, Request{Operation: "remove", Path: "/workspace/a", Recursive: true})
	if _, err := os.Stat(filepath.Join(roots.Workspace, "a")); !os.IsNotExist(err) {
		t.Fatalf("recursive removal left files: %v", err)
	}
	_, err = files(context.Background(), Request{Operation: "remove", Roots: roots, Path: "/workspace", Recursive: true})
	requireFailure(t, err, "invalid_path")
}

func TestLargeRangeReadIsBoundedWithoutHashingWholeFile(t *testing.T) {
	roots := Roots{Workspace: t.TempDir()}
	file, err := os.Create(filepath.Join(roots.Workspace, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	length := int64(7)
	read := fileRequest(t, roots, Request{Operation: "read", Path: "/workspace/large", Offset: 1<<30 - 7, Length: &length})
	if len(read.Data) != 7 || read.Info.Size != 1<<30 {
		t.Fatal("ranged read did not preserve size or requested length")
	}
	_, err = files(context.Background(), Request{Operation: "read", Roots: roots, Path: "/workspace/large"})
	requireFailure(t, err, "file_too_large")
}

func TestSymlinkReplacementCannotRedirectReads(t *testing.T) {
	roots := Roots{Workspace: t.TempDir()}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(roots.Workspace, "directory")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "file"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if os.Rename(inside, inside+"-held") == nil {
				_ = os.Symlink(outside, inside)
				_ = os.Remove(inside)
				_ = os.Rename(inside+"-held", inside)
			}
		}
	}()
	for i := 0; i < 100; i++ {
		frame, err := files(context.Background(), Request{Operation: "read", Roots: roots, Path: "/workspace/directory/file"})
		if err == nil && string(frame.Data) != "inside" {
			close(stop)
			worker.Wait()
			t.Fatal("a racing symbolic link escaped the root")
		}
	}
	close(stop)
	worker.Wait()
}
