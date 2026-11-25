package daemon

import (
	"github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"strings"
	"testing"
)

func TestJournalEventBudgetCountsStoredBytes(t *testing.T) {
	j, err := openJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	command := testCommand()
	if _, err := j.accept(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	// SQLite length(TEXT) counts characters and stops at NUL. The journal's
	// byte limit must include the complete original UTF-8 event payload.
	text := strings.Repeat("界\x00", (32<<20)/4)
	if _, err := j.db.Exec("INSERT INTO events(operation_id,sequence,kind,text) VALUES(?,1,'assistant',?)", command.OperationID, text); err != nil {
		t.Fatal(err)
	}
	if err := j.appendEvent(t.Context(), command.OperationID, "assistant", "x"); err == nil {
		t.Fatal("journal accepted bytes beyond its durable event budget")
	}
	var count int
	if err := j.db.QueryRow("SELECT count(*) FROM events WHERE operation_id=?", command.OperationID).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected append changed the journal", count, err)
	}
}

func TestJournalEventUsageSurvivesUpgradeReplayAndFinalReceipt(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	j, err := openJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	command := testCommand()
	if _, err = j.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	if err = j.appendEvent(ctx, command.OperationID, "assistant", "界\x00"); err != nil {
		t.Fatal(err)
	}
	if err = j.acknowledge(ctx, command.OperationID, 1); err != nil {
		t.Fatal(err)
	}
	// Keep the original command and event bytes in their legacy schema.
	if _, err = j.db.Exec("DROP TRIGGER count_journal_event; DROP TRIGGER retire_event_usage; DROP TABLE event_usage; DROP INDEX pending_journal_events; DELETE FROM metadata WHERE key='event-usage-v1'"); err != nil {
		t.Fatal(err)
	}
	if err = j.db.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = openJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	if err = j.appendEvent(ctx, command.OperationID, "assistant", "é"); err != nil {
		t.Fatal(err)
	}
	if same, err := j.accept(ctx, command); err != nil || same {
		t.Fatal("command replay changed its identity", same, err)
	}
	events, err := j.events(ctx)
	if err != nil || len(events) != 1 || events[0].Sequence != 2 || events[0].Text != "é" {
		t.Fatal(events, err)
	}
	var count, bytes int
	if err = j.db.QueryRow("SELECT event_count,text_bytes FROM event_usage WHERE operation_id=?", command.OperationID).Scan(&count, &bytes); err != nil || count != 2 || bytes != 6 {
		t.Fatal("journal byte projection", count, bytes, err)
	}
	if err = j.receipt(ctx, command.OperationID, runnerprotocol.Receipt{Disposition: "committed", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if err = j.db.QueryRow("SELECT COUNT(*) FROM event_usage WHERE operation_id=?", command.OperationID).Scan(&count); err != nil || count != 0 {
		t.Fatal("terminal receipt retained event accounting", count, err)
	}
	if err = j.appendEvent(ctx, command.OperationID, "assistant", "late"); err == nil {
		t.Fatal("terminal command accepted an event")
	}
}
