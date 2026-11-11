package storage

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestOutputProjectionMigrationPreservesPendingCommandsReceiptsAndRestart(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 32)); err != nil {
		t.Fatal(err)
	}
	owner, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, owner, "legacy")
	room, agent := remoteTestRoom(t, s, owner, runner)
	task := seedLegacyTask(t, s, owner, room, agent)
	run, err := getLegacyRun(ctx, s, task.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "running"
	run.AttemptID = "original-attempt"
	run.WriterFence = 7
	run.BaselineCommit = run.InputCommit
	run.Output = strings.Repeat("Older output. 你好🙂\n", 11000)
	run.Output = AppendOutputTail("", run.Output)
	run.Model = "original-model"
	run.RuntimeKey = "original-runtime"
	run.AgentSnapshot.Instructions = "Frozen original instructions."
	if err = saveLegacyRun(ctx, s.queries(), run); err != nil {
		t.Fatal(err)
	}
	task.Status = "running"
	task.ActiveAttemptID = run.AttemptID
	task.DraftFence = 7
	task.ModelRequestsUsed = 1
	taskRaw, _ := json.Marshal(task)
	if _, err = s.db.Exec("UPDATE collaboration_tasks SET status=?,snapshot=? WHERE id=?", task.Status, string(taskRaw), task.ID); err != nil {
		t.Fatal(err)
	}
	originalCommand := `{ "id": "original-command", "kind": "run.start", "frozenRequest": "原始请求" }`
	originalEvent := "  Original event.\n你好🙂\n "
	hash := strings.Repeat("a", 64)
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	q := s.queries()
	if err = q.CreateAttempt(ctx, sqlgen.CreateAttemptParams{ID: run.AttemptID, RunID: run.ID, RunnerID: runner.ID, Fence: 7, CommandID: "original-command", LeaseExpiresAt: future, Deadline: future, CapabilityHash: hash, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE run_attempts SET status='executing',model_in_flight=1 WHERE id=?", run.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateRunnerCommand(ctx, sqlgen.CreateRunnerCommandParams{ID: "original-command", RunnerID: runner.ID, OperationID: run.AttemptID, Kind: "run.start", Payload: originalCommand, PayloadHash: hash, CreatedAt: run.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateRunnerEventReceipt(ctx, sqlgen.CreateRunnerEventReceiptParams{OperationID: run.AttemptID, Sequence: 1, PayloadHash: hash, Kind: "assistant", Text: originalEvent, CreatedAt: run.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateRunEvent(ctx, sqlgen.CreateRunEventParams{RunID: run.ID, Seq: 1, Type: "assistant", Text: originalEvent, CreatedAt: run.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRun(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(got, run) {
		t.Fatalf("migration changed complete Run: %v", err)
	}
	var afterTask string
	if err = s.db.QueryRow("SELECT snapshot FROM collaboration_tasks WHERE id=?", task.ID).Scan(&afterTask); err != nil || afterTask != string(taskRaw) {
		t.Fatal("migration rewrote task grant or budget", err)
	}
	attempt, err := s.GetAttempt(ctx, run.AttemptID)
	if err != nil || !attempt.ModelInFlight || string(attempt.Command) != originalCommand {
		t.Fatal("pending command/uncertainty changed", err)
	}
	if replayed, err := s.RecordAttemptEvent(ctx, runner.ID, run.AttemptID, 1, "assistant", originalEvent, hash); err != nil || !replayed {
		t.Fatal("original receipt did not replay", err)
	}
	if _, err = s.RecordAttemptEvent(ctx, runner.ID, run.AttemptID, 1, "assistant", "Different original", hash); err == nil {
		t.Fatal("receipt accepted changed bytes")
	}
	addition := "New output after upgrade. 🙂\n"
	if replayed, err := s.RecordAttemptEvent(ctx, runner.ID, run.AttemptID, 2, "assistant", addition, strings.Repeat("b", 64)); err != nil || replayed {
		t.Fatal("could not continue original stream", err)
	}
	run.Output = AppendOutputTail(run.Output, addition)
	got, err = s.GetRun(ctx, run.ID)
	if err != nil || got.Output != run.Output || got.Model != run.Model || got.RuntimeKey != run.RuntimeKey || got.AgentSnapshot.Instructions != run.AgentSnapshot.Instructions {
		t.Fatal("upgrade lost frozen input or output", err)
	}
	run = got
	var seq int
	var name, path string
	if err = s.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	got, err = reopened.GetRun(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(got, run) {
		t.Fatal("restart changed stream or frozen input", err)
	}
	receipt, err := reopened.queries().GetRunnerEventReceipt(ctx, sqlgen.GetRunnerEventReceiptParams{OperationID: run.AttemptID, Sequence: 1})
	if err != nil || receipt.Text != originalEvent || receipt.PayloadHash != hash {
		t.Fatal("original receipt bytes changed", err)
	}
	events, err := reopened.ListRunEvents(ctx, run.ID, 0)
	if err != nil || len(events) != 2 || events[0].Text != originalEvent || events[1].Text != addition {
		t.Fatal("original event log changed", err)
	}
	// The reverse migration is also lossless; the application itself requires a
	// fully migrated schema and has no runtime fallback to old storage layouts.
	source := string(migrationSources(t)["00033_run_output_projection.sql"].Data)
	down := strings.SplitN(source, "-- +goose Down", 2)[1]
	if err = reopened.transaction(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, down); return err }); err != nil {
		t.Fatal(err)
	}
	restored, err := getLegacyRun(ctx, reopened, run.ID)
	if err != nil || !reflect.DeepEqual(restored, run) {
		t.Fatal("reverse migration changed complete original", err)
	}
}
