-- name: MessagesBefore :many
SELECT * FROM messages WHERE room_id=sqlc.arg(room_id)
 AND coalesce(thread_root_id,'')=CAST(sqlc.arg(thread_id) AS TEXT)
 AND seq<sqlc.arg(before_seq) ORDER BY seq DESC LIMIT sqlc.arg(page_limit);
-- name: MessagesAfter :many
SELECT * FROM messages WHERE room_id=sqlc.arg(room_id)
 AND coalesce(thread_root_id,'')=CAST(sqlc.arg(thread_id) AS TEXT)
 AND seq>sqlc.arg(after_seq) ORDER BY seq LIMIT sqlc.arg(page_limit);
-- name: MessageWindowEdges :one
SELECT EXISTS(SELECT 1 FROM messages older WHERE older.room_id=sqlc.arg(room_id)
 AND coalesce(older.thread_root_id,'')=CAST(sqlc.arg(thread_id) AS TEXT) AND older.seq<sqlc.arg(first_seq)) AS has_older,
 EXISTS(SELECT 1 FROM messages newer WHERE newer.room_id=sqlc.arg(room_id)
 AND coalesce(newer.thread_root_id,'')=CAST(sqlc.arg(thread_id) AS TEXT) AND newer.seq>sqlc.arg(last_seq)) AS has_newer;
-- name: ConversationRunIDs :many
SELECT r.id FROM messages m JOIN runs r ON r.id=m.run_id
 WHERE m.room_id=sqlc.arg(room_id) AND r.room_id=sqlc.arg(room_id) AND m.id IN (SELECT value FROM json_each(sqlc.arg(message_ids)))
UNION
SELECT r.id FROM messages m JOIN runs r ON r.origin_message_id=m.id
 WHERE m.room_id=sqlc.arg(room_id) AND r.room_id=sqlc.arg(room_id) AND m.id IN (SELECT value FROM json_each(sqlc.arg(message_ids)))
UNION
SELECT coalesce((SELECT r.id FROM runs r WHERE r.room_id=m.room_id AND r.thread_root_id=m.id
 ORDER BY r.created_at DESC,r.id DESC LIMIT 1),'') FROM messages m
 WHERE m.room_id=sqlc.arg(room_id) AND m.id IN (SELECT value FROM json_each(sqlc.arg(message_ids)))
UNION
SELECT coalesce((SELECT r.id FROM runs r WHERE r.room_id=m.room_id AND r.thread_root_id=m.id
 AND r.status IN ('queued','preparing','running','checking','cancelling')
 ORDER BY r.created_at DESC,r.id DESC LIMIT 1),'') FROM messages m
 WHERE m.room_id=sqlc.arg(room_id) AND m.id IN (SELECT value FROM json_each(sqlc.arg(message_ids)));
-- name: ConversationMessageTaskIDs :many
SELECT DISTINCT task_id FROM messages WHERE room_id=sqlc.arg(room_id)
 AND id IN (SELECT value FROM json_each(sqlc.arg(message_ids))) AND task_id<>'';
-- name: ConversationTaskLink :one
SELECT CAST(json_object('id',id,'roomId',room_id,'status',status,
 'workItemId',coalesce(json_extract(snapshot,'$.workItemId'),''),
 'originMessageId',coalesce(json_extract(snapshot,'$.originMessageId'),'')) AS TEXT)
FROM collaboration_tasks WHERE id=?;
-- name: RoomTaskListPage :many
SELECT CAST(json_object('id',id,'roomId',room_id,'status',status,
 'workItemId',coalesce(json_extract(snapshot,'$.workItemId'),''),
 'goal',json_extract(snapshot,'$.goal'),'error',json_extract(snapshot,'$.error'),
 'question',json_extract(snapshot,'$.question'),
 'participants',json(coalesce((SELECT json_group_array(json_object('agent',json_object('name',json_extract(p.value,'$.agent.name'))))
 FROM json_each(snapshot,'$.participants') p),'[]'))) AS TEXT) AS value,created_at,id
FROM collaboration_tasks WHERE room_id=sqlc.arg(room_id)
 AND (created_at,id)<(sqlc.arg(before_at),sqlc.arg(before_id))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: RoomTaskAttentionPage :many
SELECT CAST(json_object('id',id,'roomId',room_id,'status',status,
 'workItemId',coalesce(json_extract(snapshot,'$.workItemId'),''),
 'goal',json_extract(snapshot,'$.goal'),'error',json_extract(snapshot,'$.error'),
 'question',json_extract(snapshot,'$.question'),
 'participants',json(coalesce((SELECT json_group_array(json_object('agent',json_object('name',json_extract(p.value,'$.agent.name'))))
 FROM json_each(snapshot,'$.participants') p),'[]'))) AS TEXT) AS value,created_at,id
FROM collaboration_tasks WHERE room_id=sqlc.arg(room_id)
 AND status IN ('waiting_for_person','needs_attention','interrupted')
 AND (created_at,id)<(sqlc.arg(before_at),sqlc.arg(before_id))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
