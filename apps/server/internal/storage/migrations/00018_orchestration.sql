-- +goose Up
CREATE TABLE orchestration_records (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 source TEXT NOT NULL CHECK(source IN ('room','task')),
 event_id TEXT NOT NULL,
 room_id TEXT NOT NULL REFERENCES rooms(id),
 message_id TEXT REFERENCES messages(id),
 task_id TEXT REFERENCES collaboration_tasks(id),
 value TEXT NOT NULL CHECK(coalesce(json_valid(value)
  AND json_extract(value,'$.trigger.source')=source
  AND json_extract(value,'$.trigger.eventId')=event_id
  AND json_extract(value,'$.trigger.roomId')=room_id
  AND coalesce(json_extract(value,'$.trigger.messageId'),'')=coalesce(message_id,'')
  AND coalesce(json_extract(value,'$.trigger.taskId'),'')=coalesce(task_id,'')
  AND json_extract(value,'$.trigger.actorKind')='user'
  AND json_type(value,'$.decision.policyVersion')='text'
  AND length(json_extract(value,'$.decision.policyVersion'))>0
  AND json_extract(value,'$.decision.outcome') IN ('accept','context','ignore','stop')
  AND json_type(value,'$.decision.recipientAgentIds')='array'
  AND json_type(value,'$.decision.work')='array'
  AND json_type(value,'$.mandates')='array',0)),
 UNIQUE(source,event_id)
);
CREATE INDEX orchestration_room_page ON orchestration_records(room_id,seq);

-- Existing messages and receipts are left untouched. Admission rules and grants
-- that were not recorded at the time cannot be reconstructed as historical fact.

-- +goose Down
-- Preserve admission evidence and its source identities.
SELECT 1;
