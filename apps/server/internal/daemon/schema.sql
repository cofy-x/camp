PRAGMA journal_mode = WAL;
PRAGMA synchronous = FULL;
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS commands (
 id TEXT PRIMARY KEY,
 operation_id TEXT NOT NULL UNIQUE,
 digest TEXT NOT NULL,
 body BLOB NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('accepted','running','uploading','terminal')),
 result BLOB,
 archive_path TEXT NOT NULL DEFAULT '',
 cancelled INTEGER NOT NULL DEFAULT 0,
 receipt BLOB,
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
 operation_id TEXT NOT NULL REFERENCES commands(operation_id),
 sequence INTEGER NOT NULL,
 kind TEXT NOT NULL,
 text TEXT NOT NULL,
 acknowledged INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(operation_id,sequence)
);
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY,value TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS coordination_intents (
 operation_id TEXT PRIMARY KEY REFERENCES commands(operation_id),
 body BLOB NOT NULL
);
