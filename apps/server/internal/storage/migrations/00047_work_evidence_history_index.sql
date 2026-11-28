-- +goose Up
CREATE INDEX work_item_evidence_history ON work_item_evidence(work_item_id,seq);

-- +goose Down
DROP INDEX work_item_evidence_history;
