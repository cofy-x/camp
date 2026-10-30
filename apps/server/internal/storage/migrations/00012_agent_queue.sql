-- +goose Up
CREATE INDEX runs_agent_queue ON runs(agent_id,created_at,id)
    WHERE status='queued' AND COALESCE(json_extract(snapshot,'$.cancellationReason'),'')='';

-- +goose Down
DROP INDEX runs_agent_queue;
