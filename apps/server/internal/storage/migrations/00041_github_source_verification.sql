-- +goose Up
CREATE INDEX github_event_source_revision_page ON github_events(source_id, source_revision, seq DESC);

-- +goose Down
DROP INDEX github_event_source_revision_page;
