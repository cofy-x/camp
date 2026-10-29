-- +goose Up
ALTER TABLE messages ADD COLUMN references_json TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(references_json) AND json_type(references_json) = 'array');
CREATE INDEX runs_resource ON runs(resource_id);

-- +goose Down
DROP INDEX runs_resource;
ALTER TABLE messages DROP COLUMN references_json;
