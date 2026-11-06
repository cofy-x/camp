-- +goose Up
-- This view projects current business responsibility. It stores no read,
-- dismissal or resolution state; each domain remains its lifecycle owner.
CREATE VIEW personal_attention AS
WITH work_state AS (
 SELECT w.*, t.status AS task_status, t.snapshot AS task_snapshot,
  CASE WHEN coalesce(json_extract(w.value,'$.activeTaskId'),'')<>'' THEN
   CASE WHEN coalesce(json_extract(t.snapshot,'$.activeRunId'),'')=''
    AND t.status='waiting_for_person'
    AND coalesce(json_extract(t.snapshot,'$.question.text'),'')<>''
    AND coalesce(json_extract(t.snapshot,'$.question.answer'),'')='' THEN 1 ELSE 0 END
  WHEN w.status<>'needs_attention' AND (w.status='waiting_for_person' OR coalesce(json_extract(w.value,'$.question'),'')<>'') THEN 1
  ELSE 0 END AS judgment,
  CASE WHEN coalesce(json_extract(w.value,'$.activeTaskId'),'')<>'' THEN
   CASE WHEN coalesce(json_extract(t.snapshot,'$.activeRunId'),'')=''
    AND (t.status IN ('needs_attention','interrupted') OR (
     t.status='waiting_for_person' AND coalesce(json_extract(t.snapshot,'$.question.text'),'')<>''
     AND coalesce(json_extract(t.snapshot,'$.question.answer'),'')=''
    )) THEN 1 ELSE 0 END
  WHEN w.status IN ('waiting_for_person','needs_attention') OR coalesce(json_extract(w.value,'$.question'),'')<>'' THEN 1
  ELSE 0 END AS actionable,
  max(w.updated_at,coalesce(t.updated_at,w.updated_at)) AS changed_at
 FROM work_items w
 LEFT JOIN collaboration_tasks t ON t.id=json_extract(w.value,'$.activeTaskId')
 WHERE w.status NOT IN ('completed','cancelled','stopping')
  AND coalesce(json_extract(w.value,'$.stopRequested'),0)=0
  AND coalesce(json_extract(w.value,'$.status'),'')<>'stopping'
)
SELECT 'work:'||w.id AS id,'work_item' AS source,w.id AS entity_id,
 w.owner_user_id AS responsible_user_id,w.room_id,r.name AS room_name,
 CASE WHEN w.judgment=1 THEN 'judgment' ELSE 'exception' END AS kind,
 CASE WHEN w.judgment=1 THEN 1 ELSE 0 END AS priority,
 CAST(coalesce(json_extract(w.value,'$.goal'),'') AS TEXT) AS title,
 CAST(CASE WHEN w.judgment=1 THEN coalesce(nullif(json_extract(w.task_snapshot,'$.question.text'),''),nullif(json_extract(w.value,'$.question'),''),'Review the current question.')
  ELSE coalesce(nullif(json_extract(w.value,'$.question'),''),nullif(json_extract(w.task_snapshot,'$.error'),''),'Check this work and its recovery options.') END AS TEXT) AS summary,
 w.status AS status,
 CASE WHEN w.judgment=1 THEN
  CASE WHEN coalesce(json_extract(w.value,'$.activeTaskId'),'')<>'' THEN 'task_question'
   WHEN json_extract(w.value,'$.questionKind')='completion' THEN 'completion_confirmation'
   ELSE 'work_judgment' END
  ELSE 'work_attention' END AS reason,
 w.created_at,w.changed_at AS updated_at,'work' AS target_app,w.id AS work_item_id,
 '' AS review_id,'' AS task_id
FROM work_state w JOIN rooms r ON r.id=w.room_id WHERE w.actionable=1
UNION ALL
SELECT 'review:'||v.id,'review',v.id,v.reviewer_user_id,v.room_id,r.name,
 'review',2,v.title,'',v.status,'review_requested',
 v.created_at,v.updated_at,'workspace','',v.id,''
FROM reviews v JOIN users u ON u.id=v.reviewer_user_id
JOIN resources f ON f.id=v.resource_id JOIN rooms r ON r.id=v.room_id
WHERE v.status='in_review' AND u.enabled=1 AND v.submitted_commit=f.current_commit
UNION ALL
SELECT 'review:'||v.id,'review',v.id,v.requester_user_id,v.room_id,r.name,
 CASE WHEN v.submitted_commit<>f.current_commit OR (u.enabled=0 AND v.status<>'approved') THEN 'exception' ELSE 'judgment' END,
 CASE WHEN v.submitted_commit<>f.current_commit OR (u.enabled=0 AND v.status<>'approved') THEN 0 ELSE 1 END,
 v.title,
 CASE WHEN v.submitted_commit<>f.current_commit THEN 'Room files changed. Resubmit the current version.'
  WHEN u.enabled=0 AND v.status<>'approved' THEN 'Choose an active reviewer to continue.'
  WHEN v.status='changes_requested' THEN 'Revise and resubmit the file.'
  ELSE 'Approved. Deliver the reviewed version.' END,
 v.status,
 CASE WHEN v.submitted_commit<>f.current_commit THEN 'review_outdated'
  WHEN u.enabled=0 AND v.status<>'approved' THEN 'reviewer_unavailable'
  WHEN v.status='changes_requested' THEN 'review_changes_requested'
  ELSE 'review_ready_delivery' END,
 v.created_at,v.updated_at,'workspace','',v.id,''
FROM reviews v JOIN users u ON u.id=v.reviewer_user_id
JOIN resources f ON f.id=v.resource_id JOIN rooms r ON r.id=v.room_id
WHERE v.status IN ('in_review','changes_requested','approved')
 AND NOT(v.status='in_review' AND u.enabled=1 AND v.submitted_commit=f.current_commit)
UNION ALL
SELECT 'task:'||t.id,'task',t.id,t.requested_by_user_id,t.room_id,r.name,
 CASE WHEN t.status='waiting_for_person' THEN 'judgment' ELSE 'exception' END,
 CASE WHEN t.status='waiting_for_person' THEN 1 ELSE 0 END,
 CAST(coalesce(json_extract(t.snapshot,'$.goal'),'') AS TEXT),
 CAST(CASE WHEN t.status='waiting_for_person' THEN json_extract(t.snapshot,'$.question.text') ELSE coalesce(nullif(json_extract(t.snapshot,'$.error'),''),'Check this task and its recovery options.') END AS TEXT),
 t.status,CASE WHEN t.status='waiting_for_person' THEN 'task_question' ELSE 'task_attention' END,
 t.created_at,t.updated_at,'workspace','','',t.id
FROM collaboration_tasks t JOIN rooms r ON r.id=t.room_id
WHERE NOT EXISTS(SELECT 1 FROM work_item_tasks w WHERE w.task_id=t.id)
 AND coalesce(json_extract(t.snapshot,'$.activeRunId'),'')=''
 AND (t.status='needs_attention' OR (t.status='waiting_for_person'
  AND coalesce(json_extract(t.snapshot,'$.question.text'),'')<>''
  AND coalesce(json_extract(t.snapshot,'$.question.answer'),'')=''))
UNION ALL
SELECT 'delivery:'||d.id,'delivery',d.id,t.requested_by_user_id,t.room_id,r.name,'exception',0,
 CAST(coalesce(json_extract(t.snapshot,'$.goal'),'External comment delivery') AS TEXT),
 CAST(coalesce(nullif(json_extract(d.value,'$.reason'),''),'Check whether this comment was sent before retrying.') AS TEXT),
 d.status,CASE WHEN d.status IN ('unknown','withdrawn') THEN 'delivery_unknown' WHEN d.status='blocked' THEN 'delivery_blocked' ELSE 'delivery_failed' END,
 d.created_at,d.created_at,CASE WHEN w.work_item_id IS NOT NULL THEN 'work' ELSE 'workspace' END,
 coalesce(w.work_item_id,''),'',t.id
FROM automation_deliveries d JOIN collaboration_tasks t ON t.id=d.task_id
JOIN rooms r ON r.id=t.room_id LEFT JOIN work_item_tasks w ON w.task_id=t.id
LEFT JOIN work_items parent ON parent.id=w.work_item_id
WHERE ((d.status IN ('unknown','failed','blocked') AND coalesce(json_extract(d.value,'$.withdrawn'),0)=0)
 OR (d.status='withdrawn' AND coalesce(json_array_length(d.value,'$.receipts'),0)=0
  AND coalesce(json_extract(d.value,'$.commentId'),0)<1
  AND EXISTS(SELECT 1 FROM json_each(d.value,'$.attempts') a WHERE json_extract(a.value,'$.outcome') IN ('unknown','sending'))))
 AND (
  (coalesce(json_array_length(d.value,'$.receipts'),0)=0 AND coalesce(json_extract(d.value,'$.commentId'),0)<1
   AND (d.status='unknown' OR EXISTS(SELECT 1 FROM json_each(d.value,'$.attempts') a WHERE json_extract(a.value,'$.outcome') IN ('unknown','sending'))))
  OR (t.status NOT IN ('cancelling','cancelled')
   AND coalesce(parent.status,'') NOT IN ('cancelled','stopping')
   AND coalesce(json_extract(parent.value,'$.stopRequested'),0)=0
   AND (coalesce(json_extract(d.value,'$.questionId'),'')='' OR (
    t.status='waiting_for_person'
    AND coalesce(json_extract(t.snapshot,'$.question.id'),'')=json_extract(d.value,'$.questionId')
    AND coalesce(json_extract(t.snapshot,'$.question.answer'),'')=''
   )))
 );

CREATE INDEX attention_work_owner ON work_items(owner_user_id,status,seq);
CREATE INDEX attention_review_reviewer ON reviews(reviewer_user_id,status,created_at,id);
CREATE INDEX attention_review_requester ON reviews(requester_user_id,status,created_at,id);
CREATE INDEX attention_task_requester ON collaboration_tasks(requested_by_user_id,status,created_at,id);
CREATE INDEX attention_delivery_task ON automation_deliveries(task_id,status,created_at,id);

-- +goose Down
DROP VIEW personal_attention;
DROP INDEX attention_work_owner;
DROP INDEX attention_review_reviewer;
DROP INDEX attention_review_requester;
DROP INDEX attention_task_requester;
DROP INDEX attention_delivery_task;
