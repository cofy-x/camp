-- +goose Up
CREATE TABLE github_repositories (
 id TEXT PRIMARY KEY NOT NULL, owner_user_id TEXT NOT NULL REFERENCES users(id),
 revision INTEGER NOT NULL CHECK(revision>0), repository_id INTEGER NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), credential_ref TEXT NOT NULL,
 value TEXT NOT NULL CHECK(json_valid(value)), UNIQUE(owner_user_id,repository_id)
);
CREATE TABLE github_repository_requests (
 owner_user_id TEXT NOT NULL REFERENCES users(id), request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL, result TEXT NOT NULL CHECK(json_valid(result)), cancelled INTEGER NOT NULL DEFAULT 0 CHECK(cancelled IN(0,1)),
 PRIMARY KEY(owner_user_id,request_id)
);
CREATE TABLE github_publications (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 work_item_id TEXT NOT NULL UNIQUE REFERENCES work_items(id),
 connection_id TEXT NOT NULL REFERENCES github_repositories(id),
 revision INTEGER NOT NULL CHECK(revision>0), status TEXT NOT NULL,
 next_check_at TEXT NOT NULL, value TEXT NOT NULL CHECK(json_valid(value))
);
CREATE INDEX github_delivery_queue ON github_publications(status,next_check_at,seq);
CREATE TABLE github_delivery_receipts (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, delivery_id TEXT NOT NULL REFERENCES github_publications(id),
 revision INTEGER NOT NULL, event_id TEXT NOT NULL DEFAULT '', value TEXT NOT NULL CHECK(json_valid(value)),
 UNIQUE(delivery_id,revision)
);
-- +goose StatementBegin
CREATE TRIGGER github_delivery_receipt_immutable_update BEFORE UPDATE ON github_delivery_receipts BEGIN SELECT RAISE(ABORT,'delivery receipts are immutable'); END;
CREATE TRIGGER github_delivery_receipt_immutable_delete BEFORE DELETE ON github_delivery_receipts BEGIN SELECT RAISE(ABORT,'delivery receipts are immutable'); END;
-- +goose StatementEnd
