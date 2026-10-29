-- +goose Up
CREATE TABLE resource_publications (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 resource_id TEXT NOT NULL REFERENCES resources(id),
 previous_commit TEXT NOT NULL,
 commit_id TEXT NOT NULL,
 author_user_id TEXT REFERENCES users(id),
 run_id TEXT REFERENCES runs(id),
 kind TEXT NOT NULL CHECK(kind IN ('edit','restore','agent')),
 path TEXT NOT NULL DEFAULT '',
 checks_status TEXT NOT NULL CHECK(checks_status IN ('not_run','not_configured','passed')),
 request_id TEXT NOT NULL DEFAULT '',
 request_hash TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX publication_request ON resource_publications(resource_id,author_user_id,request_id) WHERE request_id<>'';
CREATE UNIQUE INDEX publication_run ON resource_publications(run_id) WHERE run_id IS NOT NULL;
CREATE INDEX publication_resource ON resource_publications(resource_id,seq DESC);

-- +goose Down
DROP TABLE resource_publications;
