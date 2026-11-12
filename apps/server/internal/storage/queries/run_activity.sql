-- name: RunEventFrontier :one
SELECT CAST(COALESCE(MAX(seq),0) AS INTEGER) FROM run_events WHERE run_id=?;

-- name: RunActivityBefore :many
SELECT * FROM run_events WHERE run_id=sqlc.arg(run_id)
 AND type IN ('model','tool_call','status','error','completion')
 AND seq<=sqlc.arg(through_seq)
 ORDER BY seq DESC LIMIT sqlc.arg(page_limit);

-- name: RunActivityAfter :many
SELECT * FROM run_events WHERE run_id=sqlc.arg(run_id)
 AND type IN ('model','tool_call','status','error','completion')
 AND seq>sqlc.arg(after_seq) AND seq<=sqlc.arg(through_seq)
 ORDER BY seq LIMIT sqlc.arg(page_limit);

-- name: RunActivityEdges :one
SELECT EXISTS(SELECT 1 FROM run_events older WHERE older.run_id=sqlc.arg(run_id)
 AND older.type IN ('model','tool_call','status','error','completion')
 AND older.seq<sqlc.arg(first_seq) AND older.seq<=sqlc.arg(through_seq)) AS has_older,
 EXISTS(SELECT 1 FROM run_events newer WHERE newer.run_id=sqlc.arg(run_id)
 AND newer.type IN ('model','tool_call','status','error','completion')
 AND newer.seq>sqlc.arg(last_seq) AND newer.seq<=sqlc.arg(through_seq)) AS has_newer;
