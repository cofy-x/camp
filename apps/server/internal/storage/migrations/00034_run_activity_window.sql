-- +goose Up
CREATE INDEX run_activity_window ON run_events(run_id,seq)
WHERE type IN ('model','tool_call','status','error','completion');

-- +goose Down
DROP INDEX run_activity_window;
