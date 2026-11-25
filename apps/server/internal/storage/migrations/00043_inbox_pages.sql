-- +goose Up
CREATE INDEX inbox_user_page ON inbox(user_id, created_at DESC, id DESC);
CREATE INDEX inbox_user_unread ON inbox(user_id) WHERE read_at='';

-- +goose Down
DROP INDEX inbox_user_unread;
DROP INDEX inbox_user_page;
