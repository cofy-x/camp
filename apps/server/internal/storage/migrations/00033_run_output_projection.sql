-- +goose Up
-- Output is a bounded display projection. Original Run events and Runner
-- receipts are immutable and remain in their existing tables.
CREATE TABLE run_progress (
 run_id TEXT PRIMARY KEY NOT NULL REFERENCES runs(id),
 updated_at TEXT NOT NULL,
 output_bytes INTEGER NOT NULL CHECK(output_bytes>=0),
 output_prefix TEXT NOT NULL DEFAULT ''
);
CREATE TABLE run_output_chunks (
 run_id TEXT NOT NULL REFERENCES runs(id),
 seq INTEGER NOT NULL,
 text TEXT NOT NULL,
 PRIMARY KEY(run_id,seq)
);
INSERT INTO run_progress(run_id,updated_at,output_bytes)
 SELECT id,updated_at,length(CAST(coalesce(json_extract(snapshot,'$.output'),'') AS BLOB)) FROM runs;
WITH RECURSIVE pieces(run_id,seq,output) AS (
 SELECT id,1,coalesce(json_extract(snapshot,'$.output'),'') FROM runs
 WHERE coalesce(json_extract(snapshot,'$.output'),'')<>''
 UNION ALL
 SELECT run_id,seq+1,output FROM pieces WHERE seq*4096<length(output)
)
INSERT INTO run_output_chunks(run_id,seq,text)
 SELECT run_id,seq,substr(output,(seq-1)*4096+1,4096) FROM pieces;
UPDATE runs SET snapshot=json_remove(snapshot,'$.output');

-- Full readers compose the original contract only when requested. Summary and
-- admission queries continue to project directly from the cold Run record.
CREATE VIEW run_records AS
 SELECT r.id,r.room_id,r.thread_root_id,r.origin_message_id,r.agent_id,r.resource_id,
 r.status,r.created_at,p.updated_at,r.requested_by_user_id,
 CAST(json_set(r.snapshot,'$.status',r.status,'$.updatedAt',p.updated_at,
 '$.output',p.output_prefix||coalesce((SELECT group_concat(text,'') FROM
  (SELECT text FROM run_output_chunks c WHERE c.run_id=r.id ORDER BY c.seq)),'')) AS TEXT) AS snapshot
 FROM runs r JOIN run_progress p ON p.run_id=r.id;

-- +goose Down
UPDATE runs SET snapshot=(SELECT snapshot FROM run_records WHERE run_records.id=runs.id),
 updated_at=(SELECT updated_at FROM run_progress WHERE run_id=runs.id);
DROP VIEW run_records;
DROP TABLE run_output_chunks;
DROP TABLE run_progress;
