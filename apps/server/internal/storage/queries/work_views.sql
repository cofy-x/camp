-- name: LatestWorkTask :one
SELECT t.snapshot FROM work_item_tasks w JOIN collaboration_tasks t ON t.id=w.task_id
WHERE w.work_item_id=? ORDER BY w.seq DESC LIMIT 1;

-- name: LatestWorkResultTask :one
SELECT t.snapshot FROM work_item_tasks w JOIN collaboration_tasks t ON t.id=w.task_id
WHERE w.work_item_id=? AND (coalesce(json_extract(t.snapshot,'$.publishedCommit'),'')<>'' OR (
 coalesce(json_extract(t.snapshot,'$.checkpointRevision'),0)>0 AND EXISTS(
  SELECT 1 FROM runs r WHERE json_extract(r.snapshot,'$.taskId')=t.id AND r.status='succeeded'
   AND json_extract(r.snapshot,'$.resultCommit')=json_extract(t.snapshot,'$.checkpointCommit')
   AND coalesce(json_extract(r.snapshot,'$.resultCommit'),'')<>''
  ))) ORDER BY w.seq DESC LIMIT 1;

-- name: WorkResultRun :one
SELECT r.snapshot FROM runs r WHERE json_extract(r.snapshot,'$.taskId')=sqlc.arg(task_id) AND json_extract(r.snapshot,'$.resultCommit')=sqlc.arg(commit_id)
AND r.status='succeeded' ORDER BY r.created_at DESC,r.id DESC LIMIT 1;

-- name: WorkItemsStatePage :many
SELECT w.* FROM work_items w
LEFT JOIN collaboration_tasks t ON t.id=json_extract(w.value,'$.activeTaskId')
WHERE w.room_id=sqlc.arg(room_id) AND (sqlc.arg(before_seq)=0 OR w.seq<sqlc.arg(before_seq))
AND (sqlc.arg(state)='all' OR CASE
 WHEN w.status IN ('completed','cancelled') THEN 'done'
 WHEN w.status='stopping' OR coalesce(json_extract(w.value,'$.stopRequested'),0)=1 THEN 'active'
 WHEN w.status IN ('open','needs_attention','waiting_for_person') OR t.status IN ('waiting_for_person','needs_attention','interrupted') THEN 'needs_attention'
 ELSE 'active' END=sqlc.arg(state))
ORDER BY w.seq DESC LIMIT sqlc.arg(page_limit);
