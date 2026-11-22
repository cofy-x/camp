-- name: AttentionCounts :one
SELECT CAST(count(*) AS INTEGER) AS total,
 CAST(coalesce(sum(kind='judgment'),0) AS INTEGER) AS judgment,
 CAST(coalesce(sum(kind='review'),0) AS INTEGER) AS review,
 CAST(coalesce(sum(kind='exception'),0) AS INTEGER) AS exception
FROM personal_attention
WHERE responsible_user_id=sqlc.arg(actor_id)
 AND (sqlc.arg(room_id)='' OR room_id=sqlc.arg(room_id));

-- name: AttentionPage :many
WITH page AS (
 SELECT queue.* FROM personal_attention queue
 WHERE queue.responsible_user_id=sqlc.arg(actor_id)
  AND (sqlc.arg(room_id)='' OR queue.room_id=sqlc.arg(room_id))
  AND (sqlc.arg(kind)='all' OR queue.kind=sqlc.arg(kind))
  AND (sqlc.arg(after_id)='' OR queue.priority>sqlc.arg(after_priority)
   OR (queue.priority=sqlc.arg(after_priority) AND queue.created_at<sqlc.arg(after_created_at))
   OR (queue.priority=sqlc.arg(after_priority) AND queue.created_at=sqlc.arg(after_created_at) AND queue.id>sqlc.arg(after_id)))
 ORDER BY queue.priority,queue.created_at DESC,queue.id LIMIT sqlc.arg(page_limit)
)
SELECT CAST(page.id AS TEXT) AS id,page.source,page.entity_id,page.room_id,page.room_name,page.kind,page.priority,page.title,page.summary,page.status,page.reason,
 page.created_at,CAST(page.updated_at AS TEXT) AS updated_at,page.target_app,page.work_item_id,page.review_id,page.task_id,
 CAST(coalesce(json_extract(w.value,'$.workRecovery'),'') AS TEXT) AS work_recovery,
 CAST(coalesce(json_extract(w.value,'$.question'),'') AS TEXT) AS work_question,
 CAST(coalesce(json_extract(w.value,'$.deadline'),'') AS TEXT) AS work_deadline,
 CAST(coalesce(d.value,'') AS TEXT) AS legacy_recovery_decision,
 CAST(coalesce(t.status,'') AS TEXT) AS legacy_recovery_task_status,
 CAST(coalesce(p.source_id,'') AS TEXT) AS legacy_recovery_source_id
FROM page
LEFT JOIN work_items w ON page.source='work_item' AND page.kind='exception' AND page.entity_id=w.id
 AND w.status='needs_attention' AND coalesce(json_extract(w.value,'$.activeTaskId'),'')=''
 AND coalesce(json_extract(w.value,'$.stopRequested'),0)=0
 AND NOT EXISTS(SELECT 1 FROM github_publications g WHERE g.work_item_id=w.id
  AND (g.status IN ('unknown','blocked') OR coalesce(json_extract(g.value,'$.reconcile'),0)=1))
LEFT JOIN work_item_decisions d ON w.id IS NOT NULL AND json_type(w.value,'$.workRecovery') IS NULL AND d.seq=(
 SELECT latest.seq FROM work_item_decisions latest WHERE latest.work_item_id=w.id
  AND json_extract(latest.value,'$.kind') IN ('request_judgment','result','wait','execute','accept','complete','cancel','stop','answer','answer_continue','create','delegate')
 ORDER BY latest.seq DESC LIMIT 1)
LEFT JOIN work_item_tasks linked ON linked.work_item_id=w.id AND linked.task_id=json_extract(d.value,'$.taskId')
LEFT JOIN collaboration_tasks t ON t.id=linked.task_id
LEFT JOIN automation_policies p ON d.seq IS NOT NULL AND p.id=w.policy_id AND p.room_id=w.room_id AND p.owner_user_id=w.owner_user_id
ORDER BY page.priority,page.created_at DESC,page.id;
