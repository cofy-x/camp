-- name: GetRoomRequest :one
SELECT request_hash,result FROM room_requests WHERE user_id=? AND request_id=?;

-- name: SaveRoomRequest :exec
INSERT INTO room_requests(user_id,request_id,request_hash,result) VALUES(?,?,?,?);
