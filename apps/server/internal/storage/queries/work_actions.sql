-- name: GetWorkActionForDelivery :one
SELECT * FROM work_item_actions WHERE delivery_id=?;
-- name: LatestWorkActionReceipt :one
SELECT * FROM work_action_receipts WHERE delivery_id=? ORDER BY seq DESC LIMIT 1;
-- name: InsertWorkActionReceipt :exec
INSERT INTO work_action_receipts(work_item_id,delivery_id,delivery_revision,event_id,semantic_hash,value,created_at) VALUES(?,?,?,?,?,?,?);
-- name: WorkActionSummaries :many
SELECT a.id,a.kind,CAST(coalesce(d.status,a.status) AS TEXT) AS current_status,a.task_id,a.delivery_id,a.created_at
FROM work_item_actions a LEFT JOIN automation_deliveries d ON d.id=a.delivery_id
WHERE a.work_item_id=? ORDER BY a.seq DESC LIMIT 21;
-- name: WorkPendingActions :one
SELECT CAST(count(*) AS INTEGER) FROM work_item_actions a
LEFT JOIN automation_deliveries d ON d.id=a.delivery_id
WHERE a.work_item_id=? AND a.delivery_id<>'' AND (
 d.id IS NULL OR d.status NOT IN ('sent','withdrawn') OR (
  d.status='withdrawn'
  AND coalesce(json_array_length(d.value,'$.receipts'),0)=0
  AND coalesce(json_extract(d.value,'$.commentId'),0)<1
  AND EXISTS(SELECT 1 FROM json_each(d.value,'$.attempts') attempt WHERE json_extract(attempt.value,'$.outcome') IN ('unknown','sending'))
 )
);
