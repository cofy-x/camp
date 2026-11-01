package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestCleanupJournalReadOnlyEvidencePreservesDataAndDetectsPendingWork(t *testing.T) {
	root := t.TempDir()
	if _, err := InspectCleanupJournal(t.Context(), root); err == nil {
		t.Fatal("Missing journal accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "journal.sqlite")); !os.IsNotExist(err) {
		t.Fatal("Inspection created a journal")
	}
	j, err := openJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	initial, err := InspectCleanupJournal(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	command := p.Command{ID: "controlled-command", OperationID: "controlled-operation", Kind: "source.capture"}
	if _, err = j.accept(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if _, err = j.db.Exec("INSERT INTO coordination_intents(operation_id,body) VALUES(?,?)", command.OperationID, []byte(`{"private":"must-not-be-exposed"}`)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "journal.sqlite-wal"))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := InspectCleanupJournal(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Pending != 1 || evidence.Intents != 1 || evidence.Fingerprint == initial.Fingerprint {
		t.Fatal(evidence)
	}
	after, err := os.ReadFile(filepath.Join(root, "journal.sqlite-wal"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("Read-only observation changed the journal WAL", err)
	}
	encoded, _ := json.Marshal(evidence)
	if bytes.Contains(encoded, []byte("must-not-be-exposed")) {
		t.Fatal("Journal body leaked")
	}
	if err = j.receipt(t.Context(), command.OperationID, p.Receipt{Disposition: "committed", Status: "interrupted"}); err != nil {
		t.Fatal(err)
	}
	settled, err := InspectCleanupJournal(t.Context(), root)
	if err != nil || settled.Pending != 0 || settled.Intents != 0 || settled.Fingerprint == evidence.Fingerprint {
		t.Fatal(settled, err)
	}
}
