-- +goose Up
CREATE TABLE documents (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    resource_id TEXT NOT NULL REFERENCES resources(id),
    commit_id TEXT NOT NULL CHECK(length(commit_id)=40),
    path TEXT NOT NULL,
    extractor_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('queued','extracting','ready','unreadable','failed','unavailable')),
    snapshot TEXT NOT NULL CHECK(json_valid(snapshot)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX documents_file ON documents(resource_id,commit_id,path,seq DESC);
CREATE INDEX documents_queue ON documents(status,seq);

-- +goose Down
DROP TABLE documents;
