-- +goose Up
CREATE TABLE collaboration_tasks (
 id TEXT PRIMARY KEY NOT NULL,
 room_id TEXT NOT NULL REFERENCES rooms(id),
 requested_by_user_id TEXT NOT NULL REFERENCES users(id),
 status TEXT NOT NULL CHECK(status IN ('queued','running','waiting_for_person','needs_attention','publishing','completed','cancelling','cancelled','interrupted')),
 revision INTEGER NOT NULL CHECK(revision > 0),
 snapshot TEXT NOT NULL CHECK(json_valid(snapshot)),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX tasks_room ON collaboration_tasks(room_id,created_at,id);
CREATE INDEX tasks_status ON collaboration_tasks(status,created_at,id);
CREATE TABLE task_requests (
 user_id TEXT NOT NULL REFERENCES users(id),
 scope TEXT NOT NULL,
 request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(user_id,scope,request_id)
);
ALTER TABLE messages ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
ALTER TABLE resource_publications ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
CREATE TABLE collaboration_inbox (
 id TEXT PRIMARY KEY NOT NULL, user_id TEXT NOT NULL REFERENCES users(id),
 kind TEXT NOT NULL CHECK(kind IN ('mention','run_finished','review_requested','review_updated','task_question','task_attention','task_finished')),
 room_id TEXT NOT NULL REFERENCES rooms(id),
 message_id TEXT NOT NULL REFERENCES messages(id), run_id TEXT NOT NULL DEFAULT '',
 review_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, read_at TEXT NOT NULL DEFAULT '', UNIQUE(user_id,kind,message_id)
);
INSERT INTO collaboration_inbox(id,user_id,kind,room_id,message_id,run_id,review_id,created_at,read_at)
SELECT id,user_id,kind,room_id,message_id,run_id,review_id,created_at,read_at FROM inbox;
DROP TABLE inbox;
ALTER TABLE collaboration_inbox RENAME TO inbox;

-- +goose Down
-- Collaboration records cannot be downgraded without discarding user work.
SELECT 1;
