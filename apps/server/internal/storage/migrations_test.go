package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofrs/flock"
)

func unmigratedStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", databaseDSN(filepath.Join(t.TempDir(), "migration.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return &Store{db: db}
}

func assertScalar(t *testing.T, s *Store, query string, want int) {
	t.Helper()
	var got int
	if err := s.db.QueryRow(query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: got %d, want %d: %v", query, got, want, err)
	}
}

func migrationSources(t *testing.T) fstest.MapFS {
	t.Helper()
	fsys, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	sources := fstest.MapFS{}
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file.Name())
		if err != nil {
			t.Fatal(err)
		}
		sources[file.Name()] = &fstest.MapFile{Data: data}
	}
	return sources
}

func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	latest := 0
	for name := range migrationSources(t) {
		number, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		latest = max(latest, number)
	}
	return latest
}

func TestFailedSQLMigrationRollsBackAndCanRecover(t *testing.T) {
	s := unmigratedStore(t)
	broken := fstest.MapFS{
		"00001_initial.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE partial_schema(id INTEGER);\nINSERT INTO missing_table VALUES(1);\n-- +goose Down\nDROP TABLE partial_schema;\n")},
	}
	if err := s.applyMigrations(ctx, broken); err == nil {
		t.Fatal("accepted a failing SQL migration")
	}
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='partial_schema'", 0)
	assertScalar(t, s, "SELECT max(version_id) FROM goose_db_version", 0)
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='resources'", 1)
}

func TestFailedInitializationRollsBackBothSeedsAndVersion(t *testing.T) {
	s := unmigratedStore(t)
	sources := migrationSources(t)
	initialData := sources["00001_initial.sql"]
	initialData.Data = []byte(strings.Replace(string(initialData.Data), "-- +goose Down", "INSERT INTO missing_seed_table VALUES(1);\n-- +goose Down", 1))
	err := s.applyMigrations(ctx, sources)
	if err == nil || !strings.Contains(err.Error(), "missing_seed_table") {
		t.Fatalf("want initialization failure, got %v", err)
	}
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name IN ('resources', 'rooms')", 0)
	assertScalar(t, s, "PRAGMA application_id", 0)
	assertScalar(t, s, "SELECT max(version_id) FROM goose_db_version", 0)
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='resources'", 1)
	assertScalar(t, s, "SELECT max(version_id) FROM goose_db_version", latestMigrationVersion(t))
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='resources'", 1)
}

func TestInvalidMigrationHistoryIsRejectedWithoutChangingData(t *testing.T) {
	for _, test := range []struct {
		name, damage string
	}{
		{"missing_zero", "DELETE FROM goose_db_version WHERE version_id=0"},
		{"empty", "DELETE FROM goose_db_version"},
		{"missing_predecessor", "DELETE FROM goose_db_version WHERE version_id=1"},
		{"unapplied", "UPDATE goose_db_version SET is_applied=0 WHERE version_id=1"},
		{"invalid_applied_flag", "UPDATE goose_db_version SET is_applied=2 WHERE version_id=1"},
		{"duplicate", "INSERT INTO goose_db_version(version_id,is_applied) VALUES(1,1)"},
		{"negative", "INSERT INTO goose_db_version(version_id,is_applied) VALUES(-1,1)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "invalid-history.sqlite")
			s, err := Open(filename)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(test.damage); err != nil {
				t.Fatal(err)
			}
			var before string
			if err := s.db.QueryRow("SELECT coalesce(group_concat(version_id || ':' || is_applied, ','), '') FROM (SELECT * FROM goose_db_version ORDER BY id)").Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			opened, err := Open(filename)
			if err == nil {
				opened.Close()
				t.Fatal("accepted invalid migration history")
			}
			if !strings.Contains(err.Error(), "invalid database migration history") {
				t.Fatalf("unexpected startup error: %v", err)
			}
			db, err := sql.Open("sqlite", databaseDSN(filename))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			after := ""
			if err := db.QueryRow("SELECT coalesce(group_concat(version_id || ':' || is_applied, ','), '') FROM (SELECT * FROM goose_db_version ORDER BY id)").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("invalid migration history was changed: before=%q after=%q", before, after)
			}
			assertScalar(t, &Store{db: db}, "SELECT count(*) FROM sqlite_master WHERE name='resources'", 1)
		})
	}
}

func TestMigrationHistoryAllowsSourceVersionGapsAndRejectsUnknownVersions(t *testing.T) {
	s := unmigratedStore(t)
	sources := migrationSources(t)
	sources[fmt.Sprintf("%05d_version_gap.sql", latestMigrationVersion(t)+2)] = &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE extra_data(id INTEGER);\n-- +goose Down\nDROP TABLE extra_data;\n")}
	if err := s.applyMigrations(ctx, sources); err != nil {
		t.Fatal(err)
	}
	if err := s.applyMigrations(ctx, sources); err != nil {
		t.Fatalf("rejected a valid source version gap: %v", err)
	}
	if _, err := s.db.Exec("INSERT INTO goose_db_version(version_id,is_applied) VALUES(?,1)", latestMigrationVersion(t)+1); err != nil {
		t.Fatal(err)
	}
	if err := s.applyMigrations(ctx, sources); err == nil || !strings.Contains(err.Error(), "invalid database migration history") {
		t.Fatalf("accepted an unknown applied version: %v", err)
	}
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='resources'", 1)
}

func TestUnknownDatabaseIdentityAndFutureSchemasAreRejected(t *testing.T) {
	t.Run("unrecognized_baseline", func(t *testing.T) {
		filename := filepath.Join(t.TempDir(), "unrecognized.sqlite")
		db, err := sql.Open("sqlite", filename)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("CREATE TABLE other_data(value TEXT); INSERT INTO other_data VALUES('keep'); CREATE TABLE goose_db_version(version_id INTEGER, is_applied INTEGER); INSERT INTO goose_db_version VALUES(0,1),(1,1);"); err != nil {
			t.Fatal(err)
		}
		if s, err := Open(filename); err == nil {
			s.Close()
			t.Fatal("accepted a version ledger from an unrecognized baseline")
		} else if !strings.Contains(err.Error(), "unsupported database baseline") {
			t.Fatal(err)
		}
		var value string
		if err := db.QueryRow("SELECT value FROM other_data").Scan(&value); err != nil || value != "keep" {
			t.Fatalf("unrecognized database changed: %q %v", value, err)
		}
		assertScalar(t, &Store{db: db}, "PRAGMA application_id", 0)
	})
	t.Run("future", func(t *testing.T) {
		filename := filepath.Join(t.TempDir(), "future.sqlite")
		s, err := Open(filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("INSERT INTO goose_db_version(version_id,is_applied) VALUES(99,1)"); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if s, err := Open(filename); err == nil {
			s.Close()
			t.Fatal("accepted future schema")
		} else if !strings.Contains(err.Error(), "newer than this application") {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filename)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		assertScalar(t, &Store{db: db}, "SELECT count(*) FROM sqlite_master WHERE name='resources'", 1)
		assertScalar(t, &Store{db: db}, "SELECT max(version_id) FROM goose_db_version", 99)
	})
}

func TestConcurrentProcessInitialization(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "shared.sqlite")
	commands := make([]*exec.Cmd, 2)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestMigrationProcessHelper$")
		commands[i].Env = append(os.Environ(), "PLATINUM_MIGRATION_TEST_FILE="+filename)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("concurrent opener failed: %v", err)
		}
	}
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='resources'", 1)
	assertScalar(t, s, "SELECT count(*) FROM goose_db_version", len(migrationSources(t))+1)
}

func TestMigrationProcessHelper(t *testing.T) {
	filename := os.Getenv("PLATINUM_MIGRATION_TEST_FILE")
	if filename == "" {
		return
	}
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestInitializationLockHonorsContext(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(dir, "locked.sqlite")
	lock := flock.New(filename + ".migrate.lock")
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := OpenContext(ctx, filename); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("initialization ignored cancellation")
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("waiting for the lock created a database: %v", err)
	}
}

func TestConnectionReplacementPreservesPragmas(t *testing.T) {
	s := testStore(t)
	s.db.SetMaxIdleConns(0)
	for range 2 {
		assertScalar(t, s, "PRAGMA foreign_keys", 1)
		assertScalar(t, s, "PRAGMA busy_timeout", 100)
		var journal string
		if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
			t.Fatalf("replacement journal mode %q: %v", journal, err)
		}
	}
}

func TestDatabaseFilenameIsNotInterpretedAsURIOptions(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop #? name.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := teamUsers(t, s)
	if err := s.SavePreferences(owner, Preferences{Wallpaper: "solid", IconSnap: true}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(filename); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.Bootstrap(owner, "")
	if err != nil || saved.Preferences.Wallpaper != "solid" {
		t.Fatal("preferences did not persist", err)
	}
}
