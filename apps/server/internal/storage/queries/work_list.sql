-- name: WorkListFrontier :one
SELECT CAST(coalesce(max(seq),0) AS INTEGER) FROM work_items;

-- name: WorkListPage :many
WITH attention(seq,id,room_id,owner_user_id,policy_id,status,revision,value,created_at,updated_at,room_name,needs_you) AS (
 SELECT w.seq,w.id,w.room_id,w.owner_user_id,w.policy_id,w.status,w.revision,w.value,w.created_at,w.updated_at,r.name AS room_name,
 CAST(EXISTS(SELECT 1 FROM personal_attention a WHERE a.source='work_item' AND a.entity_id=w.id AND a.responsible_user_id=sqlc.arg(actor_id)) AS INTEGER) AS needs_you
 FROM work_items w JOIN rooms r ON r.id=w.room_id
 WHERE w.seq<=sqlc.arg(through_seq)
  AND (sqlc.arg(scope)='team' OR w.owner_user_id=sqlc.arg(actor_id))
  AND (sqlc.arg(room_id)='' OR w.room_id=sqlc.arg(room_id))
)
SELECT * FROM attention
WHERE (sqlc.arg(view)='all'
  OR (sqlc.arg(view)='active' AND status NOT IN ('completed','cancelled'))
  OR (sqlc.arg(view)='done' AND status IN ('completed','cancelled'))
  OR (sqlc.arg(view)='needs_you' AND needs_you=1))
AND (sqlc.arg(before_seq)=0 OR needs_you<CAST(sqlc.arg(before_priority) AS INTEGER)
 OR (needs_you=CAST(sqlc.arg(before_priority) AS INTEGER) AND seq<sqlc.arg(before_seq)))
ORDER BY needs_you DESC,seq DESC LIMIT sqlc.arg(page_limit);
