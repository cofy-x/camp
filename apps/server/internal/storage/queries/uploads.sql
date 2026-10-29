-- name: GetRoomUpload :one
SELECT * FROM room_uploads WHERE room_id=? AND uploaded_by_user_id=? AND request_id=?;
-- name: GetResourceUpload :one
SELECT * FROM room_uploads WHERE resource_id=?;
-- name: ListRoomUploads :many
SELECT * FROM room_uploads WHERE room_id=@room_id
    AND (@before_resource='' OR (created_at,resource_id)<(@before_created,@before_resource))
    ORDER BY created_at DESC,resource_id DESC LIMIT 201;
-- name: CreateRoomUpload :exec
INSERT INTO room_uploads(room_id,uploaded_by_user_id,request_id,resource_id,name,size,sha256,created_at) VALUES(?,?,?,?,?,?,?,?);
