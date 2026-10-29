package collaboration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func uploadService(t *testing.T) (*Service, string, storage.Room) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	manager, err := runs.New(ctx, store, dir, runs.Config{MaxTokens: 32, MaxSteps: 2, Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Shutdown(ctx) })
	service, err := New(ctx, store, manager, dir)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := store.ListRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return service, dir, rooms[0]
}

func TestUploadPostOnlyAndParallelRetriesWithoutModelConfiguration(t *testing.T) {
	s, dir, room := uploadService(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	refs := make(chan storage.Reference, 6)
	content := []byte("# Customer brief\nPreserve original bytes.\n")
	for range 6 {
		wg.Go(func() {
			ref, err := s.Upload(ctx, room.ID, "same-upload", "Customer brief.md", content)
			if err != nil {
				t.Error(err)
				return
			}
			refs <- ref
		})
	}
	wg.Wait()
	close(refs)
	var reference storage.Reference
	for ref := range refs {
		if reference.ResourceID != "" && ref.ResourceID != reference.ResourceID {
			t.Fatal("different retry resources")
		}
		reference = ref
	}
	if reference.ResourceID == "" {
		t.Fatal("no uploaded file")
	}
	directories, err := os.ReadDir(filepath.Join(dir, "resources"))
	if err != nil || len(directories) != 2 {
		t.Fatalf("duplicate resources or leftover staging: %v %v", directories, err)
	}
	for _, entry := range directories {
		if entry.Name() != reference.ResourceID && entry.Name() != room.ResourceID {
			t.Fatal(entry.Name())
		}
	}
	stored, err := s.ResourceFile(ctx, reference.ResourceID, reference.Path, reference.Commit)
	if err != nil || !bytes.Equal(stored, content) {
		t.Fatalf("bytes %q %v", stored, err)
	}
	sent, err := s.Send(ctx, room.ID, storage.SendMessageInput{RequestID: "post", Content: "Please keep this for our discussion.", PostOnly: true, References: []storage.ReferenceInput{{Kind: "file", ResourceID: reference.ResourceID, Commit: reference.Commit, Path: reference.Path}}})
	if err != nil || len(sent.Runs) != 0 || len(sent.Message.References) != 1 || sent.Message.References[0].Origin != "upload" {
		t.Fatalf("post %+v %v", sent, err)
	}
	current, err := s.store.GetRoom(ctx, room.ID)
	if err != nil || current.ResourceID != room.ResourceID {
		t.Fatalf("upload switched working files: %+v %v", current, err)
	}
	files, _, err := s.ResourceFiles(ctx, room.ResourceID, "")
	if err != nil || len(files) != 0 {
		t.Fatalf("upload modified working files: %v %v", files, err)
	}
	for _, change := range []struct {
		name    string
		content []byte
	}{{"changed.md", content}, {"Customer brief.md", []byte("Changed bytes")}} {
		_, err := s.Upload(ctx, room.ID, "same-upload", change.name, change.content)
		var failure *storage.Error
		if !errors.As(err, &failure) || failure.Code != "request_conflict" {
			t.Fatalf("changed retry %v", err)
		}
	}
	// Saved retries need no runtime dependency, including Git.
	t.Setenv("PATH", t.TempDir())
	replayed, err := s.Upload(ctx, room.ID, "same-upload", "Customer brief.md", content)
	if err != nil || replayed.ResourceID != reference.ResourceID {
		t.Fatalf("retry without Git %+v %v", replayed, err)
	}
	_, err = s.Upload(ctx, room.ID, "new-upload", "note.md", []byte("new"))
	var failure *storage.Error
	if !errors.As(err, &failure) || failure.Code != "execution_unavailable" {
		t.Fatalf("missing Git %v", err)
	}
}

func TestUploadRegistrationFailureRemovesUnregisteredObjects(t *testing.T) {
	s, dir, room := uploadService(t)
	db, err := sql.Open("sqlite", filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TRIGGER reject_upload BEFORE INSERT ON room_uploads BEGIN SELECT RAISE(ABORT, 'forced registration failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(context.Background(), room.ID, "fail", "brief.md", []byte("keep")); err == nil {
		t.Fatal("registration unexpectedly succeeded")
	}
	directories, err := os.ReadDir(filepath.Join(dir, "resources"))
	if err != nil || len(directories) != 1 || directories[0].Name() != room.ResourceID {
		t.Fatalf("unregistered files retained: %v %v", directories, err)
	}
	refs, _, err := s.store.ListRoomUploads(context.Background(), room.ID, "")
	if err != nil || len(refs) != 0 {
		t.Fatalf("failed receipt retained: %+v %v", refs, err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_upload"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(context.Background(), room.ID, "fail", "brief.md", []byte("keep")); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledUploadLeavesNoObjects(t *testing.T) {
	s, dir, room := uploadService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Upload(ctx, room.ID, "cancelled", "brief.md", []byte("original")); err == nil {
		t.Fatal("cancelled upload succeeded")
	}
	directories, err := os.ReadDir(filepath.Join(dir, "resources"))
	if err != nil || len(directories) != 1 {
		t.Fatalf("cancelled upload left files: %v %v", directories, err)
	}
}
