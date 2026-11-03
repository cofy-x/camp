package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/githubhooks"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// Seed the schema-19 representation directly. Current ingress and admission
// require newer tables and must never silently operate against an old schema.
func legacyGitHubTask(t *testing.T, s *Store, actor context.Context, room Room, writer Agent, source GitHubSource, payload []byte) (GitHubEvent, CreateTaskInput, CollaborationTask) {
	t.Helper()
	observation, err := githubhooks.Parse(source.Repository, "issues", payload)
	if err != nil {
		t.Fatal(err)
	}
	user, _ := UserFromContext(actor)
	eventID := newID()
	task := seedLegacyTask(t, s, actor, room, writer)
	in := CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), SourceEventID: eventID, Goal: task.Goal, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
	if err = s.transaction(actor, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		raw, _ := json.Marshal(observation)
		fingerprint := githubhooks.PayloadFingerprint(payload)
		if err := q.InsertGitHubEvent(actor, sqlgen.InsertGitHubEventParams{ID: eventID, SourceID: source.ID, RoomID: room.ID, SourceRevision: source.Revision, Fingerprint: fingerprint, Disposition: "pending", Value: string(raw), Payload: payload, ReceivedAt: now()}); err != nil {
			return err
		}
		if err := q.InsertGitHubDelivery(actor, sqlgen.InsertGitHubDeliveryParams{SourceID: source.ID, DeliveryID: newID(), Fingerprint: fingerprint, EventID: eventID}); err != nil {
			return err
		}
		if err := q.PinGitHubRepository(actor, sqlgen.PinGitHubRepositoryParams{RepositoryID: observation.RepositoryID, ID: source.ID}); err != nil {
			return err
		}
		input := TaskInput{ID: newID(), Seq: 1, AuthorUserID: user.ID, AuthorName: user.Name, Content: observation.Context(), CreatedAt: now()}
		seq, err := q.NextMessageSequence(actor, room.ID)
		if err != nil {
			return err
		}
		if err = insertMessage(actor, q, Message{ID: input.ID, TaskID: task.ID, RoomID: room.ID, ThreadRootID: task.ThreadRootID, Seq: seq, AuthorKind: "user", AuthorUserID: user.ID, AuthorName: user.Name, Content: input.Content, CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt}, ""); err != nil {
			return err
		}
		value, _ := json.Marshal(input)
		if _, err = tx.ExecContext(actor, `INSERT INTO task_inputs(id,task_id,seq,content_bytes,value) VALUES(?,?,?,?,?)`, input.ID, task.ID, input.Seq, len(input.Content), string(value)); err != nil {
			return err
		}
		task.SourceEventID, task.InputProtocol, task.InputRevision, task.InputBytes = eventID, 1, 1, int64(len(input.Content))
		if err = saveTask(actor, q, &task); err != nil {
			return err
		}
		if err = q.DecideGitHubEvent(actor, sqlgen.DecideGitHubEventParams{EventID: eventID, Outcome: "accepted", ActorID: user.ID, TaskID: nullableParent(task.ID), Reason: "explicit_task_start", CreatedAt: now()}); err != nil {
			return err
		}
		body, _ := json.Marshal(in)
		return saveTaskReceipt(actor, q, "create:"+room.ID, in.RequestID, remoteHash(body), task)
	}); err != nil {
		t.Fatal(err)
	}
	event, err := s.GetGitHubEvent(actor, eventID)
	if err != nil {
		t.Fatal(err)
	}
	return event, in, task
}

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
	event, input, task := legacyGitHubTask(t, s, actor, room, writer, source, payload)
	beforeSource, _ := s.GitHubSourceCredential(ctx, source.ID)
	beforeTask, _ := s.queries().GetTask(ctx, task.ID)
	beforeInputs, _ := s.TaskInputs(ctx, task.ID, 0, task.InputRevision)
	var sourceReceipt string
	if err := s.db.QueryRow("SELECT result FROM github_requests WHERE json_extract(result,'$.source.id')=?", source.ID).Scan(&sourceReceipt); err != nil {
		t.Fatal(err)
	}
	beforeEvent, _ := s.GetGitHubEvent(ctx, event.ID)
	before, _ := json.Marshal(beforeEvent)
	var receipt string
	var err error
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
