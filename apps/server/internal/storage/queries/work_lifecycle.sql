-- name: DisableWorkWatches :exec
UPDATE work_item_watches SET enabled=0 WHERE work_item_id=? AND enabled=1;
-- name: RetireWorkFollowUps :exec
UPDATE automation_events SET status='context',reason='work_stopped'
WHERE status IN ('pending','evaluating') AND json_extract(value,'$.workItemId')=?;
-- name: WorkDeliveriesToWithdraw :many
SELECT d.* FROM automation_deliveries d JOIN work_item_actions a ON a.delivery_id=d.id
WHERE a.work_item_id=? AND d.status IN ('pending','blocked','failed','unknown') ORDER BY d.created_at,d.id LIMIT 100;
-- name: WorkObservedRoomThread :one
SELECT CAST(count(*) AS INTEGER) FROM work_item_evidence e
JOIN automation_events a ON a.id=e.event_id
JOIN messages m ON m.id=a.message_id AND m.room_id=a.room_id
WHERE e.work_item_id=sqlc.arg(work_item_id) AND m.room_id=sqlc.arg(room_id)
 AND m.room_id||':'||coalesce(nullif(m.thread_root_id,''),m.id)=sqlc.arg(thread_key);
