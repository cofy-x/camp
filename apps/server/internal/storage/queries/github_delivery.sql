-- name: GitHubRepository :one
SELECT * FROM github_repositories WHERE id=?;
-- name: GitHubRepositories :many
SELECT * FROM github_repositories WHERE owner_user_id=? ORDER BY id LIMIT 33;
-- name: InsertGitHubRepository :exec
INSERT INTO github_repositories(id,owner_user_id,revision,repository_id,enabled,credential_ref,value) VALUES(?,?,1,?,?,?,?);
-- name: SaveGitHubRepository :execrows
UPDATE github_repositories SET revision=revision+1,enabled=?,credential_ref=?,value=? WHERE id=? AND revision=?;
-- name: GitHubRepositoryRequest :one
SELECT * FROM github_repository_requests WHERE owner_user_id=? AND request_id=?;
-- name: InsertGitHubRepositoryRequest :exec
INSERT INTO github_repository_requests(owner_user_id,request_id,request_hash,result) VALUES(?,?,?,?);
-- name: InsertGitHubPublication :exec
INSERT INTO github_publications(id,work_item_id,connection_id,revision,status,next_check_at,value) VALUES(?,?,?,1,?,?,?);
-- name: GitHubDelivery :one
SELECT * FROM github_publications WHERE work_item_id=?;
-- name: GitHubDeliveryByID :one
SELECT * FROM github_publications WHERE id=?;
-- name: DueGitHubDeliveries :many
SELECT * FROM github_publications WHERE status IN ('queued','waiting','working') AND next_check_at<=? ORDER BY next_check_at,seq LIMIT 16;
-- name: SaveGitHubDelivery :execrows
UPDATE github_publications SET revision=revision+1,status=?,next_check_at=?,value=? WHERE id=? AND revision=?;
-- name: RecordGitHubDeliveryReceipt :exec
INSERT INTO github_delivery_receipts(delivery_id,revision,event_id,value) VALUES(?,?,?,?);
-- name: GitHubDeliveryUnsettled :one
SELECT count(*) FROM github_publications WHERE work_item_id=? AND status NOT IN ('succeeded','stopped');

-- name: CancelGitHubRepositoryRequest :exec
INSERT INTO github_repository_requests(owner_user_id,request_id,request_hash,result,cancelled) VALUES(?,?,'','{}',1);
-- name: GitHubDeliveryReceipt :one
SELECT value FROM github_delivery_receipts WHERE delivery_id=? AND revision=?;
