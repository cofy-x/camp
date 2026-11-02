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
-- name: GetGitHubFingerprint :one
SELECT id FROM github_events WHERE source_id=? AND fingerprint=?;
-- name: InsertGitHubEvent :exec
INSERT INTO github_events(id,source_id,room_id,source_revision,fingerprint,disposition,value,payload,received_at) VALUES(?,?,?,?,?,?,?,?,?);
-- name: GetGitHubEvent :one
SELECT e.seq,e.id,e.source_id,e.room_id,e.source_revision,e.fingerprint,e.disposition,e.value,e.received_at,coalesce(d.outcome,e.disposition) AS status,coalesce(d.actor_id,'') AS decided_by,coalesce(d.task_id,'') AS task_id,coalesce(d.reason,'') AS decision_reason,coalesce(d.created_at,'') AS decided_at
FROM github_events e LEFT JOIN github_event_decisions d ON d.event_id=e.id WHERE e.id=?;
-- name: GetGitHubPayload :one
SELECT payload FROM github_events WHERE id=?;
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
