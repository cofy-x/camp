-- name: CreateTask :exec
INSERT INTO collaboration_tasks(id,room_id,requested_by_user_id,status,revision,snapshot,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?);
-- name: GetTask :one
SELECT snapshot FROM collaboration_tasks WHERE id=?;
-- name: SaveTask :exec
UPDATE collaboration_tasks SET status=?,revision=?,snapshot=?,updated_at=? WHERE id=?;
-- name: ListRoomTasks :many
SELECT snapshot FROM collaboration_tasks WHERE room_id=? ORDER BY created_at,id;
-- name: ListOpenTasks :many
SELECT snapshot FROM collaboration_tasks WHERE status NOT IN ('completed','cancelled','interrupted') ORDER BY created_at,id;
-- name: ListTaskRuns :many
SELECT snapshot FROM runs WHERE json_extract(snapshot,'$.taskId')=? ORDER BY created_at,id;
-- name: InsertTaskInput :exec
INSERT INTO task_inputs(id,task_id,seq,content_bytes,value) VALUES(?,?,?,?,?);
-- name: TaskInputRange :many
SELECT value FROM task_inputs WHERE task_id=sqlc.arg(task_id) AND seq>sqlc.arg(after_seq) AND seq<=sqlc.arg(through_seq) ORDER BY seq LIMIT sqlc.arg(page_limit);
-- name: TaskRunsPage :many
SELECT id,status,created_at,updated_at,agent_id,
 CAST(coalesce(json_extract(snapshot,'$.agentSnapshot.name'),'') AS TEXT) AS agent_name,
 CAST(coalesce(json_extract(snapshot,'$.runnerName'),'') AS TEXT) AS runner_name,
 CAST(coalesce(json_extract(snapshot,'$.checksDeferred'),0) AS INTEGER) AS checks_deferred
FROM runs WHERE json_extract(snapshot,'$.taskId')=sqlc.arg(task_id)
 AND (created_at>sqlc.arg(after_created_at) OR (created_at=sqlc.arg(after_created_at) AND id>sqlc.arg(after_id)))
ORDER BY created_at,id LIMIT sqlc.arg(page_limit);
-- name: GetTaskRequest :one
SELECT * FROM task_requests WHERE user_id=? AND scope=? AND request_id=?;
-- name: SaveTaskRequest :exec
INSERT INTO task_requests(user_id,scope,request_id,request_hash,result) VALUES(?,?,?,?,?);
-- name: SetPublicationTask :exec
UPDATE resource_publications SET task_id=? WHERE id=?;
-- name: FirstRunnerSuccess :one
SELECT r.id,r.updated_at FROM runs r JOIN run_attempts a ON a.run_id=r.id
WHERE a.runner_id=? AND a.status='finished' AND r.status='succeeded'
 AND json_extract(r.snapshot,'$.executor')='native'
 AND a.result_id<>'' AND length(a.result_hash)=64
 AND json_extract(a.result,'$.disposition')='committed'
 AND json_extract(a.result,'$.status')='succeeded'
 AND json_extract(a.result,'$.cleanupConfirmed')=1
ORDER BY r.updated_at,r.id LIMIT 1;
-- name: FirstRunnerUnrecordedSuccess :one
SELECT r.id,r.updated_at FROM runs r JOIN run_attempts a ON a.run_id=r.id
WHERE a.runner_id=? AND a.status='finished' AND r.status='succeeded'
 AND coalesce(json_extract(r.snapshot,'$.executor'),'')=''
 AND a.result_id<>'' AND length(a.result_hash)=64
 AND json_extract(a.result,'$.disposition')='committed'
 AND json_extract(a.result,'$.status')='succeeded'
ORDER BY r.updated_at,r.id LIMIT 1;
