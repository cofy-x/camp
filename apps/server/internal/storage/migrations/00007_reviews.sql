-- +goose Up
CREATE TABLE reviews (
 id TEXT PRIMARY KEY NOT NULL,
 room_id TEXT NOT NULL REFERENCES rooms(id),
 resource_id TEXT NOT NULL REFERENCES resources(id),
 path TEXT NOT NULL,
 title TEXT NOT NULL,
 requester_user_id TEXT NOT NULL REFERENCES users(id),
 reviewer_user_id TEXT NOT NULL REFERENCES users(id),
 submitted_commit TEXT NOT NULL,
 delivered_commit TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('in_review','changes_requested','approved','delivered','cancelled')),
 revision INTEGER NOT NULL CHECK(revision > 0),
 thread_root_id TEXT NOT NULL UNIQUE REFERENCES messages(id),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX reviews_room ON reviews(room_id,updated_at DESC,id);
CREATE INDEX reviews_resource ON reviews(resource_id,updated_at DESC,id);
CREATE TABLE review_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 review_id TEXT NOT NULL REFERENCES reviews(id),
 actor_user_id TEXT NOT NULL REFERENCES users(id),
 actor_name TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('request','approve','request_changes','resubmit','deliver','cancel','reassign')),
 comment TEXT NOT NULL,
 commit_id TEXT NOT NULL,
 reviewer_user_id TEXT NOT NULL REFERENCES users(id),
 revision INTEGER NOT NULL,
 message_id TEXT NOT NULL UNIQUE REFERENCES messages(id),
 created_at TEXT NOT NULL,
 UNIQUE(review_id,revision)
);
CREATE TABLE review_requests (
 user_id TEXT NOT NULL REFERENCES users(id),
 room_id TEXT NOT NULL REFERENCES rooms(id),
 request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(user_id,room_id,request_id)
);
ALTER TABLE messages ADD COLUMN review_id TEXT NOT NULL DEFAULT '';
CREATE TABLE review_inbox (
 id TEXT PRIMARY KEY NOT NULL, user_id TEXT NOT NULL REFERENCES users(id),
 kind TEXT NOT NULL CHECK(kind IN ('mention','run_finished','review_requested','review_updated')),
 room_id TEXT NOT NULL REFERENCES rooms(id),
 message_id TEXT NOT NULL REFERENCES messages(id), run_id TEXT NOT NULL DEFAULT '',
 review_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, read_at TEXT NOT NULL DEFAULT '', UNIQUE(user_id,kind,message_id)
);
INSERT INTO review_inbox(id,user_id,kind,room_id,message_id,run_id,created_at,read_at)
SELECT id,user_id,kind,room_id,message_id,run_id,created_at,read_at FROM inbox;
DROP TABLE inbox;
ALTER TABLE review_inbox RENAME TO inbox;

CREATE TABLE review_drafts (
 user_id TEXT NOT NULL REFERENCES users(id), kind TEXT NOT NULL CHECK(kind IN ('conversation','document','review')),
 draft_key TEXT NOT NULL, value TEXT NOT NULL CHECK(json_valid(value)), revision INTEGER NOT NULL,
 updated_at TEXT NOT NULL, PRIMARY KEY(user_id,kind,draft_key)
);
INSERT INTO review_drafts SELECT * FROM user_drafts;
DROP TABLE user_drafts;
ALTER TABLE review_drafts RENAME TO user_drafts;

-- +goose Down
DROP TABLE review_requests;
DROP TABLE review_events;
DROP TABLE reviews;
