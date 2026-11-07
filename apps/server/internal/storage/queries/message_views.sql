-- name: MessageWorkOrigins :many
SELECT m.id,w.id AS work_item_id
FROM messages m
JOIN collaboration_tasks t ON t.id=m.task_id AND t.room_id=m.room_id
JOIN work_item_tasks link ON link.task_id=t.id
JOIN work_items w ON w.id=link.work_item_id AND w.room_id=m.room_id
WHERE m.id IN (SELECT CAST(value AS TEXT) FROM json_each(sqlc.arg(message_ids)))
 AND m.id=CAST(json_extract(t.snapshot,'$.originMessageId') AS TEXT)
 AND w.id=CAST(json_extract(t.snapshot,'$.workItemId') AS TEXT);
