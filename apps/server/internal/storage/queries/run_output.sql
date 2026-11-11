-- name: InitRunProgress :exec
INSERT INTO run_progress(run_id,updated_at,output_bytes,output_prefix) VALUES(?,?,0,'');
-- name: GetRunProgress :one
SELECT * FROM run_progress WHERE run_id=?;
-- name: SaveRunProgress :exec
UPDATE run_progress SET updated_at=?,output_bytes=?,output_prefix=? WHERE run_id=?;
-- name: FirstRunOutputChunk :one
SELECT seq,text FROM run_output_chunks WHERE run_id=? ORDER BY seq LIMIT 1;
-- name: LastRunOutputChunk :one
SELECT seq,text FROM run_output_chunks WHERE run_id=? ORDER BY seq DESC LIMIT 1;
-- name: InsertRunOutputChunk :exec
INSERT INTO run_output_chunks(run_id,seq,text) VALUES(?,?,?);
-- name: SaveRunOutputChunk :exec
UPDATE run_output_chunks SET text=? WHERE run_id=? AND seq=?;
-- name: DeleteRunOutputChunk :exec
DELETE FROM run_output_chunks WHERE run_id=? AND seq=?;
-- name: ClearRunOutput :exec
DELETE FROM run_output_chunks WHERE run_id=?;
-- name: SetRunStreamStatus :exec
UPDATE runs SET status=?,updated_at=? WHERE id=?;
-- name: GetRunAttemptState :one
SELECT CAST(json_object('id',id,'roomId',room_id,'resourceId',resource_id,'status',status,
 'attemptId',coalesce(json_extract(snapshot,'$.attemptId'),''),
 'runnerId',coalesce(json_extract(snapshot,'$.runnerId'),''),
 'taskId',coalesce(json_extract(snapshot,'$.taskId'),''),
 'inputCommit',coalesce(json_extract(snapshot,'$.inputCommit'),''),
 'baselineCommit',coalesce(json_extract(snapshot,'$.baselineCommit'),''),
 'taskInputProtocol',coalesce(json_extract(snapshot,'$.taskInputProtocol'),0),
 'agentSnapshot',json_object('access',coalesce(json_extract(snapshot,'$.agentSnapshot.access'),''))) AS TEXT)
FROM runs WHERE id=?;
-- name: GetTaskAttemptState :one
SELECT CAST(json_object('id',id,'status',status,
 'activeRunId',coalesce(json_extract(snapshot,'$.activeRunId'),''),
 'activeAttemptId',coalesce(json_extract(snapshot,'$.activeAttemptId'),''),
 'draftFence',coalesce(json_extract(snapshot,'$.draftFence'),0),
 'checkpointCommit',coalesce(json_extract(snapshot,'$.checkpointCommit'),'')) AS TEXT)
FROM collaboration_tasks WHERE id=?;
-- name: GetRunStreamIdentity :one
SELECT room_id,status FROM runs WHERE id=?;
-- name: GetAttemptStreamState :one
SELECT id,run_id,runner_id,status,lease_expires_at,fence,cancel_requested FROM run_attempts WHERE id=?;
