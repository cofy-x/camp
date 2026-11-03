package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTransactionConnectionWaitHonorsCancellation(t *testing.T) {
	s := testStore(t)
	for _, readOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "read"}[readOnly], func(t *testing.T) {
			conn, err := s.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			waiting, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			called := false
			transaction := s.transaction
			if readOnly {
				transaction = s.readTransaction
			}
			err = transaction(waiting, func(*sql.Tx) error { called = true; return nil })
			if !errors.Is(err, context.DeadlineExceeded) || called {
				t.Fatalf("connection wait invoked callback or ignored cancellation: called=%v err=%v", called, err)
			}
		})
	}
	if err := s.transaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "SELECT 1")
		return err
	}); err != nil {
		t.Fatal("a cancelled waiter retained the connection", err)
	}
}

func TestReadTransactionKeepsSnapshotWithoutReservingWriter(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec("CREATE TABLE transaction_probe (value INTEGER); INSERT INTO transaction_probe VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", databaseDSN(filename))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetMaxOpenConns(1)
	if err := s.readTransaction(t.Context(), func(tx *sql.Tx) error {
		var before, after int
		if err := tx.QueryRowContext(t.Context(), "SELECT value FROM transaction_probe").Scan(&before); err != nil {
			return err
		}
		if _, err := writer.ExecContext(t.Context(), "UPDATE transaction_probe SET value=2"); err != nil {
			return err
		}
		if err := tx.QueryRowContext(t.Context(), "SELECT value FROM transaction_probe").Scan(&after); err != nil {
			return err
		}
		if before != 1 || after != 1 {
			t.Fatalf("read transaction mixed committed versions: before=%d after=%d", before, after)
		}
		return nil
	}); err != nil {
		t.Fatal("a read snapshot blocked the independent writer", err)
	}
	assertScalar(t, s, "SELECT value FROM transaction_probe", 2)
}

func TestWriteLockWaitHonorsCancellationWithoutReplayingWork(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	writer, err := sql.Open("sqlite", databaseDSN(filename))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetMaxOpenConns(1)
	lock, err := writer.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	waiting, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	calls := 0
	work := func(*sql.Tx) error { calls++; return nil }
	if err := s.transaction(waiting, work); !errors.Is(err, context.DeadlineExceeded) || calls != 0 {
		t.Fatalf("locked transaction ran work or ignored cancellation: calls=%d err=%v", calls, err)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := s.transaction(t.Context(), work); err != nil || calls != 1 {
		t.Fatalf("transaction did not recover after lock release: calls=%d err=%v", calls, err)
	}
}
