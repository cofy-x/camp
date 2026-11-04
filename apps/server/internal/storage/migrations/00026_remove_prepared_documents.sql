-- +goose Up
-- Derived PDF/DOCX snapshots are retired. Immutable uploaded originals remain
-- in their Resources. Restore a stopped backup to return to an older Server.
DROP TABLE documents;
