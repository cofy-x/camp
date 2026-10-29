-- +goose Up
CREATE TABLE room_uploads (
    room_id TEXT NOT NULL REFERENCES rooms(id),
    uploaded_by_user_id TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL CHECK(length(CAST(request_id AS BLOB)) BETWEEN 1 AND 128),
    resource_id TEXT NOT NULL UNIQUE REFERENCES resources(id),
    name TEXT NOT NULL CHECK(length(CAST(name AS BLOB)) BETWEEN 1 AND 255),
    size INTEGER NOT NULL CHECK(size BETWEEN 0 AND 8388608),
    sha256 TEXT NOT NULL CHECK(length(sha256) = 64),
    created_at TEXT NOT NULL,
    PRIMARY KEY(room_id, uploaded_by_user_id, request_id)
);
CREATE INDEX room_uploads_recent ON room_uploads(room_id, created_at DESC, resource_id DESC);

-- +goose Down
DROP TABLE room_uploads;
