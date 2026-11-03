-- +goose Up
-- Resolve all projections of a source part before applying a task's frozen
-- input interval, including tasks that share the same visible Room Thread.
CREATE INDEX task_input_source_message ON task_inputs(message_id,source_event_id,part_index);

-- +goose Down
-- The additive index preserves retained provenance and immutable input bytes.
SELECT 1;
