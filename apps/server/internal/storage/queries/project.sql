-- name: ProjectSettings :one
SELECT * FROM project_settings WHERE owner_user_id=? AND room_id=?;
-- name: InsertProjectSettings :exec
INSERT INTO project_settings(owner_user_id,room_id,revision,value) VALUES(?,?,1,?);
-- name: SaveProjectSettings :execrows
UPDATE project_settings SET revision=revision+1,value=? WHERE owner_user_id=? AND room_id=? AND revision=?;
-- name: ProjectSettingRequest :one
SELECT * FROM project_setting_requests WHERE owner_user_id=? AND request_id=?;
-- name: InsertProjectSettingRequest :exec
INSERT INTO project_setting_requests(owner_user_id,request_id,request_hash,result) VALUES(?,?,?,?);
-- name: InsertGitHubDeliverySource :exec
INSERT INTO github_delivery_sources(delivery_id,attempt,resource_id,baseline_commit,result_commit,task_id,created_at) VALUES(?,?,?,?,?,?,?);
