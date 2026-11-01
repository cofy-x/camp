-- +goose Up
ALTER TABLE resources ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0);

-- +goose Down
ALTER TABLE resources DROP COLUMN revision;
