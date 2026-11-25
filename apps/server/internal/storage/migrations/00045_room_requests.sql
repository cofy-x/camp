-- +goose Up
CREATE TABLE room_requests (
 user_id TEXT NOT NULL REFERENCES users(id),
 request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(user_id,request_id)
) STRICT;

-- +goose Down
DROP TABLE room_requests;
