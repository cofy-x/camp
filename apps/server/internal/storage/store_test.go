package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var ctx = context.Background()

// Build an empty, fully migrated database once per test process. Tests that
// exercise initialization and migration history still open their own files.
var testStoreSeed = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "camp-storage-test-seed-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	filename := filepath.Join(dir, "desktop.sqlite")
	s, err := Open(filename)
	if err != nil {
		return nil, err
	}
	if err := s.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(filename)
})

func testStore(t *testing.T) *Store {
	t.Helper()
	seed, err := testStoreSeed()
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	if err := os.WriteFile(filename, seed, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestCorruptAndFutureDatabaseAreNotReset(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "broken.sqlite")
	original := []byte("This is not a database.")
	if err := os.WriteFile(filename, original, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(filename); err == nil {
		s.Close()
		t.Fatal("opened corrupt database")
	}
	after, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatalf("corrupt database was changed: %q %v", after, err)
	}
	s := testStore(t)
	if _, err = s.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(ctx); err == nil {
		t.Fatal("accepted future schema")
	}
}

func ptr[T any](value T) *T { return &value }
