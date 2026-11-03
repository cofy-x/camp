package daemon

import (
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
