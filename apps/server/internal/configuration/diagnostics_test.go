package configuration

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDiagnosticsRetainLastFailureAcrossSuccessAndRestart(t *testing.T) {
	directory := t.TempDir()
	diagnostics, err := OpenDiagnostics(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := diagnostics.Record("pdf", "missing", "Reader is unavailable."); err != nil {
		t.Fatal(err)
	}
	failed := diagnostics.Get("pdf")
	if err := diagnostics.Record("pdf", "ready", "Reader extracted the expected fixture."); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDiagnostics(directory)
	if err != nil {
		t.Fatal(err)
	}
	record := reopened.Get("pdf")
	if record.Status != "ready" || record.LastSuccessAt == "" || record.LastFailureAt != failed.CheckedAt || record.LastFailure != failed.Detail {
		t.Fatalf("diagnostic history lost: %+v", record)
	}
	info, err := os.Stat(filepath.Join(directory, "readiness.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("diagnostic journal must be private")
	}
}

func TestModelRotationsKeepDiagnosticJournalBoundedAndReadable(t *testing.T) {
	directory := t.TempDir()
	diagnostics, err := OpenDiagnostics(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := diagnostics.Record("document-pdf", "ready", "Reader verified."); err != nil {
		t.Fatal(err)
	}
	for i := range maxConnectionDiagnostics + 8 {
		id := "connection:" + strconv.Itoa(i)
		for _, status := range []string{"error", "ready"} {
			if err := diagnostics.Record(id, status, strings.Repeat("x", 2048)); err != nil {
				t.Fatal(err)
			}
		}
	}
	reopened, err := OpenDiagnostics(directory)
	if err != nil {
		t.Fatal("rotations made the next service startup unreadable", err)
	}
	if len(reopened.records) != maxConnectionDiagnostics+1 || reopened.Get("document-pdf").Status != "ready" || reopened.Get("connection:0").Status != "" || reopened.Get("connection:71").Status != "ready" {
		t.Fatal("rotation cache lost current or stable component diagnostics")
	}
}

func TestDiagnosticsFailClosedAndDoNotInventSavedHistory(t *testing.T) {
	directory := t.TempDir()
	diagnostics, err := OpenDiagnostics(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "readiness.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := diagnostics.Record("execution", "ready", "Completed."); err == nil || diagnostics.Get("execution").Status != "" {
		t.Fatal("failed journal write changed in-memory history")
	}
	if _, err := OpenDiagnostics(directory); err == nil {
		t.Fatal("invalid journal was silently reset")
	}
}
