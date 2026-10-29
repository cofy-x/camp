-- name: CreateReview :exec
INSERT INTO reviews(id,room_id,resource_id,path,title,requester_user_id,reviewer_user_id,submitted_commit,delivered_commit,status,revision,thread_root_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?);
-- name: GetReview :one
SELECT r.*,requester.name AS requester_name,reviewer.name AS reviewer_name,reviewer.enabled AS reviewer_enabled,f.current_commit
FROM reviews r JOIN users requester ON requester.id=r.requester_user_id JOIN users reviewer ON reviewer.id=r.reviewer_user_id JOIN resources f ON f.id=r.resource_id WHERE r.id=?;
-- name: ListReviews :many
SELECT r.*,requester.name AS requester_name,reviewer.name AS reviewer_name,reviewer.enabled AS reviewer_enabled,f.current_commit
FROM reviews r JOIN users requester ON requester.id=r.requester_user_id JOIN users reviewer ON reviewer.id=r.reviewer_user_id JOIN resources f ON f.id=r.resource_id
WHERE (@room_id='' OR r.room_id=@room_id) AND (@resource_id='' OR r.resource_id=@resource_id)
ORDER BY r.updated_at DESC,r.id;
-- name: UpdateReview :execrows
UPDATE reviews SET reviewer_user_id=?,submitted_commit=?,delivered_commit=?,status=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?;
-- name: CreateReviewEvent :exec
INSERT INTO review_events(id,review_id,actor_user_id,actor_name,action,comment,commit_id,reviewer_user_id,revision,message_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?);
-- name: ListReviewEvents :many
SELECT * FROM review_events WHERE review_id=? ORDER BY revision;
-- name: GetReviewRequest :one
SELECT request_hash,result FROM review_requests WHERE user_id=? AND room_id=? AND request_id=?;
-- name: CreateReviewRequest :exec
INSERT INTO review_requests(user_id,room_id,request_id,request_hash,result) VALUES(?,?,?,?,?);
