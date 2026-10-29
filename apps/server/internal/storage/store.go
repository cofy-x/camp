// Package storage owns Camp's durable business records and SQLite transactions.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(filename string) (*Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return OpenContext(ctx, filename)
}

// OpenContext configures and migrates the database before making it available.
// The lock file remains in place so openers always lock the same inode.
func OpenContext(ctx context.Context, filename string) (result *Store, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if filename == "" {
		return nil, errors.New("database filename must not be empty")
	}
	if filename != ":memory:" {
		filename, err = filepath.Abs(filename)
		if err != nil {
			return nil, fmt.Errorf("resolve database filename: %w", err)
		}
		if resolved, resolveErr := filepath.EvalSymlinks(filename); resolveErr == nil {
			filename = resolved
		} else if dir, resolveErr := filepath.EvalSymlinks(filepath.Dir(filename)); resolveErr == nil {
			filename = filepath.Join(dir, filepath.Base(filename))
		}
		lock := flock.New(filename + ".migrate.lock")
		defer func() {
			if unlockErr := lock.Close(); unlockErr != nil {
				if result != nil {
					unlockErr = errors.Join(unlockErr, result.Close())
					result = nil
				}
				err = errors.Join(err, unlockErr)
			}
		}()
		locked, lockErr := lock.TryLockContext(ctx, 25*time.Millisecond)
		if lockErr != nil {
			return nil, fmt.Errorf("lock database initialization: %w", lockErr)
		}
		if !locked {
			return nil, errors.New("database initialization lock was not acquired")
		}
	}
	db, err := sql.Open("sqlite", databaseDSN(filename))
	if err != nil {
		return nil, err
	}
	// Serializing this local application's operations is intentional. BEGIN
	// IMMEDIATE also serializes write transactions across application instances.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if err = s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func databaseDSN(filename string) string {
	// A short SQLite busy wait keeps context cancellation responsive. Write
	// transactions retry lock acquisition within the context and a 5 s ceiling.
	params := url.Values{"_pragma": {"busy_timeout(100)", "foreign_keys(1)", "journal_mode(WAL)"}, "_txlock": {"immediate"}}
	if filename == ":memory:" {
		return filename + "?" + params.Encode()
	}
	name := filepath.ToSlash(filename)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	return (&url.URL{Scheme: "file", Path: name, RawQuery: params.Encode()}).String()
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.withTransaction(ctx, nil, fn)
}

func (s *Store) readTransaction(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.withTransaction(ctx, &sql.TxOptions{ReadOnly: true}, fn)
}

func (s *Store) withTransaction(ctx context.Context, opts *sql.TxOptions, fn func(*sql.Tx) error) error {
	deadline := time.Now().Add(5 * time.Second)
	var tx *sql.Tx
	var err error
	for {
		tx, err = s.db.BeginTx(ctx, opts)
		if err == nil || !sqliteBusy(err) || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func sqliteBusy(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code()&0xff == 5
}

func (s *Store) queries() *sqlgen.Queries { return sqlgen.New(s.db) }
