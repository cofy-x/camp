-- name: GetDocument :one
SELECT snapshot FROM documents WHERE id=?;
-- name: LatestDocument :one
SELECT snapshot FROM documents WHERE resource_id=? AND commit_id=? AND path=? ORDER BY seq DESC LIMIT 1;
-- name: LatestReadyDocument :one
SELECT snapshot FROM documents WHERE resource_id=? AND commit_id=? AND path=? AND status='ready' ORDER BY seq DESC LIMIT 1;
