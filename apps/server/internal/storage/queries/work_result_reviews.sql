-- name: ResultFileReviews :many
SELECT CAST(file.value AS TEXT) AS path, review.id, review.status, review.revision, review.requester_user_id, review.reviewer_user_id
FROM json_each(CAST(sqlc.arg(paths) AS TEXT)) file
JOIN reviews review ON review.id = (
 SELECT candidate.id FROM reviews candidate
 WHERE candidate.room_id=sqlc.arg(room_id) AND candidate.resource_id=sqlc.arg(resource_id)
  AND candidate.path=file.value AND candidate.submitted_commit=sqlc.arg(commit_id) AND candidate.status <> 'cancelled'
 ORDER BY (candidate.status = 'delivered') DESC,candidate.created_at DESC,candidate.id DESC LIMIT 1
);
