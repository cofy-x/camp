-- name: GetWorkItem :one
SELECT * FROM work_items WHERE id=?;
-- name: WorkItemActiveTask :one
SELECT CAST(coalesce(json_extract(value,'$.activeTaskId'),'') AS TEXT) FROM work_items WHERE id=?;
-- name: InsertWorkItem :one
INSERT INTO work_items(id,room_id,owner_user_id,policy_id,status,revision,value,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?,?) RETURNING seq;
-- name: SaveWorkItem :execrows
UPDATE work_items SET policy_id=?,status=?,revision=revision+1,value=?,updated_at=? WHERE id=? AND revision=?;
-- name: WorkItemsPage :many
SELECT * FROM work_items WHERE room_id=? AND seq>? ORDER BY seq LIMIT ?;
-- name: InsertWorkEvidence :execrows
INSERT INTO work_item_evidence(work_item_id,event_id,kind,created_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING;
-- name: WorkEvidence :many
SELECT * FROM work_item_evidence WHERE work_item_id=? ORDER BY created_at,event_id LIMIT 21;
-- name: WorkExecutionEvidence :many
SELECT * FROM work_item_evidence WHERE work_item_id=? ORDER BY seq LIMIT 1001;
-- name: WorkDecisions :many
SELECT * FROM work_item_decisions WHERE work_item_id=? AND seq>? ORDER BY seq DESC LIMIT ?;
-- name: WorkClosingDecision :one
SELECT * FROM work_item_decisions WHERE work_item_id=sqlc.arg(work_item_id)
 AND ((sqlc.arg(status)='completed' AND json_extract(value,'$.kind')='complete')
 OR (sqlc.arg(status)='cancelled' AND json_extract(value,'$.kind') IN ('stop','cancel')))
ORDER BY seq DESC LIMIT 1;
-- name: LegacyWorkRecovery :one
SELECT d.value AS decision,CAST(coalesce(t.status,'') AS TEXT) AS task_status,
 CAST(coalesce(p.source_id,'') AS TEXT) AS source_id,
 CAST(coalesce(json_extract(a.assessment,'$.outcome'),'') AS TEXT) AS assessment_outcome,
 CAST(coalesce(json_extract(a.assessment,'$.workItemId'),'') AS TEXT) AS assessed_work_item_id,
 CAST(EXISTS(SELECT 1 FROM json_each(a.candidate,'$.events')
  WHERE json_extract(value,'$.workItemId')=d.work_item_id) AS INTEGER) AS targeted
FROM work_item_decisions d
JOIN work_items w ON w.id=d.work_item_id
LEFT JOIN work_item_tasks linked ON linked.work_item_id=d.work_item_id AND linked.task_id=json_extract(d.value,'$.taskId')
LEFT JOIN collaboration_tasks t ON t.id=linked.task_id
LEFT JOIN automation_policies p ON p.id=w.policy_id AND p.room_id=w.room_id AND p.owner_user_id=w.owner_user_id
LEFT JOIN automation_evaluations a ON a.id=json_extract(d.value,'$.evaluationId')
 AND a.status='needs_attention'
 AND json_extract(d.value,'$.actorKind')='system' AND json_extract(d.value,'$.reason')='work_budget_exhausted'
WHERE d.work_item_id=? AND json_extract(d.value,'$.kind') IN ('request_judgment','result','wait','execute','accept','complete','cancel','stop','answer','answer_continue','create','delegate')
ORDER BY d.seq DESC LIMIT 1;
-- name: InsertWorkDecision :exec
INSERT INTO work_item_decisions(id,work_item_id,value) VALUES(?,?,?);
-- name: WorkTasks :many
SELECT w.task_id,t.status,CAST(coalesce(json_extract(t.snapshot,'$.summary'),'') AS TEXT) AS summary,w.created_at FROM work_item_tasks w JOIN collaboration_tasks t ON t.id=w.task_id WHERE w.work_item_id=? ORDER BY w.created_at DESC,w.task_id LIMIT 21;
-- name: WorkTimers :many
SELECT * FROM work_item_timers WHERE work_item_id=? ORDER BY created_at DESC,id LIMIT 21;
-- name: PendingWorkTimer :one
SELECT * FROM work_item_timers WHERE work_item_id=? AND status='pending' ORDER BY due_at,id LIMIT 1;
-- name: WorkActions :many
SELECT a.*,CAST(coalesce(d.status,a.status) AS TEXT) AS current_status FROM work_item_actions a LEFT JOIN automation_deliveries d ON d.id=a.delivery_id WHERE a.work_item_id=? ORDER BY a.created_at DESC,a.id LIMIT 21;
-- name: WorkWatches :many
SELECT * FROM work_item_watches WHERE work_item_id=? ORDER BY source_id,thread_key LIMIT 33;
-- name: GetWorkRequest :one
SELECT * FROM work_item_requests WHERE user_id=? AND request_id=?;
-- name: SaveWorkRequest :exec
INSERT INTO work_item_requests(user_id,request_id,request_hash,result,cancelled) VALUES(?,?,?,?,?);
-- name: WorkItemForTask :one
SELECT * FROM work_item_tasks WHERE task_id=?;
-- name: WorkItemForEvent :many
SELECT w.* FROM work_items w JOIN work_item_evidence e ON e.work_item_id=w.id WHERE e.event_id=? ORDER BY w.seq LIMIT 21;
-- name: WatchingWorkItems :many
SELECT w.* FROM work_items w JOIN work_item_watches s ON s.work_item_id=w.id WHERE s.source_id=? AND s.thread_key=? AND s.enabled=1 AND w.status NOT IN ('completed','cancelled') ORDER BY w.seq;
-- name: DueWorkTimers :many
SELECT * FROM work_item_timers WHERE status='pending' AND due_at<=? ORDER BY due_at,id LIMIT 100;
-- name: CancelWorkTimers :exec
UPDATE work_item_timers SET status='cancelled' WHERE work_item_id=? AND status='pending';
-- name: RetireWorkTimerFollowUps :many
UPDATE automation_events
SET status='context',reason=sqlc.arg(reason)
WHERE room_id=sqlc.arg(room_id) AND source_id='timer' AND thread_key=sqlc.arg(thread_key) AND kind='timer.fired'
 AND (status='pending' OR (status='evaluating' AND evaluation_id<>sqlc.arg(current_evaluation_id)))
RETURNING id;
-- name: InsertWorkTimer :exec
INSERT INTO work_item_timers(id,work_item_id,due_at,condition,status,created_at) VALUES(?,?,?,?,'pending',?);
-- name: FireWorkTimer :exec
UPDATE work_item_timers SET status='fired',event_id=? WHERE id=? AND status='pending';
-- name: InsertWorkTask :exec
INSERT INTO work_item_tasks(work_item_id,task_id,reserved_requests,created_at) VALUES(?,?,?,?);
-- name: WorkTaskResult :exec
UPDATE work_item_tasks SET result_event_id=? WHERE task_id=? AND result_event_id='';
-- name: ReleaseWorkTaskReservation :execrows
UPDATE work_item_tasks SET reservation_released=1 WHERE task_id=? AND reservation_released=0;
-- name: SaveWorkWatch :exec
INSERT INTO work_item_watches(work_item_id,source_id,thread_key,enabled) VALUES(?,?,?,?) ON CONFLICT(work_item_id,source_id,thread_key) DO UPDATE SET enabled=excluded.enabled;
-- name: InsertWorkAction :exec
INSERT INTO work_item_actions(id,work_item_id,kind,status,task_id,delivery_id,body,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING;
-- name: CreateWorkInbox :exec
INSERT INTO inbox(id,user_id,kind,room_id,message_id,work_item_id,created_at) VALUES(?,?,?,?,?,?,?);
-- name: WorkInternalEvent :one
SELECT CAST(count(*) AS INTEGER) FROM automation_events e
WHERE e.id=? AND (
 EXISTS(SELECT 1 FROM work_item_timers t WHERE t.event_id=e.id AND t.status='fired')
 OR EXISTS(SELECT 1 FROM work_item_tasks t WHERE t.result_event_id=e.id)
 OR EXISTS(SELECT 1 FROM work_action_receipts a WHERE a.event_id=e.id)
 OR EXISTS(SELECT 1 FROM github_delivery_receipts a WHERE a.event_id=e.id)
 OR (e.source_id='workitem' AND e.kind='workitem.evidence' AND e.actor_kind='system' AND e.actor_id='workitem'
  AND EXISTS(
   SELECT 1 FROM work_item_evidence own
   JOIN automation_events original ON original.id=own.event_id AND original.room_id=e.room_id
   JOIN work_item_watches watch ON watch.work_item_id=own.work_item_id AND watch.source_id=original.source_id AND watch.thread_key=original.thread_key AND watch.enabled=1
   WHERE own.work_item_id=json_extract(e.value,'$.workItemId') AND original.id=json_extract(e.value,'$.causationId')
  ))
 OR (e.source_id='workitem' AND e.kind='workitem.answer' AND e.actor_kind='user'
  AND EXISTS(
   SELECT 1 FROM work_item_decisions d
   WHERE d.work_item_id=json_extract(e.value,'$.workItemId') AND json_extract(d.value,'$.kind')='evidence'
    AND json_extract(d.value,'$.actorKind')='user' AND json_extract(d.value,'$.actorId')=e.actor_id
    AND EXISTS(SELECT 1 FROM json_each(json_extract(d.value,'$.evidenceIds')) ids WHERE ids.value=e.id)
  ))
);
-- name: WorkEvidenceExists :one
SELECT count(*) FROM work_item_evidence WHERE work_item_id=? AND event_id=?;
-- name: WorkObservedConversation :one
SELECT CAST(EXISTS(SELECT 1 FROM work_item_evidence w JOIN automation_events e ON e.id=w.event_id WHERE w.work_item_id=sqlc.arg(work_item_id) AND e.source_id=sqlc.arg(source_id) AND e.thread_key=sqlc.arg(thread_key)) AS INTEGER);
-- name: GetWorkWatch :one
SELECT * FROM work_item_watches WHERE work_item_id=? AND source_id=? AND thread_key=?;
-- name: SetWorkTaskWatch :exec
INSERT INTO automation_subscriptions(id,task_id,source_id,thread_key,enabled,revision) VALUES(?,?,?,?,?,1)
ON CONFLICT(task_id,source_id,thread_key) DO UPDATE SET enabled=excluded.enabled,revision=automation_subscriptions.revision+1;
