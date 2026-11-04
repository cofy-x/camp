-- name: InspectionIdentity :one
SELECT value FROM server_identity WHERE id=1;
-- name: InspectionResources :many
SELECT id,current_commit FROM resources ORDER BY id LIMIT 100001;
-- name: InspectionReservations :many
SELECT resource_key FROM source_imports ORDER BY resource_key LIMIT 100001;
-- name: InspectionTransfers :many
SELECT id,status FROM run_attempts UNION ALL SELECT id,status FROM source_imports LIMIT 100001;
-- name: InspectionTotals :one
SELECT (SELECT count(*) FROM task_inputs) AS task_inputs,
 (SELECT count(*) FROM runs) AS runs,
 (SELECT count(*) FROM collaboration_tasks) AS tasks,
 (SELECT count(*) FROM runners) AS runners;
-- name: InspectionRevisions :many
SELECT resource_id,commit_id FROM (
 SELECT id AS resource_id,current_commit AS commit_id FROM resources
 UNION SELECT resource_id,commit_id FROM resource_publications
 UNION SELECT resource_id,previous_commit FROM resource_publications
 UNION SELECT resource_id,submitted_commit FROM reviews
 UNION SELECT resource_id,delivered_commit FROM reviews
 UNION SELECT r.resource_id,e.commit_id FROM review_events e JOIN reviews r ON e.review_id=r.id
 UNION SELECT resource_id,commit_id FROM source_imports WHERE resource_id IS NOT NULL
 UNION SELECT resource_id,CAST(coalesce(json_extract(snapshot,'$.baselineCommit'),'') AS TEXT) FROM runs
 UNION SELECT resource_id,CAST(coalesce(json_extract(snapshot,'$.inputCommit'),'') AS TEXT) FROM runs
 UNION SELECT resource_id,CAST(coalesce(json_extract(snapshot,'$.resultCommit'),'') AS TEXT) FROM runs
 UNION SELECT resource_id,CAST(coalesce(json_extract(snapshot,'$.previousDraftCommit'),'') AS TEXT) FROM runs
 UNION SELECT CAST(json_extract(snapshot,'$.resourceId') AS TEXT),CAST(coalesce(json_extract(snapshot,'$.baseSharedCommit'),'') AS TEXT) FROM collaboration_tasks
 UNION SELECT CAST(json_extract(snapshot,'$.resourceId') AS TEXT),CAST(coalesce(json_extract(snapshot,'$.checkpointCommit'),'') AS TEXT) FROM collaboration_tasks
 UNION SELECT CAST(json_extract(snapshot,'$.resourceId') AS TEXT),CAST(coalesce(json_extract(snapshot,'$.previousDraftCommit'),'') AS TEXT) FROM collaboration_tasks
 UNION SELECT CAST(json_extract(snapshot,'$.resourceId') AS TEXT),CAST(coalesce(json_extract(snapshot,'$.publishedCommit'),'') AS TEXT) FROM collaboration_tasks
 UNION SELECT CAST(json_extract(g.value,'$.resourceId') AS TEXT),CAST(json_extract(g.value,'$.baseCommit') AS TEXT)
 FROM orchestration_records o,json_each(o.value,'$.mandates') g
 UNION SELECT CAST(json_extract(j.value,'$.resourceId') AS TEXT),CAST(json_extract(j.value,'$.commit') AS TEXT)
 FROM messages m,json_each(m.references_json) j WHERE json_extract(j.value,'$.kind')='file'
 UNION SELECT CAST(json_extract(j.value,'$.resourceId') AS TEXT),CAST(json_extract(j.value,'$.commit') AS TEXT)
 FROM runs r,json_each(r.snapshot,'$.references') j WHERE json_extract(j.value,'$.kind')='file'
 UNION SELECT CAST(json_extract(value,'$.resourceId') AS TEXT),CAST(json_extract(value,'$.baseCommit') AS TEXT)
 FROM user_drafts WHERE kind='document'
) WHERE resource_id IS NOT NULL AND commit_id IS NOT NULL AND commit_id<>'' ORDER BY resource_id,commit_id LIMIT 100001;
