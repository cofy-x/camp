-- +goose Up
CREATE INDEX reviews_exact_output ON reviews(room_id,resource_id,path,submitted_commit,(status = 'delivered') DESC,created_at DESC,id DESC)
WHERE status <> 'cancelled';

-- +goose Down
DROP INDEX reviews_exact_output;
