-- +goose Up
CREATE TABLE github_recovery_deliveries (
    source_id TEXT NOT NULL REFERENCES github_sources(id),
    delivery_id TEXT NOT NULL,
    prepared_by TEXT NOT NULL,
    prepared_at TEXT NOT NULL,
    PRIMARY KEY (source_id, delivery_id)
) STRICT;

-- +goose Down
DROP TABLE github_recovery_deliveries;
