-- name: InsertMemory :exec
INSERT INTO memories(id,room_id,author_user_id,revision,created_at) VALUES(?,?,?,1,?);
-- name: GetMemoryHead :one
SELECT * FROM memories WHERE id=?;
-- name: AdvanceMemory :execrows
UPDATE memories SET revision=revision+1 WHERE id=? AND revision=?;
-- name: InsertMemoryRevision :exec
INSERT INTO memory_revisions(memory_id,revision,state,content,value,created_at) VALUES(?,?,?,?,?,?);
-- name: GetMemoryRevision :one
SELECT r.*,m.room_id,m.author_user_id,m.revision AS current_revision
FROM memory_revisions r JOIN memories m ON m.id=r.memory_id
WHERE r.memory_id=? AND r.revision=?;
-- name: SearchMemories :many
SELECT r.value FROM memory_revisions r
JOIN memories m ON m.id=r.memory_id AND m.revision=r.revision
WHERE m.room_id=sqlc.arg(room_id) AND r.state='active'
 AND (sqlc.arg(search_text)='' OR instr(lower(r.content),lower(sqlc.arg(search_text)))>0)
ORDER BY r.created_at DESC,m.id LIMIT sqlc.arg(result_limit);
-- name: GetMemoryRequest :one
SELECT * FROM memory_requests WHERE user_id=? AND request_id=?;
-- name: InsertMemoryRequest :exec
INSERT INTO memory_requests(user_id,request_id,request_hash,result) VALUES(?,?,?,?);
