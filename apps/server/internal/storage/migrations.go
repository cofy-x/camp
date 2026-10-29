package storage

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Schema SQL is shared by Goose and sqlc. There is no independently maintained
// schema snapshot or ORM auto-migration.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

const databaseApplicationID = 0x43414d52 // Camp Team development baseline

func (s *Store) migrate(ctx context.Context) error {
	fsys, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	return s.applyMigrations(ctx, fsys)
}

func (s *Store) applyMigrations(ctx context.Context, fsys fs.FS) error {
	var userVersion, applicationID int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return err
	}
	if userVersion != 0 {
		return fmt.Errorf("unsupported database version marker %d; choose a new data directory", userVersion)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return err
	}
	if applicationID != 0 && applicationID != databaseApplicationID {
		return fmt.Errorf("unsupported database identity; choose a new data directory")
	}
	var hasVersionTable, tableCount int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='goose_db_version'").Scan(&hasVersionTable); err != nil {
		return err
	}
	if hasVersionTable == 0 {
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tableCount); err != nil {
			return err
		}
		if tableCount != 0 {
			return fmt.Errorf("unsupported unversioned database schema; choose a new data directory")
		}
	} else if applicationID != databaseApplicationID {
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'").Scan(&tableCount); err != nil {
			return err
		}
		var initialized int
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&initialized); err != nil {
			return err
		}
		if tableCount != 0 || initialized != 0 {
			return fmt.Errorf("unsupported database baseline; choose a new data directory")
		}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, fsys,
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("configure database migrations: %w", err)
	}
	if hasVersionTable != 0 {
		if err := s.checkMigrationHistory(ctx, provider.ListSources()); err != nil {
			return err
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

// This application only applies migrations in order. Goose's Provider treats
// every recorded version as applied, so validate the ledger before allowing it
// to decide which migrations to skip. A failed migration leaves a valid prefix
// and can be retried without resetting or reseeding completed versions.
func (s *Store) checkMigrationHistory(ctx context.Context, sources []*goose.Source) error {
	versions := make([]int64, 1, len(sources)+1) // Goose's initial version is zero.
	for _, source := range sources {
		versions = append(versions, source.Version)
	}
	latest := versions[len(versions)-1]
	rows, err := s.db.QueryContext(ctx, "SELECT version_id, is_applied FROM goose_db_version ORDER BY version_id")
	if err != nil {
		return fmt.Errorf("read database migration history: %w", err)
	}
	defer rows.Close()
	index := 0
	var previous int64 = -1
	for rows.Next() {
		var version, applied int64
		if err := rows.Scan(&version, &applied); err != nil {
			return fmt.Errorf("read database migration history: %w", err)
		}
		if version > latest {
			return fmt.Errorf("database schema %d is newer than this application (maximum %d)", version, latest)
		}
		if version < 0 || version == previous || applied != 1 || index >= len(versions) || version != versions[index] {
			return fmt.Errorf("invalid database migration history at version %d: expected one applied row for each version in migration order", version)
		}
		previous = version
		index++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read database migration history: %w", err)
	}
	if index == 0 {
		return fmt.Errorf("invalid database migration history: missing zero version")
	}
	if index == 1 {
		var applicationID int
		if err := s.db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
			return err
		}
		if applicationID == databaseApplicationID {
			return fmt.Errorf("invalid database migration history: missing initial schema version")
		}
	}
	return nil
}
