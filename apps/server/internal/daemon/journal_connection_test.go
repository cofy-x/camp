package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalReplacementConnectionPreservesConstraints(t *testing.T) {
	j, err := openJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	j.db.SetMaxIdleConns(0)
	for range 2 {
		if err := j.appendEvent(context.Background(), "missing-operation", "assistant", "orphan"); err == nil {
			t.Fatal("replacement connection accepted an event without a command")
		}
		for pragma, want := range map[string]int{"busy_timeout": 5000, "foreign_keys": 1, "synchronous": 2} {
			var got int
			if err := j.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
				t.Fatalf("replacement %s = %d, want %d: %v", pragma, got, want, err)
			}
		}
	}
}

func TestJournalDoesNotAbandonLegacyDisplacedReceipts(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "runner?legacy")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(directory, "journal.sqlite")
	if err := os.WriteFile(name, nil, 0600); err != nil {
		t.Fatal(err)
	}
	legacyName, _, _ := strings.Cut(name, "?")
	legacy, err := sql.Open("sqlite", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(journalSchema); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	command := testCommand()
	if _, err := (&journal{db: legacy}).accept(t.Context(), command); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(legacyName)
	if err != nil {
		t.Fatal(err)
	}
	if j, err := openJournal(directory); err == nil {
		j.db.Close()
		t.Fatal("started a new journal while the old receipts were displaced")
	} else if !strings.Contains(err.Error(), "legacy Runner journal") {
		t.Fatal(err)
	}
	after, err := os.ReadFile(legacyName)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy journal was changed", err)
	}
	if info, err := os.Stat(name); err != nil || info.Size() != 0 {
		t.Fatal("literal journal was initialized before legacy recovery", err)
	}
	if err := os.WriteFile(name, []byte("An incomplete attempted recovery"), 0600); err != nil {
		t.Fatal(err)
	}
	if j, err := openJournal(directory); err == nil {
		j.db.Close()
		t.Fatal("a nonempty replacement hid the displaced journal")
	} else if !strings.Contains(err.Error(), "legacy Runner journal") {
		t.Fatal(err)
	}
	if err := os.Rename(legacyName, name); err != nil {
		t.Fatal(err)
	}
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	if created, err := j.accept(t.Context(), command); err != nil || created {
		t.Fatalf("recovering the old journal lost command deduplication: %v %v", created, err)
	}
}

func TestJournalFilenamePreservesLiteralURICharacters(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "runner #? state")
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	command := testCommand()
	if created, err := j.accept(context.Background(), command); err != nil || !created {
		j.db.Close()
		t.Fatalf("accept: %v %v", created, err)
	}
	if err := j.db.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(directory, "journal.sqlite"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("journal was not stored at its literal path: %v", err)
	}
	j, err = openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	if created, err := j.accept(context.Background(), command); err != nil || created {
		t.Fatalf("reopened journal lost command deduplication: %v %v", created, err)
	}
}
