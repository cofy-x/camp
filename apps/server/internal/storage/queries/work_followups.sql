-- name: HasPendingWorkFollowUp :many
SELECT * FROM automation_events
WHERE status='pending' AND seq>sqlc.arg(after_seq)
 AND json_extract(value,'$.workItemId')=sqlc.arg(work_item_id)
ORDER BY seq;

-- name: HasHumanWorkDecisionAfter :one
SELECT count(*) FROM work_item_decisions
WHERE work_item_id=sqlc.arg(work_item_id) AND seq>sqlc.arg(after_seq)
 AND json_extract(value,'$.actorKind')='user';
