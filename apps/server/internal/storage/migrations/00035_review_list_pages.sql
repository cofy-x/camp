-- +goose Up
DROP INDEX reviews_room;
DROP INDEX reviews_resource;
CREATE INDEX reviews_created ON reviews(created_at DESC,id DESC);
CREATE INDEX reviews_room_created ON reviews(room_id,created_at DESC,id DESC);
CREATE INDEX reviews_resource_created ON reviews(resource_id,created_at DESC,id DESC);
CREATE INDEX reviews_file_created ON reviews(resource_id,path,created_at DESC,id DESC);
CREATE INDEX reviews_room_resource_created ON reviews(room_id,resource_id,created_at DESC,id DESC);
CREATE INDEX reviews_room_file_created ON reviews(room_id,resource_id,path,created_at DESC,id DESC);

-- +goose Down
DROP INDEX reviews_created;
DROP INDEX reviews_room_created;
DROP INDEX reviews_resource_created;
DROP INDEX reviews_file_created;
DROP INDEX reviews_room_resource_created;
DROP INDEX reviews_room_file_created;
CREATE INDEX reviews_room ON reviews(room_id,updated_at DESC,id);
CREATE INDEX reviews_resource ON reviews(resource_id,updated_at DESC,id);
