-- +goose Up
-- Retain every source identity, credential reference and child record while
-- replacing the lifetime address constraint with an active-route constraint.
-- Goose wraps the rebuild in one transaction. Foreign keys stay enabled and
-- are deferred until the original parent IDs have been restored.
PRAGMA defer_foreign_keys = ON;
CREATE TABLE github_sources_saved AS SELECT * FROM github_sources;
DROP TABLE github_sources;
CREATE TABLE github_sources (
 id TEXT PRIMARY KEY,
 room_id TEXT NOT NULL REFERENCES rooms(id),
 repository TEXT NOT NULL COLLATE NOCASE,
 repository_id INTEGER NOT NULL DEFAULT 0,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>0),
 secret_ref TEXT NOT NULL,
 created_by TEXT NOT NULL REFERENCES users(id),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
INSERT INTO github_sources(id,room_id,repository,repository_id,enabled,revision,secret_ref,created_by,created_at,updated_at)
 SELECT id,room_id,repository,repository_id,enabled,revision,secret_ref,created_by,created_at,updated_at FROM github_sources_saved;
DROP TABLE github_sources_saved;
CREATE UNIQUE INDEX github_active_repository ON github_sources(room_id,repository) WHERE enabled=1;

-- +goose Down
-- Retain source identities, observations and their decision/request history.
SELECT 1;
