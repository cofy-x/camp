-- +goose Up
CREATE TABLE runners (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    credential_hash TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1)),
    storage_id TEXT NOT NULL,
    runtime TEXT NOT NULL DEFAULT '',
    slots INTEGER NOT NULL DEFAULT 1 CHECK(slots BETWEEN 1 AND 32),
    capabilities TEXT NOT NULL DEFAULT '{}',
    sources TEXT NOT NULL DEFAULT '[]',
    connection_generation INTEGER NOT NULL DEFAULT 0,
    connected_at TEXT NOT NULL DEFAULT '',
    last_seen_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE runner_enrollments (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1)),
    registration_id TEXT NOT NULL DEFAULT '',
    runner_id TEXT REFERENCES runners(id),
    created_at TEXT NOT NULL
);
ALTER TABLE rooms ADD COLUMN runner_id TEXT REFERENCES runners(id);
ALTER TABLE rooms ADD COLUMN runner_revision INTEGER NOT NULL DEFAULT 0;
CREATE TABLE resource_writer_leases (
    resource_id TEXT PRIMARY KEY REFERENCES resources(id),
    owner TEXT NOT NULL,
    fence INTEGER NOT NULL CHECK(fence > 0),
    expires_at TEXT NOT NULL
);
CREATE TABLE run_attempts (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE REFERENCES runs(id),
    runner_id TEXT NOT NULL REFERENCES runners(id),
    fence INTEGER NOT NULL,
    command_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK(status IN ('dispatching','executing','uploading','finished')),
    lease_expires_at TEXT NOT NULL,
    deadline TEXT NOT NULL,
    upload_expires_at TEXT NOT NULL DEFAULT '',
    cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK(cancel_requested IN (0,1)),
    capability_hash TEXT NOT NULL UNIQUE,
    model_in_flight INTEGER NOT NULL DEFAULT 0 CHECK(model_in_flight IN (0,1)),
    input_digest TEXT NOT NULL DEFAULT '',
    input_path TEXT NOT NULL DEFAULT '',
    artifact_digest TEXT NOT NULL DEFAULT '',
    artifact_path TEXT NOT NULL DEFAULT '',
    result_hash TEXT NOT NULL DEFAULT '',
    result_id TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX attempts_runner ON run_attempts(runner_id,status);
CREATE TABLE runner_commands (
    id TEXT PRIMARY KEY,
    runner_id TEXT NOT NULL REFERENCES runners(id),
    operation_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    payload TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    acknowledged INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged IN (0,1)),
    created_at TEXT NOT NULL,
    UNIQUE(operation_id,kind)
);
CREATE INDEX runner_command_pending ON runner_commands(runner_id,acknowledged,created_at);
CREATE TABLE runner_event_receipts (
    operation_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK(sequence > 0),
    payload_hash TEXT NOT NULL,
    kind TEXT NOT NULL,
    text TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY(operation_id,sequence)
);
CREATE TABLE source_imports (
    id TEXT PRIMARY KEY,
    runner_id TEXT NOT NULL REFERENCES runners(id),
    requested_by_user_id TEXT NOT NULL REFERENCES users(id),
    request_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    source TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    source_alias TEXT NOT NULL,
    room_name TEXT NOT NULL,
    resource_key TEXT NOT NULL UNIQUE,
    command_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK(status IN ('queued','capturing','uploading','cancelling','succeeded','failed','cancelled','interrupted')),
    expires_at TEXT NOT NULL,
    room_id TEXT REFERENCES rooms(id),
    resource_id TEXT REFERENCES resources(id),
    artifact_digest TEXT NOT NULL DEFAULT '',
    artifact_path TEXT NOT NULL DEFAULT '',
    commit_id TEXT NOT NULL DEFAULT '',
    completion_id TEXT NOT NULL DEFAULT '',
    result_hash TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(requested_by_user_id,request_id)
);

-- +goose Down
DROP TABLE source_imports;
DROP TABLE runner_event_receipts;
DROP TABLE runner_commands;
DROP TABLE run_attempts;
DROP TABLE resource_writer_leases;
ALTER TABLE rooms DROP COLUMN runner_revision;
ALTER TABLE rooms DROP COLUMN runner_id;
DROP TABLE runner_enrollments;
DROP TABLE runners;
