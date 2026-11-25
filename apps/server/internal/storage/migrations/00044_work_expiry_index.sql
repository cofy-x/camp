-- +goose Up
-- Match the deadline sweep predicate so an idle check can seek to the due range.
CREATE INDEX work_item_expiry_due ON work_items(julianday(json_extract(value,'$.deadline')),seq)
WHERE status NOT IN ('completed','cancelled') AND coalesce(json_extract(value,'$.stopRequested'),0)=0
 AND coalesce(json_extract(value,'$.observedAuthorizationExpiry'),'')<>json_extract(value,'$.deadline')
 AND (status<>'needs_attention' OR coalesce(json_extract(value,'$.workRecovery.kind'),'')<>'renew_authorization');

-- +goose Down
DROP INDEX work_item_expiry_due;
