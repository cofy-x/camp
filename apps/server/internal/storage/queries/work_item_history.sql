-- name: WorkHistoryEvidenceFrontier :one
SELECT CAST(coalesce(max(e.seq),0) AS INTEGER) AS through_seq FROM work_item_evidence e WHERE e.work_item_id=sqlc.arg(work_item_id);
-- name: WorkHistoryEvidencePage :many
SELECT CAST(e.seq AS INTEGER) AS seq,CAST(json_object('eventId',e.event_id,'kind',e.kind,'createdAt',e.created_at) AS TEXT) AS value
FROM work_item_evidence e
WHERE e.work_item_id=sqlc.arg(work_item_id) AND e.seq>sqlc.arg(after_seq) AND e.seq<=sqlc.arg(through_seq)
ORDER BY e.seq LIMIT sqlc.arg(result_limit);
-- name: WorkHistoryDecisionsFrontier :one
SELECT CAST(coalesce(max(d.seq),0) AS INTEGER) AS through_seq FROM work_item_decisions d WHERE d.work_item_id=sqlc.arg(work_item_id);
-- name: WorkHistoryDecisionsPage :many
SELECT CAST(d.seq AS INTEGER) AS seq,CAST(json_set(d.value,'$.seq',d.seq) AS TEXT) AS value
FROM work_item_decisions d
WHERE d.work_item_id=sqlc.arg(work_item_id) AND d.seq>sqlc.arg(after_seq) AND d.seq<=sqlc.arg(through_seq)
ORDER BY d.seq LIMIT sqlc.arg(result_limit);
-- name: WorkHistoryTasksFrontier :one
SELECT CAST(coalesce(max(w.seq),0) AS INTEGER) AS through_seq FROM work_item_tasks w WHERE w.work_item_id=sqlc.arg(work_item_id);
-- name: WorkHistoryTasksPage :many
SELECT CAST(w.seq AS INTEGER) AS seq,CAST(json_object('id',w.task_id,'status',t.status,'summary',coalesce(json_extract(t.snapshot,'$.summary'),''),'createdAt',w.created_at) AS TEXT) AS value
FROM work_item_tasks w JOIN collaboration_tasks t ON t.id=w.task_id
WHERE w.work_item_id=sqlc.arg(work_item_id) AND w.seq>sqlc.arg(after_seq) AND w.seq<=sqlc.arg(through_seq)
ORDER BY w.seq LIMIT sqlc.arg(result_limit);
-- name: WorkHistoryTimersFrontier :one
SELECT CAST(coalesce(max(t.seq),0) AS INTEGER) AS through_seq FROM work_item_timers t WHERE t.work_item_id=sqlc.arg(work_item_id);
-- name: WorkHistoryTimersPage :many
SELECT CAST(t.seq AS INTEGER) AS seq,CAST(json_object('id',t.id,'dueAt',t.due_at,'condition',t.condition,'status',t.status,'eventId',t.event_id,'createdAt',t.created_at) AS TEXT) AS value
FROM work_item_timers t
WHERE t.work_item_id=sqlc.arg(work_item_id) AND t.seq>sqlc.arg(after_seq) AND t.seq<=sqlc.arg(through_seq)
ORDER BY t.seq LIMIT sqlc.arg(result_limit);
-- name: WorkHistoryActionsFrontier :one
SELECT CAST(coalesce(max(a.seq),0) AS INTEGER) AS through_seq FROM work_item_actions a WHERE a.work_item_id=sqlc.arg(work_item_id);
-- name: WorkHistoryActionsPage :many
SELECT CAST(a.seq AS INTEGER) AS seq,CAST(json_object('id',a.id,'kind',a.kind,'status',coalesce(d.status,a.status),'taskId',a.task_id,'deliveryId',a.delivery_id,'body',a.body,'createdAt',a.created_at) AS TEXT) AS value
FROM work_item_actions a LEFT JOIN automation_deliveries d ON d.id=a.delivery_id
WHERE a.work_item_id=sqlc.arg(work_item_id) AND a.seq>sqlc.arg(after_seq) AND a.seq<=sqlc.arg(through_seq)
ORDER BY a.seq LIMIT sqlc.arg(result_limit);
