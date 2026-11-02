-- name: InsertOrchestrationRecord :exec
INSERT INTO orchestration_records(source,event_id,room_id,message_id,task_id,value)
VALUES(?,?,?,?,?,?);

-- name: OrchestrationFrontier :one
SELECT CAST(COALESCE(MAX(seq),0) AS INTEGER) FROM orchestration_records WHERE room_id=?;

-- name: OrchestrationPage :many
SELECT seq,value FROM orchestration_records
WHERE room_id=@room_id AND seq>@after_seq AND seq<=@through_seq
ORDER BY seq LIMIT @page_limit;
