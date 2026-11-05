-- +goose Up
CREATE TABLE work_action_receipts (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 work_item_id TEXT NOT NULL REFERENCES work_items(id),
 delivery_id TEXT NOT NULL REFERENCES automation_deliveries(id),
 delivery_revision INTEGER NOT NULL CHECK(delivery_revision>0),
 event_id TEXT NOT NULL UNIQUE REFERENCES automation_events(id),
 semantic_hash TEXT NOT NULL,
 value TEXT NOT NULL CHECK(json_valid(value)),
 created_at TEXT NOT NULL,
 UNIQUE(delivery_id,delivery_revision)
);
CREATE INDEX work_action_receipt_history ON work_action_receipts(delivery_id,seq);
-- +goose StatementBegin
CREATE TRIGGER work_action_receipts_immutable_update BEFORE UPDATE ON work_action_receipts
BEGIN SELECT RAISE(ABORT,'Action receipts are immutable'); END;
CREATE TRIGGER work_action_receipts_immutable_delete BEFORE DELETE ON work_action_receipts
BEGIN SELECT RAISE(ABORT,'Action receipts are immutable'); END;
-- +goose StatementEnd

-- +goose Down
-- Preserve confirmed and uncertain side-effect evidence.
SELECT 1;
