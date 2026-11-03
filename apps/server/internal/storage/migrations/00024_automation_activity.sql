-- +goose Up
-- Activity source filters select matching events before their bounded keyset page.
CREATE INDEX automation_room_source_events ON automation_events(room_id,source_id,seq);

-- +goose Down
-- Retain the additive index without altering event history.
SELECT 1;
