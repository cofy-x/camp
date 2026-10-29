-- +goose Up
ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN avatar TEXT NOT NULL DEFAULT '';

-- Empty selections are backfilled transactionally from the bundled catalog
-- before the Store is returned. Existing message and Run snapshots stay intact.

-- +goose Down
ALTER TABLE agents DROP COLUMN avatar;
ALTER TABLE users DROP COLUMN avatar;
