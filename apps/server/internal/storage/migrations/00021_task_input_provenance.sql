-- +goose Up
-- An input is a task-local delivery, independently of its source Message.
-- Retain historical IDs, sequence numbers and JSON bytes without reinterpretation.
CREATE TABLE task_inputs_next (
 id TEXT PRIMARY KEY NOT NULL,
 task_id TEXT NOT NULL REFERENCES collaboration_tasks(id),
 message_id TEXT NOT NULL REFERENCES messages(id),
 source_event_id TEXT NOT NULL DEFAULT '',
 part_index INTEGER NOT NULL DEFAULT 0 CHECK(part_index>=0),
 seq INTEGER NOT NULL CHECK(seq > 0),
 content_bytes INTEGER NOT NULL CHECK(content_bytes BETWEEN 1 AND 8192),
 value TEXT NOT NULL CHECK(coalesce(json_valid(value)
  AND json_type(value,'$.id')='text' AND json_extract(value,'$.id')=id
  AND json_type(value,'$.authorUserId')='text'
  AND json_type(value,'$.authorName')='text'
  AND json_type(value,'$.createdAt')='text'
  AND json_type(value,'$.content')='text'
  AND length(CAST(json_extract(value,'$.content') AS BLOB))=content_bytes
  AND json_extract(value,'$.seq')=seq,0)),
 UNIQUE(task_id,seq)
);
INSERT INTO task_inputs_next(id,task_id,message_id,seq,content_bytes,value)
 SELECT id,task_id,id,seq,content_bytes,value FROM task_inputs;
DROP TABLE task_inputs;
ALTER TABLE task_inputs_next RENAME TO task_inputs;
CREATE INDEX task_input_message ON task_inputs(task_id,message_id,seq);
CREATE UNIQUE INDEX task_input_event_delivery ON task_inputs(task_id,source_event_id,part_index)
 WHERE source_event_id<>'';

-- +goose Down
-- Provenance, original bytes and independent delivery identities are retained.
SELECT 1;
