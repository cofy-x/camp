package storage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func registerUpload(t *testing.T, s *Store, room, requestID string) UploadReceipt {
	t.Helper()
	r, err := s.RegisterRoomUpload(ctx, room, requestID, newID(), strings.Repeat("b", 40), "brief.md", 7, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRoomUploadRequestIDsRemainLookupable(t *testing.T) {
	s, room, _ := roomFixture(t)
	for _, id := range []string{".", ".."} {
		_, err := s.RegisterRoomUpload(ctx, room.ID, id, newID(), strings.Repeat("b", 40), "brief.md", 7, strings.Repeat("c", 64))
		requireCode(t, err, "invalid_upload")
		_, err = s.GetRoomUpload(ctx, room.ID, id)
		requireCode(t, err, "invalid_upload")
	}
	for _, id := range []string{".request", "request..id"} {
		saved := registerUpload(t, s, room.ID, id)
		got, err := s.GetRoomUpload(ctx, room.ID, id)
		if err != nil || got.Reference.ResourceID != saved.Reference.ResourceID {
			t.Fatalf("lookup %q: %+v %v", id, got, err)
		}
	}
}

func TestRoomUploadRetryConcurrencyAndIsolation(t *testing.T) {
	s, room, _ := roomFixture(t)
	var wg sync.WaitGroup
	receipts := make(chan UploadReceipt, 8)
	for range 8 {
		wg.Go(func() { receipts <- registerUpload(t, s, room.ID, "same-request") })
	}
	wg.Wait()
	close(receipts)
	first, err := s.GetRoomUpload(ctx, room.ID, "same-request")
	if err != nil {
		t.Fatal(err)
	}
	for receipt := range receipts {
		if receipt.Reference.ResourceID != first.Reference.ResourceID {
			t.Fatal("concurrent retries created different receipts")
		}
	}
	assertScalar(t, s, "SELECT count(*) FROM room_uploads", 1)
	assertScalar(t, s, "SELECT count(*) FROM resources", 2)
	assertScalar(t, s, "SELECT count(*) FROM workspace_events WHERE type='upload.created'", 1)
	assertScalar(t, s, "SELECT count(*) FROM runs", 0)
	for _, changed := range []struct {
		name   string
		size   int
		digest string
	}{
		{"other.md", 7, strings.Repeat("c", 64)}, {"brief.md", 8, strings.Repeat("c", 64)}, {"brief.md", 7, strings.Repeat("d", 64)},
	} {
		_, err := s.RegisterRoomUpload(ctx, room.ID, "same-request", newID(), strings.Repeat("b", 40), changed.name, changed.size, changed.digest)
		requireCode(t, err, "request_conflict")
	}
	resources, err := s.ListResources(ctx)
	if err != nil || len(resources) != 1 || resources[0].ID != room.ResourceID {
		t.Fatalf("uploads leaked into working resources: %+v %v", resources, err)
	}
	upload := first.Reference
	r, err := s.GetResource(ctx, upload.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateResource(ctx, r)
	requireCode(t, err, "invalid_resource")
	changed := room
	changed.ResourceID = upload.ResourceID
	_, err = s.UpdateRoom(ctx, changed)
	requireCode(t, err, "invalid_resource")
	_, err = s.CreateRoom(ctx, Room{Name: "Invalid working files", ResourceID: upload.ResourceID})
	requireCode(t, err, "invalid_resource")
	ref := ReferenceInput{Kind: "file", ResourceID: upload.ResourceID, Commit: upload.Commit, Path: upload.Path}
	sent, err := sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: "posted", Content: "Read this later.", PostOnly: true, References: []ReferenceInput{ref}}, roomBudget)
	if err != nil || len(sent.Runs) != 0 || len(sent.Message.References) != 1 {
		t.Fatalf("post %+v %v", sent, err)
	}
	canonical := sent.Message.References[0]
	if canonical.Origin != "upload" || canonical.Size == nil || *canonical.Size != 7 || canonical.Title != "brief.md" || canonical.CreatedAt != upload.CreatedAt {
		t.Fatal(canonical)
	}
	other, err := s.CreateRoom(ctx, Room{Name: "Other room", ResourceID: room.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sendToAssistant(s, ctx, other.ID, SendMessageInput{RequestID: "cross-room", Content: "Read this.", PostOnly: true, References: []ReferenceInput{ref}}, roomBudget)
	if err != nil {
		t.Fatal("shared team evidence should be available across rooms", err)
	}
	_, err = s.GetRoomUpload(ctx, other.ID, "same-request")
	requireCode(t, err, "not_found")
	_, _, err = s.ListRoomUploads(ctx, other.ID, upload.ResourceID)
	requireCode(t, err, "invalid_upload")
	ref.Path = "missing.md"
	_, err = sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: "missing", Content: "Read this.", PostOnly: true, References: []ReferenceInput{ref}}, roomBudget)
	requireCode(t, err, "invalid_reference")
}

func TestRoomUploadRegistrationRollsBackReceiptResourceAndEvent(t *testing.T) {
	for _, table := range []string{"room_uploads", "workspace_events"} {
		t.Run(table, func(t *testing.T) {
			s, room, _ := roomFixture(t)
			if _, err := s.db.Exec("CREATE TRIGGER reject_upload BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT, 'forced registration failure'); END"); err != nil {
				t.Fatal(err)
			}
			_, err := s.RegisterRoomUpload(ctx, room.ID, "failed", newID(), strings.Repeat("b", 40), "brief.md", 7, strings.Repeat("c", 64))
			if err == nil {
				t.Fatal("registration unexpectedly succeeded")
			}
			assertScalar(t, s, "SELECT count(*) FROM room_uploads", 0)
			assertScalar(t, s, "SELECT count(*) FROM resources", 1)
			assertScalar(t, s, "SELECT count(*) FROM workspace_events WHERE type='upload.created'", 0)
			if _, err := s.db.Exec("DROP TRIGGER reject_upload"); err != nil {
				t.Fatal(err)
			}
			registerUpload(t, s, room.ID, "failed")
		})
	}
}

func TestRoomUploadsPaginationAndRestart(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "uploads.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	room, err := s.SeedRoom(ctx, Resource{ID: "room-resource", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 205 {
		registerUpload(t, s, room.ID, fmt.Sprintf("file-%03d", i))
	}
	// Identical timestamps must still produce complete, nonoverlapping pages.
	if _, err := s.db.Exec("UPDATE room_uploads SET created_at='2026-10-27T00:00:00.000Z'"); err != nil {
		t.Fatal(err)
	}
	page, next, err := s.ListRoomUploads(ctx, room.ID, "")
	if err != nil || len(page) != 200 || next == "" {
		t.Fatalf("first page %d %s %v", len(page), next, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	last, cursor, err := s.ListRoomUploads(ctx, room.ID, next)
	if err != nil || len(last) != 5 || cursor != "" {
		t.Fatalf("last page %d %s %v", len(last), cursor, err)
	}
	seen := map[string]bool{}
	for _, ref := range append(page, last...) {
		if seen[ref.ResourceID] {
			t.Fatal("duplicate pagination result")
		}
		seen[ref.ResourceID] = true
	}
	receipt, err := s.GetRoomUpload(ctx, room.ID, "file-000")
	if err != nil || !seen[receipt.Reference.ResourceID] || receipt.Reference.Size == nil {
		t.Fatalf("restart receipt %+v %v", receipt, err)
	}
}

func TestUploadMigrationPreservesExistingRoomMessageAndResource(t *testing.T) {
	s := unmigratedStore(t)
	sources := migrationSources(t)
	previous := fstest.MapFS{"00001_initial.sql": sources["00001_initial.sql"], "00002_message_references.sql": sources["00002_message_references.sql"]}
	if err := s.applyMigrations(ctx, previous); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO resources(id,name,current_commit,created_at,updated_at) VALUES('kept-files','Files','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','now','now')`,
		`INSERT INTO rooms(id,name,resource_id,created_at,updated_at) VALUES('kept-room','Kept room','kept-files','now','now')`,
		`INSERT INTO messages(id,room_id,seq,author_kind,content,created_at,updated_at,references_json) VALUES('kept-message','kept-room',1,'user','Preserve this discussion','now','now','[]')`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	message, err := s.GetRoomMessage(ctx, "kept-room", "kept-message")
	if err != nil || message.Content != "Preserve this discussion" || string(mustJSON(t, message.References)) != "[]" {
		t.Fatalf("message %+v %v", message, err)
	}
	registerUpload(t, s, "kept-room", "after-migration")
	assertScalar(t, s, "SELECT count(*) FROM messages", 1)
	assertScalar(t, s, "SELECT count(*) FROM resources", 2)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
