-- +goose Up
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
 updated_at TEXT NOT NULL,
 UNIQUE(room_id,repository)
);
CREATE TABLE github_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 source_id TEXT NOT NULL REFERENCES github_sources(id),
 room_id TEXT NOT NULL REFERENCES rooms(id),
 source_revision INTEGER NOT NULL,
 fingerprint TEXT NOT NULL,
 disposition TEXT NOT NULL CHECK(disposition IN ('pending','ignored')),
 value TEXT NOT NULL CHECK(json_valid(value)),
 payload BLOB NOT NULL CHECK(length(payload)<=1048576),
 received_at TEXT NOT NULL,
 UNIQUE(source_id,fingerprint)
);
CREATE INDEX github_event_room_page ON github_events(room_id,seq);
CREATE TABLE github_deliveries (
 source_id TEXT NOT NULL REFERENCES github_sources(id),
 delivery_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 event_id TEXT NOT NULL REFERENCES github_events(id),
 PRIMARY KEY(source_id,delivery_id)
);
CREATE TABLE github_event_decisions (
 event_id TEXT PRIMARY KEY REFERENCES github_events(id),
 outcome TEXT NOT NULL CHECK(outcome IN ('accepted','dismissed')),
 actor_id TEXT NOT NULL REFERENCES users(id),
 task_id TEXT REFERENCES collaboration_tasks(id),
 reason TEXT NOT NULL,
 created_at TEXT NOT NULL,
 CHECK((outcome='accepted' AND task_id IS NOT NULL) OR (outcome='dismissed' AND task_id IS NULL))
);
CREATE TABLE github_requests (
 user_id TEXT NOT NULL REFERENCES users(id),
 request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(user_id,request_id)
);

-- +goose Down
-- Observation originals, decisions, source identities and receipts are retained.
SELECT 1;
