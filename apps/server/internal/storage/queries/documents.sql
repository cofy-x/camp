-- name: GetDocument :one
SELECT snapshot FROM documents WHERE id=?;
-- name: LatestDocument :one
SELECT snapshot FROM documents WHERE resource_id=? AND commit_id=? AND path=? ORDER BY seq DESC LIMIT 1;
-- name: LatestDocumentVersion :one
SELECT snapshot FROM documents WHERE resource_id=? AND commit_id=? AND path=? AND extractor_version=? ORDER BY seq DESC LIMIT 1;
-- name: CreateDocument :exec
INSERT INTO documents(id,resource_id,commit_id,path,extractor_version,status,snapshot,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?);
-- name: UpdateDocument :execrows
UPDATE documents SET status=@status,snapshot=@snapshot,updated_at=@updated_at WHERE id=@id AND status=@previous_status;
-- name: PendingDocuments :many
SELECT snapshot FROM documents WHERE status IN ('queued','extracting') ORDER BY seq;
-- name: DocumentRooms :many
SELECT id FROM rooms WHERE rooms.resource_id=@resource_id
UNION SELECT room_id FROM room_uploads WHERE room_uploads.resource_id=@resource_id
UNION SELECT room_id FROM runs WHERE runs.resource_id=@resource_id;
