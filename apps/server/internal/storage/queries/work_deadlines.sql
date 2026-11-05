-- name: ExpiredWorkItems :many
SELECT * FROM work_items
WHERE status NOT IN ('completed','cancelled','needs_attention') AND coalesce(json_extract(value,'$.stopRequested'),0)=0
 AND julianday(json_extract(value,'$.deadline'))<=julianday(sqlc.arg(now_at))
ORDER BY seq LIMIT 100;
