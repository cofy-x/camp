-- +goose Up
CREATE TABLE feishu_sources (
 id TEXT PRIMARY KEY,
 room_id TEXT NOT NULL REFERENCES rooms(id),
 app_id TEXT NOT NULL,
 tenant_key TEXT NOT NULL,
 chat_id TEXT NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>0),
 credential_ref TEXT NOT NULL,
 created_by TEXT NOT NULL REFERENCES users(id),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX feishu_active_scope ON feishu_sources(room_id,app_id,tenant_key,chat_id) WHERE enabled=1;
CREATE TABLE feishu_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 source_id TEXT NOT NULL REFERENCES feishu_sources(id),
 room_id TEXT NOT NULL REFERENCES rooms(id),
 source_revision INTEGER NOT NULL,
 credential_ref TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 value TEXT NOT NULL CHECK(json_valid(value)),
 payload BLOB NOT NULL CHECK(length(payload)<=1048576),
 received_at TEXT NOT NULL,
 UNIQUE(source_id,fingerprint)
);
CREATE INDEX feishu_room_events ON feishu_events(room_id,seq);
CREATE TABLE feishu_deliveries (
 source_id TEXT NOT NULL REFERENCES feishu_sources(id),
 delivery_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 event_id TEXT NOT NULL REFERENCES feishu_events(id),
 PRIMARY KEY(source_id,delivery_id)
);
CREATE TABLE feishu_requests (
 user_id TEXT NOT NULL REFERENCES users(id),
 request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(user_id,request_id)
);

-- +goose Down
-- Preserve original encrypted deliveries, source identities and receipts.
SELECT 1;
