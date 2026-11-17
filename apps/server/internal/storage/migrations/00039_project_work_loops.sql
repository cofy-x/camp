-- +goose Up
CREATE TABLE project_settings (
 owner_user_id TEXT NOT NULL REFERENCES users(id), room_id TEXT NOT NULL REFERENCES rooms(id),
 revision INTEGER NOT NULL CHECK(revision>0), value TEXT NOT NULL CHECK(json_valid(value)),
 PRIMARY KEY(owner_user_id,room_id)
);
CREATE TABLE project_setting_requests (
 owner_user_id TEXT NOT NULL REFERENCES users(id), request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL, result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(owner_user_id,request_id)
);
CREATE TABLE github_delivery_sources (
 delivery_id TEXT NOT NULL REFERENCES github_publications(id), attempt INTEGER NOT NULL,
 resource_id TEXT NOT NULL REFERENCES resources(id), baseline_commit TEXT NOT NULL,
 result_commit TEXT NOT NULL, task_id TEXT NOT NULL REFERENCES collaboration_tasks(id),
 created_at TEXT NOT NULL,
 PRIMARY KEY(delivery_id,attempt)
);
-- +goose StatementBegin
CREATE TRIGGER github_delivery_sources_immutable_update BEFORE UPDATE ON github_delivery_sources BEGIN SELECT RAISE(ABORT,'delivery sources are immutable'); END;
CREATE TRIGGER github_delivery_sources_immutable_delete BEFORE DELETE ON github_delivery_sources BEGIN SELECT RAISE(ABORT,'delivery sources are immutable'); END;
-- +goose StatementEnd
