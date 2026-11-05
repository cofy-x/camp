-- +goose Up
CREATE TABLE memories (
 id TEXT PRIMARY KEY,
 room_id TEXT NOT NULL REFERENCES rooms(id),
 author_user_id TEXT NOT NULL REFERENCES users(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 created_at TEXT NOT NULL
);
CREATE INDEX memories_room ON memories(room_id,created_at,id);
CREATE TABLE memory_revisions (
 memory_id TEXT NOT NULL REFERENCES memories(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 state TEXT NOT NULL CHECK(state IN ('active','retracted')),
 content TEXT NOT NULL,
 value TEXT NOT NULL CHECK(json_valid(value)),
 created_at TEXT NOT NULL,
 PRIMARY KEY(memory_id,revision)
);
CREATE TABLE memory_requests (
 user_id TEXT NOT NULL REFERENCES users(id),
 request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(user_id,request_id)
);
-- +goose StatementBegin
CREATE TRIGGER memory_revisions_immutable_update BEFORE UPDATE ON memory_revisions
BEGIN SELECT RAISE(ABORT,'Memory revisions are immutable'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER memory_revisions_immutable_delete BEFORE DELETE ON memory_revisions
BEGIN SELECT RAISE(ABORT,'Memory revisions are immutable'); END;
-- +goose StatementEnd

-- +goose Down
-- Preserve historical evidence and request receipts.
SELECT 1;
