-- name: CreateFeishuSource :exec
INSERT INTO feishu_sources(id,room_id,app_id,tenant_key,chat_id,enabled,revision,credential_ref,created_by,created_at,updated_at) VALUES(?,?,?,?,?,1,1,?,?,?,?);
-- name: GetFeishuSource :one
SELECT * FROM feishu_sources WHERE id=?;
-- name: ListFeishuSources :many
SELECT * FROM feishu_sources WHERE room_id=? ORDER BY created_at,id;
-- name: UpdateFeishuSource :exec
UPDATE feishu_sources SET enabled=?,credential_ref=?,revision=revision+1,updated_at=? WHERE id=?;
-- name: GetFeishuDelivery :one
SELECT * FROM feishu_deliveries WHERE source_id=? AND delivery_id=?;
-- name: InsertFeishuDelivery :exec
INSERT INTO feishu_deliveries(source_id,delivery_id,fingerprint,event_id) VALUES(?,?,?,?);
-- name: GetFeishuFingerprint :one
SELECT id FROM feishu_events WHERE source_id=? AND fingerprint=?;
-- name: InsertFeishuEvent :exec
INSERT INTO feishu_events(id,source_id,room_id,source_revision,credential_ref,fingerprint,value,payload,received_at) VALUES(?,?,?,?,?,?,?,?,?);
-- name: GetFeishuEvent :one
SELECT seq,id,source_id,room_id,source_revision,fingerprint,value,received_at FROM feishu_events WHERE id=?;
-- name: GetFeishuPayload :one
SELECT payload FROM feishu_events WHERE id=?;
-- name: GetFeishuRequest :one
SELECT * FROM feishu_requests WHERE user_id=? AND request_id=?;
-- name: SaveFeishuRequest :exec
INSERT INTO feishu_requests(user_id,request_id,request_hash,result) VALUES(?,?,?,?);
