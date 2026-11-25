-- name: ExpiredWorkItems :many
SELECT * FROM work_items INDEXED BY work_item_expiry_due
WHERE status NOT IN ('completed','cancelled') AND coalesce(json_extract(value,'$.stopRequested'),0)=0
 AND julianday(json_extract(value,'$.deadline'))<=julianday(sqlc.arg(now_at))
 AND coalesce(json_extract(value,'$.observedAuthorizationExpiry'),'')<>json_extract(value,'$.deadline')
 AND (status<>'needs_attention' OR coalesce(json_extract(value,'$.workRecovery.kind'),'')<>'renew_authorization')
ORDER BY seq LIMIT 100;
