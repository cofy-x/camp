package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarkdownUpgradeRemovesDerivedArtifactsAndPreservesOriginalRecords(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 25)); err != nil {
		t.Fatal(err)
	}
	resourceID, roomID, commit := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 40)
	if _, err := s.db.Exec(`INSERT INTO resources(id,name,current_commit,created_at,updated_at) VALUES(?,?,?,?,?)`, resourceID, "Original attachment", commit, "created", "updated"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO rooms(id,name,resource_id,created_at,updated_at) VALUES(?,?,?,?,?)`, roomID, "Sources", resourceID, "created", "updated"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO room_uploads(room_id,request_id,resource_id,name,size,sha256,created_at) VALUES(?,?,?,?,?,?,?)`, roomID, "original-upload", resourceID, "attachment.bin", 4, strings.Repeat("d", 64), "created"); err != nil {
		t.Fatal(err)
	}
	refs, err := json.Marshal([]Reference{{Kind: "file", ResourceID: resourceID, Commit: commit, Path: "attachment.bin", Title: "Original attachment"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO messages(id,room_id,seq,author_kind,content,references_json,created_at,updated_at) VALUES(?,?,1,'user',?,?,?,?)`, "original-message", roomID, "Keep the original source.", string(refs), "created", "updated"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO documents(id,resource_id,commit_id,path,extractor_version,status,snapshot,created_at,updated_at) VALUES(?,?,?,?,?,'ready',?,?,?)`, "derived-artifact", resourceID, commit, "attachment.bin", "retired-reader", `{"sections":[{"text":"Derived text"}]}`, "created", "updated"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name IN ('documents','documents_file','documents_queue')", 0)
	assertScalar(t, s, "SELECT count(*) FROM resources", 1)
	assertScalar(t, s, "SELECT count(*) FROM room_uploads", 1)
	var savedCommit, savedName, savedContent, savedReferences string
	if err := s.db.QueryRow(`SELECT current_commit,name FROM resources WHERE id=?`, resourceID).Scan(&savedCommit, &savedName); err != nil || savedCommit != commit || savedName != "Original attachment" {
		t.Fatal("The original Resource changed", savedCommit, savedName, err)
	}
	if err := s.db.QueryRow(`SELECT content,references_json FROM messages WHERE id='original-message'`).Scan(&savedContent, &savedReferences); err != nil || savedContent != "Keep the original source." || savedReferences != string(refs) {
		t.Fatal("The original message or exact file reference changed", err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal("Reopening the upgraded schema failed", err)
	}
}
