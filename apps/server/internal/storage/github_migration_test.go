package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGitHubActiveSourceMigrationPreservesIdentitiesOriginalsAndReceipts(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 19)); err != nil {
		t.Fatal(err)
	}
	actor, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, actor, "legacy")
	room, writer := remoteTestRoom(t, s, actor, runner)
	source := testGitHubSource(t, s, actor, room)
	payload := testGitHubPayload("Original source", "  Source context before the migration.\n你好\n ")
	event := receiveTestGitHub(t, s, source, payload)
	input := CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), SourceEventID: event.ID, Goal: "Keep the original task and source identity.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
	task, err := s.CreateTask(actor, room.ID, input, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	beforeSource, _ := s.GitHubSourceCredential(ctx, source.ID)
	beforeTask, _ := s.queries().GetTask(ctx, task.ID)
	beforeInputs, _ := s.TaskInputs(ctx, task.ID, 0, task.InputRevision)
	var sourceReceipt string
	if err = s.db.QueryRow("SELECT result FROM github_requests WHERE json_extract(result,'$.source.id')=?", source.ID).Scan(&sourceReceipt); err != nil {
		t.Fatal(err)
	}
	beforeEvent, _ := s.GetGitHubEvent(ctx, event.ID)
	before, _ := json.Marshal(beforeEvent)
	var receipt string
	if err = s.db.QueryRow("SELECT result FROM task_requests WHERE request_id=?", input.RequestID).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	// A corrupt reference must fail the table rebuild atomically, retaining its
	// migration prefix and all original children so the operator can repair it.
	if _, err = s.db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE github_sources SET created_by='missing-user' WHERE id=?", source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if err = s.applyMigrations(ctx, migrationSourcesThrough(t, 20)); err == nil {
		t.Fatal("corrupt parent reference passed migration")
	}
	var version int
	var schema string
	var saved int
	if err = s.db.QueryRow("SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil || version != 19 {
		t.Fatal(version, err)
	}
	if err = s.db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='github_sources'").Scan(&schema); err != nil || !strings.Contains(schema, "UNIQUE(room_id,repository)") {
		t.Fatal("original schema not restored", schema, err)
	}
	if err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='github_sources_saved'").Scan(&saved); err != nil || saved != 0 {
		t.Fatal("partial rebuild retained", saved, err)
	}
	if _, err = s.db.Exec("UPDATE github_sources SET created_by=? WHERE id=?", beforeSource.CreatedBy, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.applyMigrations(ctx, migrationSourcesThrough(t, 20)); err != nil {
		t.Fatal(err)
	}
	afterSource, err := s.GitHubSourceCredential(ctx, source.ID)
	if err != nil || afterSource != beforeSource {
		t.Fatal("source identity or private reference changed", err)
	}
	afterTask, _ := s.queries().GetTask(ctx, task.ID)
	if afterTask != beforeTask {
		t.Fatal("task snapshot changed")
	}
	afterInputs, _ := s.TaskInputs(ctx, task.ID, 0, task.InputRevision)
	originalInputs, _ := json.Marshal(beforeInputs)
	currentInputs, _ := json.Marshal(afterInputs)
	if string(originalInputs) != string(currentInputs) {
		t.Fatal("input originals changed")
	}
	afterEvent, _ := s.GetGitHubEvent(ctx, event.ID)
	after, _ := json.Marshal(afterEvent)
	if string(before) != string(after) {
		t.Fatal("event or decision changed")
	}
	raw, _ := s.GitHubPayload(ctx, event.ID)
	if string(raw) != string(payload) {
		t.Fatal("payload bytes changed")
	}
	var afterReceipt string
	if err = s.db.QueryRow("SELECT result FROM task_requests WHERE request_id=?", input.RequestID).Scan(&afterReceipt); err != nil || afterReceipt != receipt {
		t.Fatal("request receipt changed", err)
	}
	var afterSourceReceipt string
	if err = s.db.QueryRow("SELECT result FROM github_requests WHERE json_extract(result,'$.source.id')=?", source.ID).Scan(&afterSourceReceipt); err != nil || afterSourceReceipt != sourceReceipt {
		t.Fatal("source receipt changed", err)
	}
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("migration left an invalid reference")
	}
	rows.Close()
	var enabled int
	if err = s.db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal("foreign keys were disabled", enabled, err)
	}
	if _, err = s.db.Exec("INSERT INTO github_event_decisions(event_id,outcome,actor_id,reason,created_at) VALUES('missing-event','dismissed',?,'invalid','now')", source.CreatedBy); err == nil {
		t.Fatal("foreign key enforcement lost after migration")
	}
}
