-- name: ListRunners :many
SELECT * FROM runners ORDER BY created_at,id;
-- name: GetRunner :one
SELECT * FROM runners WHERE id=?;
-- name: GetRunnerByCredential :one
SELECT * FROM runners WHERE credential_hash=?;
-- name: CreateRunner :exec
INSERT INTO runners(id,name,credential_hash,storage_id,created_at,updated_at) VALUES(?,?,?,?,?,?);
-- name: UpdateRunner :exec
UPDATE runners SET name=?,enabled=?,revoked=?,updated_at=? WHERE id=?;
-- name: ConnectRunner :exec
UPDATE runners SET runtime=?,slots=?,capabilities=?,sources=?,connection_generation=connection_generation+1,connected_at=?,last_seen_at=?,updated_at=? WHERE id=?;
-- name: TouchRunner :execrows
UPDATE runners SET last_seen_at=? WHERE id=? AND connection_generation=? AND enabled=1 AND revoked=0;
-- name: CreateRunnerEnrollment :exec
INSERT INTO runner_enrollments(id,name,token_hash,expires_at,created_at) VALUES(?,?,?,?,?);
-- name: GetRunnerEnrollment :one
SELECT * FROM runner_enrollments WHERE token_hash=?;
-- name: ListRunnerEnrollments :many
SELECT * FROM runner_enrollments ORDER BY created_at,id;
-- name: RevokeRunnerEnrollment :execrows
UPDATE runner_enrollments SET revoked=1 WHERE id=?;
-- name: ConsumeRunnerEnrollment :exec
UPDATE runner_enrollments SET registration_id=?,runner_id=? WHERE id=?;
-- name: SetRoomRunner :execrows
UPDATE rooms SET runner_id=?,runner_revision=runner_revision+1,updated_at=? WHERE id=? AND runner_revision=?;
-- name: GetResourceLease :one
SELECT * FROM resource_writer_leases WHERE resource_id=?;
-- name: SetResourceLease :exec
INSERT INTO resource_writer_leases(resource_id,owner,fence,expires_at) VALUES(?,?,?,?)
ON CONFLICT(resource_id) DO UPDATE SET owner=excluded.owner,fence=excluded.fence,expires_at=excluded.expires_at;
-- name: ReleaseResourceLease :execrows
UPDATE resource_writer_leases SET owner='',expires_at='' WHERE resource_id=? AND owner=? AND fence=?;
-- name: RenewResourceLease :execrows
UPDATE resource_writer_leases SET expires_at=? WHERE resource_id=? AND owner=? AND fence=? AND expires_at>@now;
-- name: CreateAttempt :exec
INSERT INTO run_attempts(id,run_id,runner_id,fence,command_id,status,lease_expires_at,deadline,capability_hash,created_at,updated_at) VALUES(?,?,?,?,?,'dispatching',?,?,?,?,?);
-- name: SetAttemptCapability :exec
UPDATE run_attempts SET capability_hash=? WHERE id=?;
-- name: GetAttempt :one
SELECT * FROM run_attempts WHERE id=?;
-- name: GetRunAttempt :one
SELECT * FROM run_attempts WHERE run_id=?;
-- name: GetAttemptByCapability :one
SELECT * FROM run_attempts WHERE capability_hash=?;
-- name: SetAttemptModelInFlight :exec
UPDATE run_attempts SET model_in_flight=? WHERE id=?;
-- name: ListActiveAttempts :many
SELECT * FROM run_attempts WHERE status<>'finished' ORDER BY created_at,id;
-- name: ListRunnerAttempts :many
SELECT * FROM run_attempts WHERE runner_id=? AND status<>'finished' ORDER BY created_at,id;
-- name: CountRunnerOperations :one
SELECT CAST(
  (SELECT count(*) FROM run_attempts AS a WHERE a.runner_id=@runner_id AND a.status<>'finished') +
  (SELECT count(*) FROM source_imports AS i WHERE i.runner_id=@runner_id AND i.status IN ('capturing','uploading','cancelling'))
AS INTEGER);
-- name: SaveAttemptInput :exec
UPDATE run_attempts SET input_digest=?,input_path=?,updated_at=? WHERE id=?;
-- name: SaveAttemptArtifact :exec
UPDATE run_attempts SET artifact_digest=?,artifact_path=?,updated_at=? WHERE id=?;
-- name: RenewAttempt :exec
UPDATE run_attempts SET lease_expires_at=?,updated_at=? WHERE id=?;
-- name: SetAttemptStatus :exec
UPDATE run_attempts SET status=?,upload_expires_at=?,updated_at=? WHERE id=?;
-- name: CancelAttempt :exec
UPDATE run_attempts SET cancel_requested=1,updated_at=? WHERE id=?;
-- name: FinishAttempt :exec
UPDATE run_attempts SET status='finished',result_hash=?,result_id=?,result=?,updated_at=? WHERE id=?;
-- name: CreateRunnerCommand :exec
INSERT INTO runner_commands(id,runner_id,operation_id,kind,payload,payload_hash,created_at) VALUES(?,?,?,?,?,?,?);
-- name: GetRunnerCommand :one
SELECT * FROM runner_commands WHERE operation_id=? AND kind=?;
-- name: ListRunnerCommands :many
SELECT * FROM runner_commands WHERE runner_id=? AND acknowledged=0 ORDER BY created_at,id;
-- name: AcknowledgeRunnerCommand :execrows
UPDATE runner_commands SET acknowledged=1 WHERE id=? AND runner_id=?;
-- name: AcknowledgeOperationCommands :exec
UPDATE runner_commands SET acknowledged=1 WHERE operation_id=?;
-- name: GetRunnerEventReceipt :one
SELECT * FROM runner_event_receipts WHERE operation_id=? AND sequence=?;
-- name: CreateRunnerEventReceipt :exec
INSERT INTO runner_event_receipts(operation_id,sequence,payload_hash,kind,text,created_at) VALUES(?,?,?,?,?,?);
-- name: CreateSourceImport :exec
INSERT INTO source_imports(id,runner_id,requested_by_user_id,request_id,request_hash,source,source_revision,source_alias,room_name,resource_key,command_id,status,expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,'queued',?,?,?);
-- name: GetSourceImport :one
SELECT * FROM source_imports WHERE id=?;
-- name: GetSourceImportRequest :one
SELECT * FROM source_imports WHERE requested_by_user_id=? AND request_id=?;
-- name: ListSourceImports :many
SELECT * FROM source_imports ORDER BY created_at DESC,id DESC LIMIT 100;
-- name: ListActiveSourceImports :many
SELECT * FROM source_imports WHERE status IN ('queued','capturing','uploading','cancelling') ORDER BY created_at,id;
-- name: SaveSourceImport :exec
UPDATE source_imports SET status=?,expires_at=?,room_id=?,resource_id=?,artifact_digest=?,artifact_path=?,error=?,updated_at=? WHERE id=?;
-- name: FinishSourceImport :exec
UPDATE source_imports SET status=?,room_id=?,resource_id=?,artifact_digest=?,commit_id=?,completion_id=?,result_hash=?,error=?,updated_at=? WHERE id=?;
-- name: GetResourceSourceImport :one
SELECT * FROM source_imports WHERE resource_id=? AND status='succeeded';
-- name: NextRunnerEventSequence :one
SELECT CAST(COALESCE(MAX(sequence),0)+1 AS INTEGER) FROM runner_event_receipts WHERE operation_id=?;
-- name: SetRunnerConnectionIssue :exec
UPDATE runners SET capabilities=?,updated_at=? WHERE id=?;
