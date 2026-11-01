package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func legacyTaskInputs(t *testing.T) (*Store, context.Context, CollaborationTask, string) {
	t.Helper()
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 13)); err != nil {
		t.Fatal(err)
	}
	actor, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, actor, "legacy")
	room, writer := remoteTestRoom(t, s, actor, runner)
	task := startTask(t, s, actor, room, writer)
	user, _ := UserFromContext(actor)
	for _, text := range []string{"Original input <one>.", "Original input two."} {
		input := TaskInput{ID: newID(), AuthorUserID: user.ID, AuthorName: user.Name, Content: text, CreatedAt: now()}
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			q := sqlgen.New(tx)
			seq, err := q.NextMessageSequence(ctx, room.ID)
			if err != nil {
				return err
			}
			return insertMessage(ctx, q, Message{ID: input.ID, TaskID: task.ID, RoomID: room.ID, ThreadRootID: task.ThreadRootID, Seq: seq, AuthorKind: "user", AuthorUserID: user.ID, AuthorName: user.Name, Content: text, CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt}, "")
		})
		if err != nil {
			t.Fatal(err)
		}
		task.Inputs = append(task.Inputs, input)
	}
	// Failed historical work may have advanced the old processed cursor.
	task.ProcessedInputRevision = 2
	raw, _ := json.Marshal(task)
	var old map[string]json.RawMessage
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"inputRevision", "inputBytes", "coveredInputRevision"} {
		delete(old, key)
	}
	raw, _ = json.Marshal(old)
	if _, err := s.db.Exec("UPDATE collaboration_tasks SET snapshot=? WHERE id=?", string(raw), task.ID); err != nil {
		t.Fatal(err)
	}
	in := TaskOperation{RequestID: "legacy-receipt", Content: "Original input two."}
	body, _ := json.Marshal(in)
	if err := s.queries().SaveTaskRequest(ctx, sqlgen.SaveTaskRequestParams{UserID: user.ID, Scope: "inputs:" + task.ID, RequestID: in.RequestID, RequestHash: remoteHash(body), Result: string(raw)}); err != nil {
		t.Fatal(err)
	}
	return s, actor, task, string(raw)
}

func TestTaskInputMigrationPreservesOriginalsAndHistoricalReceipt(t *testing.T) {
	for _, succeeded := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "successful"}[succeeded], func(t *testing.T) {
			s, actor, task, receipt := legacyTaskInputs(t)
			run, err := s.GetRun(ctx, task.ActiveRunID)
			if err != nil {
				t.Fatal(err)
			}
			run.TaskInputRevision = 1
			run.Status = "failed"
			if succeeded {
				run.Status = "succeeded"
			}
			run.Intent = &CoordinationIntent{Kind: "finish", CallID: "historical", Summary: "Accepted historical checkpoint."}
			if err := saveRun(ctx, s.queries(), run); err != nil {
				t.Fatal(err)
			}
			if err := s.migrate(ctx); err != nil {
				t.Fatal(err)
			}
			migrated, err := s.GetTask(ctx, task.ID)
			wantCoverage := int64(0)
			if succeeded {
				wantCoverage = 1
			}
			if err != nil || migrated.InputRevision != 2 || migrated.CoveredInputRevision != wantCoverage || migrated.ProcessedInputRevision != 2 || len(migrated.Inputs) != 2 {
				t.Fatal(migrated, err)
			}
			for i, original := range task.Inputs {
				original.Seq = int64(i + 1)
				if migrated.Inputs[i] != original {
					t.Fatal("changed immutable original", original, migrated.Inputs[i])
				}
			}
			var after string
			if err := s.db.QueryRow("SELECT result FROM task_requests WHERE request_id='legacy-receipt'").Scan(&after); err != nil || after != receipt {
				t.Fatal("rewrote a historical receipt", err)
			}
			replayed, err := s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: "legacy-receipt", Content: "Original input two."})
			if err != nil || replayed.InputRevision != 2 || replayed.Inputs[0].ID != task.Inputs[0].ID {
				t.Fatal(replayed, err)
			}
		})
	}
}

func TestTaskInputMigrationRejectsCorruptionWithoutPartialSchema(t *testing.T) {
	for _, damage := range []string{
		"UPDATE collaboration_tasks SET snapshot=json_set(snapshot,'$.inputs[1].id',json_extract(snapshot,'$.inputs[0].id'))",
		"UPDATE collaboration_tasks SET snapshot=json_set(snapshot,'$.inputs[0].content','different original')",
		"UPDATE collaboration_tasks SET snapshot=json_remove(snapshot,'$.inputs[0].authorName')",
		"UPDATE collaboration_tasks SET snapshot=json_set(snapshot,'$.inputs','not an array')",
	} {
		t.Run(strings.Split(damage, "snapshot=")[1], func(t *testing.T) {
			s, _, task, _ := legacyTaskInputs(t)
			if _, err := s.db.Exec(damage); err != nil {
				t.Fatal(err)
			}
			var before string
			if err := s.db.QueryRow("SELECT snapshot FROM collaboration_tasks WHERE id=?", task.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := s.migrate(ctx); err == nil {
				t.Fatal("accepted damaged task originals")
			}
			assertScalar(t, s, "SELECT max(version_id) FROM goose_db_version", 13)
			assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name IN ('task_inputs','task_input_migration_guard')", 0)
			var after string
			if err := s.db.QueryRow("SELECT snapshot FROM collaboration_tasks WHERE id=?", task.ID).Scan(&after); err != nil || before != after {
				t.Fatal("changed damaged source data", err)
			}
		})
	}
}

func TestEmptyLegacyTaskInputRepresentationsMigrate(t *testing.T) {
	for _, change := range []string{"json_set(snapshot,'$.inputs',NULL)", "json_remove(snapshot,'$.inputs')"} {
		t.Run(change, func(t *testing.T) {
			s := unmigratedStore(t)
			if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 13)); err != nil {
				t.Fatal(err)
			}
			actor, _ := teamUsers(t, s)
			runner, _ := enrollTestRunner(t, s, actor, "empty")
			room, writer := remoteTestRoom(t, s, actor, runner)
			task := startTask(t, s, actor, room, writer)
			if _, err := s.db.Exec("UPDATE collaboration_tasks SET snapshot="+change+" WHERE id=?", task.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.migrate(ctx); err != nil {
				t.Fatal(err)
			}
			migrated, err := s.GetTask(ctx, task.ID)
			if err != nil || migrated.InputRevision != 0 || migrated.InputBytes != 0 || len(migrated.Inputs) != 0 {
				t.Fatal(migrated, err)
			}
		})
	}
}

func TestTaskInputMigrationRejectsInvalidSuccessfulRoundFrontiers(t *testing.T) {
	for _, revision := range []any{1.5, "not-a-sequence", -1, 3, nil} {
		t.Run(fmt.Sprint(revision), func(t *testing.T) {
			s, _, task, _ := legacyTaskInputs(t)
			if _, err := s.db.Exec("UPDATE runs SET status='succeeded',snapshot=json_set(snapshot,'$.taskInputRevision',?,'$.intent',json_object('kind','finish')) WHERE id=?", revision, task.ActiveRunID); err != nil {
				t.Fatal(err)
			}
			if err := s.migrate(ctx); err == nil {
				t.Fatal("accepted an invalid historical input frontier", revision)
			}
			assertScalar(t, s, "SELECT max(version_id) FROM goose_db_version", 13)
			assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='task_inputs'", 0)
		})
	}
}
