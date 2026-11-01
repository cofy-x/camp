-- +goose Up
CREATE TABLE task_inputs (
 id TEXT PRIMARY KEY NOT NULL REFERENCES messages(id),
 task_id TEXT NOT NULL REFERENCES collaboration_tasks(id),
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

-- A failing guard rolls back the entire migration and its ledger entry.
CREATE TABLE task_input_migration_guard (valid INTEGER NOT NULL CHECK(valid=1));
INSERT INTO task_input_migration_guard SELECT CASE WHEN NOT EXISTS (
 SELECT 1 FROM collaboration_tasks WHERE coalesce(json_type(snapshot,'$.inputs'),'null') NOT IN ('array','null')
) THEN 1 ELSE 0 END;

INSERT INTO task_inputs(id,task_id,seq,content_bytes,value)
SELECT json_extract(i.value,'$.id'),t.id,CAST(i.key AS INTEGER)+1,
 length(CAST(json_extract(i.value,'$.content') AS BLOB)),
 json_set(i.value,'$.seq',CAST(i.key AS INTEGER)+1)
FROM collaboration_tasks t,json_each(t.snapshot,'$.inputs') i
WHERE json_type(t.snapshot,'$.inputs')='array';

INSERT INTO task_input_migration_guard SELECT CASE WHEN NOT EXISTS (
 SELECT 1 FROM task_inputs i LEFT JOIN messages m ON m.id=i.id
 WHERE m.id IS NULL OR m.task_id<>i.task_id OR m.content<>json_extract(i.value,'$.content')
  OR coalesce(m.author_user_id,'')<>json_extract(i.value,'$.authorUserId')
  OR m.author_name<>json_extract(i.value,'$.authorName') OR m.created_at<>json_extract(i.value,'$.createdAt')
) THEN 1 ELSE 0 END;
INSERT INTO task_input_migration_guard SELECT CASE WHEN NOT EXISTS (
 SELECT 1 FROM collaboration_tasks t
 WHERE coalesce(json_array_length(t.snapshot,'$.inputs'),0)<>(SELECT count(*) FROM task_inputs i WHERE i.task_id=t.id)
) THEN 1 ELSE 0 END;
INSERT INTO task_input_migration_guard SELECT CASE WHEN NOT EXISTS (
 SELECT 1 FROM runs r JOIN collaboration_tasks t ON t.id=json_extract(r.snapshot,'$.taskId')
 WHERE r.status='succeeded' AND json_extract(r.snapshot,'$.intent.kind') IN ('handoff','ask_person','finish')
 AND (coalesce(json_type(r.snapshot,'$.taskInputRevision'),'integer')<>'integer'
  OR coalesce(json_extract(r.snapshot,'$.taskInputRevision'),0)<0
  OR coalesce(json_extract(r.snapshot,'$.taskInputRevision'),0)>coalesce(json_array_length(t.snapshot,'$.inputs'),0))
) THEN 1 ELSE 0 END;

UPDATE collaboration_tasks SET snapshot=json_set(snapshot,
 '$.inputRevision',coalesce(json_array_length(snapshot,'$.inputs'),0),
 '$.inputBytes',coalesce((SELECT sum(content_bytes) FROM task_inputs i WHERE i.task_id=collaboration_tasks.id),0),
 '$.coveredInputRevision',coalesce((SELECT max(CAST(json_extract(r.snapshot,'$.taskInputRevision') AS INTEGER)) FROM runs r
  WHERE json_extract(r.snapshot,'$.taskId')=collaboration_tasks.id AND r.status='succeeded'
  AND json_extract(r.snapshot,'$.intent.kind') IN ('handoff','ask_person','finish')),0),
 '$.inputs',json('[]'));
DROP TABLE task_input_migration_guard;
CREATE INDEX task_runs_page ON runs(json_extract(snapshot,'$.taskId'),created_at,id);

-- +goose Down
-- Input originals and their identities cannot be discarded by a downgrade.
SELECT 1;
