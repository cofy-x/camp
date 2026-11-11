-- +goose Up
CREATE INDEX message_window ON messages(room_id,coalesce(thread_root_id,''),seq);
CREATE INDEX run_thread_window ON runs(room_id,thread_root_id,created_at DESC,id DESC);
CREATE INDEX run_thread_active_window ON runs(room_id,thread_root_id,created_at DESC,id DESC)
 WHERE status IN ('queued','preparing','running','checking','cancelling');
CREATE INDEX task_attention_window ON collaboration_tasks(room_id,created_at DESC,id DESC)
 WHERE status IN ('waiting_for_person','needs_attention','interrupted');

-- +goose Down
DROP INDEX task_attention_window;
DROP INDEX run_thread_active_window;
DROP INDEX run_thread_window;
DROP INDEX message_window;
