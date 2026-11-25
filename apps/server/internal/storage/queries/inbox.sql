-- name: CountUnreadInbox :one
SELECT COUNT(*) FROM inbox WHERE user_id=? AND read_at='';

-- name: InboxPage :many
SELECT i.*,room.name AS room_name,COALESCE(review.title,'') AS review_title,
CAST(COALESCE(NULLIF(i.work_item_id,''),relation.work_item_id,'') AS TEXT) AS resolved_work_item_id,
CAST(COALESCE(NULLIF(json_extract(w.value,'$.goal'),''),NULLIF(json_extract(notification_task.snapshot,'$.goal'),''),review.title,'') AS TEXT) AS title,
CAST(COALESCE(NULLIF(json_extract(w.value,'$.question'),''),
 CASE WHEN active_task.status='waiting_for_person' THEN NULLIF(json_extract(active_task.snapshot,'$.question.text'),'') END,
 CASE WHEN notification_task.status='waiting_for_person' THEN json_extract(notification_task.snapshot,'$.question.text') END,'') AS TEXT) AS question,
CAST(COALESCE(NULLIF(json_extract(notification_task.snapshot,'$.summary'),''),message.content,'') AS TEXT) AS summary
FROM inbox i JOIN rooms room ON room.id=i.room_id LEFT JOIN reviews review ON review.id=i.review_id
LEFT JOIN collaboration_tasks notification_task ON notification_task.id=i.task_id
LEFT JOIN work_item_tasks relation ON relation.task_id=i.task_id
LEFT JOIN work_items w ON w.id=COALESCE(NULLIF(i.work_item_id,''),relation.work_item_id)
LEFT JOIN collaboration_tasks active_task ON active_task.id=json_extract(w.value,'$.activeTaskId')
LEFT JOIN messages message ON message.id=i.message_id
WHERE i.id IN (
 SELECT selected.id FROM inbox selected
 WHERE selected.user_id=sqlc.arg(user_id)
   AND (selected.created_at, selected.id) < (sqlc.arg(before_created_at), sqlc.arg(before_id))
 ORDER BY selected.created_at DESC,selected.id DESC
 LIMIT sqlc.arg(page_limit)
)
ORDER BY i.created_at DESC,i.id DESC;
