-- name: ReviewListFirst :one
SELECT created_at,id FROM reviews ORDER BY created_at DESC,id DESC LIMIT 1;

-- name: ReviewListGlobalPage :many
SELECT r.*,requester.name AS requester_name,reviewer.name AS reviewer_name,reviewer.enabled AS reviewer_enabled,f.current_commit
FROM reviews r JOIN users requester ON requester.id=r.requester_user_id JOIN users reviewer ON reviewer.id=r.reviewer_user_id JOIN resources f ON f.id=r.resource_id
WHERE (r.created_at,r.id) <= (@before_created_at,@before_id)
ORDER BY r.created_at DESC,r.id DESC LIMIT @result_limit;

-- name: ReviewListRoomPage :many
SELECT r.*,requester.name AS requester_name,reviewer.name AS reviewer_name,reviewer.enabled AS reviewer_enabled,f.current_commit
FROM reviews r JOIN users requester ON requester.id=r.requester_user_id JOIN users reviewer ON reviewer.id=r.reviewer_user_id JOIN resources f ON f.id=r.resource_id
WHERE r.room_id=@room_id AND (r.created_at,r.id) <= (@before_created_at,@before_id)
ORDER BY r.created_at DESC,r.id DESC LIMIT @result_limit;

-- name: ReviewListResourcePage :many
SELECT r.*,requester.name AS requester_name,reviewer.name AS reviewer_name,reviewer.enabled AS reviewer_enabled,f.current_commit
FROM reviews r JOIN users requester ON requester.id=r.requester_user_id JOIN users reviewer ON reviewer.id=r.reviewer_user_id JOIN resources f ON f.id=r.resource_id
WHERE r.resource_id=@resource_id
AND (r.created_at,r.id) <= (@before_created_at,@before_id)
ORDER BY r.created_at DESC,r.id DESC LIMIT @result_limit;

-- name: ReviewListFilePage :many
SELECT r.*,requester.name AS requester_name,reviewer.name AS reviewer_name,reviewer.enabled AS reviewer_enabled,f.current_commit
FROM reviews r JOIN users requester ON requester.id=r.requester_user_id JOIN users reviewer ON reviewer.id=r.reviewer_user_id JOIN resources f ON f.id=r.resource_id
WHERE r.resource_id=@resource_id AND r.path=@path
AND (r.created_at,r.id) <= (@before_created_at,@before_id)
ORDER BY r.created_at DESC,r.id DESC LIMIT @result_limit;

-- name: ReviewListRoomResourcePage :many
SELECT r.*,requester.name AS requester_name,reviewer.name AS reviewer_name,reviewer.enabled AS reviewer_enabled,f.current_commit
FROM reviews r JOIN users requester ON requester.id=r.requester_user_id JOIN users reviewer ON reviewer.id=r.reviewer_user_id JOIN resources f ON f.id=r.resource_id
WHERE r.room_id=@room_id AND r.resource_id=@resource_id
AND (r.created_at,r.id) <= (@before_created_at,@before_id)
ORDER BY r.created_at DESC,r.id DESC LIMIT @result_limit;

-- name: ReviewListRoomFilePage :many
SELECT r.*,requester.name AS requester_name,reviewer.name AS reviewer_name,reviewer.enabled AS reviewer_enabled,f.current_commit
FROM reviews r JOIN users requester ON requester.id=r.requester_user_id JOIN users reviewer ON reviewer.id=r.reviewer_user_id JOIN resources f ON f.id=r.resource_id
WHERE r.room_id=@room_id AND r.resource_id=@resource_id AND r.path=@path
AND (r.created_at,r.id) <= (@before_created_at,@before_id)
ORDER BY r.created_at DESC,r.id DESC LIMIT @result_limit;
