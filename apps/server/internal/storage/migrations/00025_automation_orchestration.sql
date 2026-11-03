-- +goose Up
-- Extend the admission journal for system work delegated by a frozen automation
-- assessment. Preserve historical record bytes and sequence identities exactly.
CREATE TABLE orchestration_records_next (
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
  AND (json_extract(value,'$.trigger.actorKind')='user' OR (
   json_extract(value,'$.trigger.actorKind')='system'
   AND json_extract(value,'$.trigger.actorId')='automation'
   AND source='task' AND task_id IS NOT NULL
   AND json_extract(value,'$.trigger.kind')='task.start'
   AND json_extract(value,'$.decision.outcome')='accept'
   AND json_array_length(value,'$.mandates')=1
   AND json_type(value,'$.trigger.automation.policyId')='text'
   AND length(json_extract(value,'$.trigger.automation.policyId'))>0
   AND json_type(value,'$.trigger.automation.policyRevision')='integer'
   AND json_extract(value,'$.trigger.automation.policyRevision')>0
   AND json_type(value,'$.trigger.automation.evaluationId')='text'
   AND length(json_extract(value,'$.trigger.automation.evaluationId'))>0
   AND json_type(value,'$.trigger.automation.sourceEventIds')='array'
   AND json_array_length(value,'$.trigger.automation.sourceEventIds') BETWEEN 1 AND 20
  ))
  AND json_type(value,'$.decision.policyVersion')='text'
  AND length(json_extract(value,'$.decision.policyVersion'))>0
  AND json_extract(value,'$.decision.outcome') IN ('accept','context','ignore','stop')
  AND json_type(value,'$.decision.recipientAgentIds')='array'
  AND json_type(value,'$.decision.work')='array'
  AND json_type(value,'$.mandates')='array',0)),
 UNIQUE(source,event_id)
);
INSERT INTO orchestration_records_next(seq,source,event_id,room_id,message_id,task_id,value)
 SELECT seq,source,event_id,room_id,message_id,task_id,value FROM orchestration_records;
DROP TABLE orchestration_records;
ALTER TABLE orchestration_records_next RENAME TO orchestration_records;
CREATE INDEX orchestration_room_page ON orchestration_records(room_id,seq);

-- +goose Down
-- Retain automatic and historical admission evidence without reinterpretation.
SELECT 1;
