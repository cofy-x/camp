package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type writer struct{ sync.Mutex }

func (w *writer) WithResourceWriter(_ context.Context, _ string, apply func() error) error {
	w.Lock()
	defer w.Unlock()
	return apply()
}
func fixture(t *testing.T) (context.Context, *Service, storage.Resource) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	s, err := storage.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureSetupToken(ctx, "setup"); err != nil {
		t.Fatal(err)
	}
	u, err := s.SetupAdmin(ctx, "setup", "alice", "Alice", strings.Repeat("p", 32), "session", "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	ctx = storage.WithUser(ctx, u)
	f := resources.New(filepath.Join(dir, "resources"))
	id := strings.Repeat("a", 32)
	created, err := f.Create(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateResource(ctx, storage.Resource{ID: id, Name: "Room files", CurrentCommit: created.Commit})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, New(s, f, &writer{}), r
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var p *storage.Error
	if !errors.As(err, &p) || p.Code != want {
		t.Fatalf("wanted %s, got %v", want, err)
	}
}

func TestSharedEditsConflictRetryAndRestore(t *testing.T) {
	ctx, s, r := fixture(t)
	first := Edit{RequestID: "first", ExpectedCommit: r.CurrentCommit, Path: "proposal.md", Content: "First draft\n"}
	var wg sync.WaitGroup
	out := make(chan Result, 2)
	for range 2 {
		wg.Go(func() {
			v, err := s.Edit(ctx, r.ID, first)
			if err != nil {
				t.Error(err)
			}
			out <- v
		})
	}
	wg.Wait()
	close(out)
	var saved Result
	for v := range out {
		if saved.Publication.ID != "" && saved.Publication.ID != v.Publication.ID {
			t.Fatal("duplicate publication")
		}
		saved = v
	}
	if saved.Publication.ChecksStatus != "not_run" || saved.Publication.AuthorName != "Alice" {
		t.Fatal(saved.Publication)
	}
	changed := first
	changed.Content = "Different retry"
	_, err := s.Edit(ctx, r.ID, changed)
	code(t, err, "request_conflict")
	stale := first
	stale.RequestID = "stale"
	_, err = s.Edit(ctx, r.ID, stale)
	code(t, err, "revision_conflict")
	second, err := s.Edit(ctx, r.ID, Edit{RequestID: "second", ExpectedCommit: saved.Resource.CurrentCommit, Path: "proposal.md", Content: "Revised draft\n"})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.Edit(ctx, r.ID, first)
	if err != nil || replayed.Resource.CurrentCommit != saved.Publication.Commit {
		t.Fatalf("retry pretended old text represented a newer shared version: %#v %v", replayed, err)
	}
	restored, err := s.Restore(ctx, r.ID, Restore{RequestID: "restore", ExpectedCommit: second.Resource.CurrentCommit, Commit: saved.Resource.CurrentCommit})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Resource.CurrentCommit == saved.Resource.CurrentCommit || restored.Resource.CurrentCommit == second.Resource.CurrentCommit {
		t.Fatal("restore rewrote history")
	}
	body, err := s.files.ReadFile(ctx, r.ID, restored.Resource.CurrentCommit, "proposal.md")
	if err != nil || string(body) != first.Content {
		t.Fatalf("restore bytes: %q %v", body, err)
	}
	history, err := s.store.ListPublications(ctx, r.ID, 0)
	if err != nil || len(history) != 3 || history[0].Kind != "restore" {
		t.Fatalf("history: %#v %v", history, err)
	}
	page, err := s.store.ListPublications(ctx, r.ID, history[1].Seq)
	if err != nil || len(page) != 1 || page[0].ID != saved.Publication.ID {
		t.Fatal(page, err)
	}
	patch, err := s.Patch(ctx, r.ID, saved.Publication.Commit, second.Publication.Commit)
	if err != nil || !strings.Contains(string(patch), "-First draft") || !strings.Contains(string(patch), "+Revised draft") {
		t.Fatalf("historical comparison: %q %v", patch, err)
	}
	// An immutable candidate whose database transaction failed is not published history.
	unregistered, err := s.files.WriteFile(ctx, r.ID, restored.Publication.Commit, "private.md", []byte("candidate"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Patch(ctx, r.ID, saved.Publication.Commit, unregistered)
	code(t, err, "invalid_reference")
	_, err = s.Patch(ctx, r.ID, "", second.Publication.Commit)
	code(t, err, "invalid_resource")
}

func TestPublicationFailureKeepsPointerAndAllowsSameRetry(t *testing.T) {
	ctx, s, r := fixture(t)
	user, _ := storage.UserFromContext(ctx)
	// Immutable objects can exist after a failed final transaction. They are not
	// automatically authoritative or exposed as a completed save.
	commit, err := s.files.WriteFile(ctx, r.ID, r.CurrentCommit, "draft.md", []byte("draft"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.store.Publish(storage.WithUser(ctx, storage.User{ID: "missing"}), storage.PublishInput{ResourceID: r.ID, ExpectedCommit: r.CurrentCommit, Commit: commit, Kind: "edit", Path: "draft.md", RequestID: "attempt", RequestHash: strings.Repeat("a", 64)})
	code(t, err, "forbidden")
	current, _ := s.store.GetResource(ctx, r.ID)
	if current.CurrentCommit != r.CurrentCommit {
		t.Fatal("failed transaction moved pointer")
	}
	history, _ := s.store.ListPublications(ctx, r.ID, 0)
	if len(history) != 0 {
		t.Fatal("failed transaction published receipt")
	}
	_, err = s.Edit(storage.WithUser(ctx, user), r.ID, Edit{RequestID: "attempt", ExpectedCommit: r.CurrentCommit, Path: "draft.md", Content: "draft"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBinaryWorkingCopyAndInputBoundaries(t *testing.T) {
	ctx, s, r := fixture(t)
	sourceID := strings.Repeat("b", 32)
	created, err := s.files.Create(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte{0, 1, 255, 128, 10}
	commit, err := s.files.WriteFile(ctx, sourceID, created.Commit, "asset.bin", binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateResource(ctx, storage.Resource{ID: sourceID, Name: "Original", CurrentCommit: commit}); err != nil {
		t.Fatal(err)
	}
	copied, err := s.Copy(ctx, r.ID, Copy{RequestID: "copy", ExpectedCommit: r.CurrentCommit, Path: "source/asset.bin", SourceResourceID: sourceID, SourceCommit: commit, SourcePath: "asset.bin"})
	if err != nil {
		t.Fatal(err)
	}
	content, err := s.files.ReadFile(ctx, r.ID, copied.Resource.CurrentCommit, "source/asset.bin")
	if err != nil || string(content) != string(binary) {
		t.Fatal(content, err)
	}
	for _, path := range []string{"../outside", ".env", "keys/secret.pem", "/absolute"} {
		_, err := s.Edit(ctx, r.ID, Edit{RequestID: "unsafe", ExpectedCommit: copied.Resource.CurrentCommit, Path: path, Content: "secret"})
		code(t, err, "invalid_resource")
	}
}

func TestUploadedOriginalIsVisibleImmutableAndCopyable(t *testing.T) {
	ctx, s, working := fixture(t)
	room, err := s.store.CreateRoom(ctx, storage.Room{Name: "Review", ResourceID: working.ID})
	if err != nil {
		t.Fatal(err)
	}
	originalID := strings.Repeat("c", 32)
	created, err := s.files.Create(ctx, originalID)
	if err != nil {
		t.Fatal(err)
	}
	originalBytes := []byte{0, 255, 1, 128, 10}
	commit, err := s.files.WriteFile(ctx, originalID, created.Commit, "source.bin", originalBytes)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(originalBytes)
	receipt, err := s.store.RegisterRoomUpload(ctx, room.ID, "upload-original", originalID, commit, "source.bin", len(originalBytes), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.store.LibraryResources(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("Library omitted uploaded evidence: %+v %v", items, err)
	}
	found := false
	for _, item := range items {
		if item.ID == originalID {
			found = true
			if !item.Immutable || item.Name != "source.bin" || item.Source == nil || item.Source.Commit != commit || item.Source.Origin != "upload" || item.Source.UploadedByUserID != receipt.Reference.UploadedByUserID {
				t.Fatalf("incorrect original metadata: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("uploaded original is missing from Library")
	}
	_, err = s.Edit(ctx, originalID, Edit{RequestID: "edit-original", ExpectedCommit: commit, Path: "source.bin", Content: "replacement"})
	code(t, err, "invalid_resource")
	_, err = s.Restore(ctx, originalID, Restore{RequestID: "restore-original", ExpectedCommit: commit, Commit: commit})
	code(t, err, "invalid_resource")
	_, err = s.Copy(ctx, originalID, Copy{RequestID: "copy-into-original", ExpectedCommit: commit, Path: "another.bin", SourceResourceID: originalID, SourceCommit: commit, SourcePath: "source.bin"})
	code(t, err, "invalid_resource")
	copy, err := s.Copy(ctx, working.ID, Copy{RequestID: "create-working-copy", ExpectedCommit: working.CurrentCommit, Path: "source.bin", SourceResourceID: originalID, SourceCommit: commit, SourcePath: "source.bin"})
	if err != nil {
		t.Fatal(err)
	}
	content, err := s.files.ReadFile(ctx, working.ID, copy.Resource.CurrentCommit, "source.bin")
	if err != nil || !bytes.Equal(content, originalBytes) {
		t.Fatal("working copy changed binary content", err)
	}
	original, err := s.store.GetResource(ctx, originalID)
	if err != nil || original.CurrentCommit != commit {
		t.Fatal("original revision changed", err)
	}
	content, err = s.files.ReadFile(ctx, originalID, commit, "source.bin")
	if err != nil || !bytes.Equal(content, originalBytes) {
		t.Fatal("original bytes changed", err)
	}
	history, err := s.store.ListPublications(ctx, originalID, 0)
	if err != nil || len(history) != 0 {
		t.Fatal("rejected original writes created publication receipts", err)
	}
}
