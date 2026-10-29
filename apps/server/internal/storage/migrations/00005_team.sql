-- +goose Up
CREATE TABLE team_profile(id INTEGER PRIMARY KEY CHECK(id=1),name TEXT NOT NULL,updated_at TEXT NOT NULL);
INSERT INTO team_profile(id,name,updated_at) VALUES(1,'Camp',strftime('%Y-%m-%dT%H:%M:%fZ','now'));
CREATE TABLE users (
 id TEXT PRIMARY KEY NOT NULL, username TEXT NOT NULL UNIQUE COLLATE NOCASE,
 name TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','member')),
 enabled BOOLEAN NOT NULL CHECK(enabled IN (0,1)), password_hash TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE management_events(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id TEXT NOT NULL REFERENCES users(id),action TEXT NOT NULL,object_id TEXT NOT NULL,detail TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE team_setup (id INTEGER PRIMARY KEY CHECK(id=1), token_hash TEXT NOT NULL);
CREATE TABLE sessions (
 token_hash TEXT PRIMARY KEY NOT NULL, user_id TEXT NOT NULL REFERENCES users(id),
 created_at TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE invites (
 id TEXT PRIMARY KEY NOT NULL, token_hash TEXT NOT NULL UNIQUE,
 created_by_user_id TEXT NOT NULL REFERENCES users(id), created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL, consumed_at TEXT NOT NULL DEFAULT '', revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE user_preferences (
 user_id TEXT PRIMARY KEY NOT NULL REFERENCES users(id), value TEXT NOT NULL CHECK(json_valid(value))
);
CREATE TABLE user_layouts (
 user_id TEXT NOT NULL REFERENCES users(id), client_id TEXT NOT NULL,
 value TEXT NOT NULL CHECK(json_valid(value)), PRIMARY KEY(user_id,client_id)
);
CREATE TABLE user_drafts (
 user_id TEXT NOT NULL REFERENCES users(id), kind TEXT NOT NULL CHECK(kind IN ('conversation','document')),
 draft_key TEXT NOT NULL, value TEXT NOT NULL CHECK(json_valid(value)), revision INTEGER NOT NULL,
 updated_at TEXT NOT NULL, PRIMARY KEY(user_id,kind,draft_key)
);
CREATE TABLE room_reads (
 user_id TEXT NOT NULL REFERENCES users(id), room_id TEXT NOT NULL REFERENCES rooms(id),
 seq INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(user_id,room_id)
);
CREATE TABLE inbox (
 id TEXT PRIMARY KEY NOT NULL, user_id TEXT NOT NULL REFERENCES users(id),
 kind TEXT NOT NULL CHECK(kind IN ('mention','run_finished')), room_id TEXT NOT NULL REFERENCES rooms(id),
 message_id TEXT NOT NULL REFERENCES messages(id), run_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, read_at TEXT NOT NULL DEFAULT '', UNIQUE(user_id,kind,message_id)
);
ALTER TABLE rooms ADD COLUMN last_message_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN author_user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN author_name TEXT NOT NULL DEFAULT '';
DROP INDEX message_request;
CREATE UNIQUE INDEX message_request ON messages(room_id,author_user_id,request_id) WHERE request_id<>'';
ALTER TABLE runs ADD COLUMN requested_by_user_id TEXT NOT NULL DEFAULT '';

-- +goose Down
DROP TABLE inbox;
DROP TABLE room_reads;
DROP TABLE user_drafts;
DROP TABLE user_layouts;
DROP TABLE user_preferences;
DROP TABLE invites;
DROP TABLE sessions;
DROP TABLE team_setup;
DROP TABLE management_events;
DROP TABLE users;
DROP TABLE team_profile;
