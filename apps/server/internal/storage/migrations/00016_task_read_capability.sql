-- +goose Up
ALTER TABLE run_attempts ADD COLUMN task_read_token TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Preserve the private capability identity of retained Attempts.
SELECT 1;
