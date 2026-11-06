-- name: AttentionCounts :one
SELECT CAST(count(*) AS INTEGER) AS total,
 CAST(coalesce(sum(kind='judgment'),0) AS INTEGER) AS judgment,
 CAST(coalesce(sum(kind='review'),0) AS INTEGER) AS review,
 CAST(coalesce(sum(kind='exception'),0) AS INTEGER) AS exception
FROM personal_attention
WHERE responsible_user_id=sqlc.arg(actor_id)
 AND (sqlc.arg(room_id)='' OR room_id=sqlc.arg(room_id));

-- name: AttentionPage :many
SELECT CAST(id AS TEXT) AS id,source,entity_id,room_id,room_name,kind,priority,title,summary,status,reason,
 created_at,CAST(updated_at AS TEXT) AS updated_at,target_app,work_item_id,review_id,task_id
FROM personal_attention
WHERE responsible_user_id=sqlc.arg(actor_id)
 AND (sqlc.arg(room_id)='' OR room_id=sqlc.arg(room_id))
 AND (sqlc.arg(kind)='all' OR kind=sqlc.arg(kind))
 AND (sqlc.arg(after_id)='' OR priority>sqlc.arg(after_priority)
  OR (priority=sqlc.arg(after_priority) AND created_at<sqlc.arg(after_created_at))
  OR (priority=sqlc.arg(after_priority) AND created_at=sqlc.arg(after_created_at) AND id>sqlc.arg(after_id)))
ORDER BY priority,created_at DESC,id LIMIT sqlc.arg(page_limit);
