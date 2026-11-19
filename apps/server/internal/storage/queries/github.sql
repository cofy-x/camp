-- name: CreateGitHubSource :exec
INSERT INTO github_sources(id,room_id,repository,enabled,revision,secret_ref,created_by,created_at,updated_at) VALUES(?,?,?,1,1,?,?,?,?);
-- name: GetGitHubSource :one
SELECT * FROM github_sources WHERE id=?;
-- name: ListGitHubSources :many
SELECT * FROM github_sources WHERE room_id=? ORDER BY created_at,id;
-- name: UpdateGitHubSource :exec
UPDATE github_sources SET enabled=?,secret_ref=?,revision=revision+1,updated_at=? WHERE id=?;
-- name: PinGitHubRepository :exec
UPDATE github_sources SET repository_id=? WHERE id=? AND repository_id=0;
-- name: GetGitHubDelivery :one
SELECT * FROM github_deliveries WHERE source_id=? AND delivery_id=?;
-- name: InsertGitHubDelivery :exec
INSERT INTO github_deliveries(source_id,delivery_id,fingerprint,event_id) VALUES(?,?,?,?);
-- name: GitHubRecoveryDeliveries :many
SELECT CAST(requested.value AS TEXT) AS delivery_id,
    COALESCE(delivery.event_id,'') AS event_id,
    COALESCE(intent.prepared_at,'') AS prepared_at
FROM json_each(sqlc.arg(delivery_ids)) AS requested
LEFT JOIN github_recovery_deliveries intent
    ON intent.source_id=sqlc.arg(source_id) AND intent.delivery_id=requested.value
LEFT JOIN github_deliveries delivery
    ON delivery.source_id=sqlc.arg(source_id) AND delivery.delivery_id=requested.value;
-- name: HasGitHubRecoveryIntent :one
SELECT EXISTS(SELECT 1 FROM github_recovery_deliveries WHERE source_id=? AND delivery_id=?);
-- name: PrepareGitHubRecovery :execrows
INSERT INTO github_recovery_deliveries(source_id,delivery_id,prepared_by,prepared_at)
SELECT sqlc.arg(source_id),sqlc.arg(delivery_id),sqlc.arg(prepared_by),sqlc.arg(prepared_at)
WHERE NOT EXISTS(SELECT 1 FROM github_deliveries WHERE source_id=sqlc.arg(source_id) AND delivery_id=sqlc.arg(delivery_id))
ON CONFLICT DO NOTHING;
-- name: GetGitHubFingerprint :one
SELECT id FROM github_events WHERE source_id=? AND fingerprint=?;
-- name: InsertGitHubEvent :exec
INSERT INTO github_events(id,source_id,room_id,source_revision,fingerprint,disposition,value,payload,received_at) VALUES(?,?,?,?,?,?,?,?,?);
-- name: GetGitHubEvent :one
SELECT e.seq,e.id,e.source_id,e.room_id,e.source_revision,e.fingerprint,e.disposition,e.value,e.received_at,coalesce(d.outcome,e.disposition) AS status,coalesce(d.actor_id,'') AS decided_by,coalesce(d.task_id,'') AS task_id,coalesce(d.reason,'') AS decision_reason,coalesce(d.created_at,'') AS decided_at
FROM github_events e LEFT JOIN github_event_decisions d ON d.event_id=e.id WHERE e.id=?;
-- name: GetGitHubPayload :one
SELECT payload FROM github_events WHERE id=?;
-- name: RecentGitHubSourceProof :many
WITH recent AS (
 SELECT g.id, g.received_at, g.disposition, g.seq FROM github_events g
 WHERE g.source_id=sqlc.arg(source_id) AND g.source_revision=sqlc.arg(source_revision)
 ORDER BY g.seq DESC LIMIT 20
)
SELECT recent.received_at, recent.disposition,
 CAST(coalesce(a.seq,0) AS INTEGER) AS event_seq,
 CAST(coalesce(a.kind,'') AS TEXT) AS kind,
 CAST(coalesce(a.actor_kind,'') AS TEXT) AS actor_kind,
 CAST(coalesce(a.actor_id,'') AS TEXT) AS actor_id,
 CAST(coalesce(a.status,'') AS TEXT) AS status
FROM recent LEFT JOIN automation_events a ON a.id='github:'||recent.id
ORDER BY recent.seq DESC;
-- name: GitHubEventFrontier :one
SELECT CAST(coalesce(max(seq),0) AS INTEGER) FROM github_events WHERE room_id=?;
-- name: GitHubEventsPage :many
SELECT e.id FROM github_events e LEFT JOIN github_event_decisions d ON d.event_id=e.id
WHERE e.room_id=sqlc.arg(room_id) AND e.seq>sqlc.arg(after_seq) AND e.seq<=sqlc.arg(through_seq)
AND (sqlc.arg(state)='all' OR coalesce(d.outcome,e.disposition)=sqlc.arg(state))
ORDER BY e.seq LIMIT sqlc.arg(page_limit);
-- name: DecideGitHubEvent :exec
INSERT INTO github_event_decisions(event_id,outcome,actor_id,task_id,reason,created_at) VALUES(?,?,?,?,?,?);
-- name: GetGitHubRequest :one
SELECT * FROM github_requests WHERE user_id=? AND request_id=?;
-- name: SaveGitHubRequest :exec
INSERT INTO github_requests(user_id,request_id,request_hash,result) VALUES(?,?,?,?);
