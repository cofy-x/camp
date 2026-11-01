-- +goose Up
ALTER TABLE run_attempts ADD COLUMN preparation_context TEXT NOT NULL DEFAULT ''
 CHECK(preparation_context='' OR json_valid(preparation_context));

-- +goose Down
-- Retain the frozen identity used to authorize existing Attempts.
SELECT 1;
