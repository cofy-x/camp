-- name: WorkExecutionView :one
SELECT CAST(json_object(
 'taskId',t.id,'status',t.status,'revision',t.revision,
 'summary',coalesce(json_extract(t.snapshot,'$.summary'),''),
 'error',coalesce(json_extract(t.snapshot,'$.error'),''),
 'activeRunId',coalesce(json_extract(t.snapshot,'$.activeRunId'),''),
 'updatedAt',t.updated_at,
 'currentAgentName',coalesce((SELECT json_extract(p.value,'$.agent.name')
  FROM json_each(t.snapshot,'$.participants') p
  WHERE json_extract(p.value,'$.agent.id')=json_extract(t.snapshot,'$.currentAgentId') LIMIT 1),''),
 'question',CASE WHEN json_type(t.snapshot,'$.question')='object' THEN json_object(
  'id',coalesce(json_extract(t.snapshot,'$.question.id'),''),
  'text',coalesce(json_extract(t.snapshot,'$.question.text'),''),
  'options',json(CASE WHEN json_type(t.snapshot,'$.question.options')='array' THEN json_extract(t.snapshot,'$.question.options') ELSE '[]' END),
  'answer',coalesce(json_extract(t.snapshot,'$.question.answer'),'')
 ) ELSE NULL END
) AS TEXT) AS metadata
FROM collaboration_tasks t WHERE t.id=CASE WHEN CAST(sqlc.arg(active_task_id) AS TEXT)<>'' THEN sqlc.arg(active_task_id)
 ELSE (SELECT task_id FROM work_item_tasks WHERE work_item_id=sqlc.arg(work_item_id) ORDER BY seq DESC LIMIT 1) END;

-- name: WorkResultView :one
WITH candidate AS (
 SELECT t.id,t.snapshot FROM work_item_tasks w JOIN collaboration_tasks t ON t.id=w.task_id
 WHERE w.work_item_id=sqlc.arg(work_item_id) AND (coalesce(json_extract(t.snapshot,'$.publishedCommit'),'')<>'' OR (
  coalesce(json_extract(t.snapshot,'$.checkpointRevision'),0)>0 AND EXISTS(
   SELECT 1 FROM runs r WHERE json_extract(r.snapshot,'$.taskId')=t.id AND r.status='succeeded'
    AND json_extract(r.snapshot,'$.resultCommit')=json_extract(t.snapshot,'$.checkpointCommit')
    AND coalesce(json_extract(r.snapshot,'$.resultCommit'),'')<>''
   ))) ORDER BY w.seq DESC LIMIT 1
), saved AS (
 SELECT id,snapshot,
  CASE WHEN coalesce(json_extract(snapshot,'$.publishedCommit'),'')<>'' THEN json_extract(snapshot,'$.publishedCommit') ELSE coalesce(json_extract(snapshot,'$.checkpointCommit'),'') END AS result_commit
 FROM candidate
), result AS (
 SELECT t.*,r.snapshot AS run_snapshot FROM saved t LEFT JOIN runs r ON r.id=coalesce(
  (SELECT id FROM runs WHERE id=json_extract(t.snapshot,'$.publicationRunId') AND status='succeeded'
   AND json_extract(snapshot,'$.taskId')=t.id AND coalesce(nullif(json_extract(snapshot,'$.resultCommit'),''),json_extract(snapshot,'$.baselineCommit'))=t.result_commit),
  (SELECT id FROM runs WHERE json_extract(snapshot,'$.taskId')=t.id AND json_extract(snapshot,'$.resultCommit')=t.result_commit
   AND status='succeeded' ORDER BY created_at DESC,id DESC LIMIT 1)
 )
), changed_files AS (
 SELECT json_extract(f.value,'$.path') AS path,json_extract(f.value,'$.status') AS status
 FROM json_each((SELECT run_snapshot FROM result),'$.files') f
), artifacts AS (
 SELECT DISTINCT CAST(value AS TEXT) AS path FROM json_each((SELECT run_snapshot FROM result),'$.intent.artifactPaths')
 WHERE type='text' AND value<>''
), visible_files AS (
 SELECT a.path,coalesce(c.status,'saved') AS status,0 AS priority
 FROM artifacts a LEFT JOIN changed_files c ON c.path=a.path
 UNION ALL
 SELECT c.path,c.status,1 FROM changed_files c WHERE NOT EXISTS(SELECT 1 FROM artifacts a WHERE a.path=c.path)
)
SELECT CAST(json_object(
 'taskId',t.id,'summary',coalesce(json_extract(t.snapshot,'$.summary'),''),
 'resourceId',coalesce(json_extract(t.snapshot,'$.resourceId'),''),'commit',t.result_commit,
 'published',json(CASE WHEN coalesce(json_extract(t.snapshot,'$.publishedCommit'),'')<>'' THEN 'true' ELSE 'false' END),
 'filesLimited',json(CASE WHEN (SELECT count(*) FROM visible_files)>20 THEN 'true' ELSE 'false' END),
 'files',json((SELECT json_group_array(json_object('path',path,'status',status))
  FROM (SELECT path,status FROM visible_files ORDER BY priority,path LIMIT 20)))
) AS TEXT) AS metadata
FROM result t;

-- name: WorkItemsStatePage :many
SELECT w.* FROM work_items w
LEFT JOIN collaboration_tasks t ON t.id=json_extract(w.value,'$.activeTaskId')
WHERE w.room_id=sqlc.arg(room_id) AND (sqlc.arg(before_seq)=0 OR w.seq<sqlc.arg(before_seq))
AND (sqlc.arg(state)='all' OR CASE
 WHEN EXISTS(SELECT 1 FROM personal_attention a WHERE a.source='work_item' AND a.entity_id=w.id) THEN 'needs_attention'
 WHEN w.status IN ('completed','cancelled') THEN 'done'
 WHEN w.status='stopping' OR coalesce(json_extract(w.value,'$.stopRequested'),0)=1 THEN 'active'
 WHEN w.status IN ('open','needs_attention','waiting_for_person') OR t.status IN ('waiting_for_person','needs_attention','interrupted') THEN 'needs_attention'
 ELSE 'active' END=sqlc.arg(state))
ORDER BY w.seq DESC LIMIT sqlc.arg(page_limit);
