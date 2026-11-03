// Package testfixture seeds historical records without production write APIs.
package testfixture

import (
	"database/sql"
	"encoding/json"
	"testing"
)

// Document inserts a retained artifact into a fresh test database. It does not
// enqueue parsing or claim that the fixture bytes were processed by a reader.
func Document(t testing.TB, db *sql.DB, snapshot any) {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		ID, ResourceID, Commit, Path, ExtractorVersion, Status, CreatedAt, UpdatedAt string
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO documents(id,resource_id,commit_id,path,extractor_version,status,snapshot,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, metadata.ID, metadata.ResourceID, metadata.Commit, metadata.Path, metadata.ExtractorVersion, metadata.Status, string(data), metadata.CreatedAt, metadata.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
}
