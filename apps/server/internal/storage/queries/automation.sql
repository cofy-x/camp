-- name: GetAutomationPolicy :one
SELECT * FROM automation_policies WHERE room_id=? AND source_id=?;
-- name: GetAutomationPolicyID :one
SELECT * FROM automation_policies WHERE id=?;
-- name: ListAutomationPolicies :many
SELECT * FROM automation_policies WHERE room_id=? ORDER BY source_id;
-- name: InsertAutomationPolicy :exec
INSERT INTO automation_policies(id,room_id,source_id,revision,owner_user_id,enabled,mode,value,credential_ref,start_after) VALUES(?,?,?,?,?,?,?,?,?,?);
-- name: UpdateAutomationPolicy :exec
UPDATE automation_policies SET revision=?,owner_user_id=?,enabled=?,mode=?,value=?,credential_ref=?,start_after=? WHERE id=?;
-- name: AutomationFrontier :one
SELECT CAST(coalesce(max(seq),0) AS INTEGER) FROM automation_events WHERE room_id=?;
-- name: InsertAutomationEvent :one
INSERT INTO automation_events(id,room_id,source_id,thread_key,kind,actor_id,actor_kind,message_id,external_event_id,status,value) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING seq;
-- name: GetAutomationEvent :one
SELECT * FROM automation_events WHERE id=?;
-- name: GetAutomationExternalEvent :one
SELECT * FROM automation_events WHERE external_event_id=?;
-- name: AutomationEventsPage :many
SELECT * FROM automation_events WHERE room_id=? AND seq>? AND seq<=? ORDER BY seq LIMIT ?;
-- name: PendingAutomationEvents :many
SELECT * FROM automation_events WHERE status='pending' ORDER BY seq LIMIT 100;
-- name: AutomationThreadBatch :many
SELECT * FROM automation_events WHERE room_id=? AND source_id=? AND thread_key=? AND status='pending' ORDER BY seq LIMIT 20;
-- name: UpdateAutomationEvent :exec
UPDATE automation_events SET status=?,evaluation_id=?,task_id=?,reason=?,policy_revision=? WHERE id=?;
-- name: InsertAutomationEvaluation :exec
INSERT INTO automation_evaluations(id,source_id,thread_key,status,candidate,created_at) VALUES(?,?,?,'running',?,?);
-- name: GetAutomationEvaluation :one
SELECT * FROM automation_evaluations WHERE id=?;
-- name: RunningAutomationThread :one
SELECT count(*) FROM automation_evaluations WHERE source_id=? AND thread_key=? AND status='running';
-- name: FinishAutomationEvaluation :exec
UPDATE automation_evaluations SET status=?,assessment=? WHERE id=? AND status='running';
-- name: EnsureAutomationUsage :exec
INSERT INTO automation_usage(policy_id,day) VALUES(?,?) ON CONFLICT DO NOTHING;
-- name: GetAutomationUsage :one
SELECT * FROM automation_usage WHERE policy_id=? AND day=?;
-- name: ChargeAutomationEvaluation :exec
UPDATE automation_usage SET evaluations=evaluations+1 WHERE policy_id=? AND day=?;
-- name: ChargeAutomationTask :exec
UPDATE automation_usage SET tasks=tasks+1 WHERE policy_id=? AND day=?;
-- name: GetAutomationRequest :one
SELECT * FROM automation_requests WHERE user_id=? AND request_id=?;
-- name: SaveAutomationRequest :exec
INSERT INTO automation_requests(user_id,request_id,request_hash,result) VALUES(?,?,?,?);
-- name: ListAutomationTaskSubscriptions :many
SELECT * FROM automation_subscriptions WHERE task_id=? ORDER BY id;
-- name: ListAutomationThreadSubscriptions :many
SELECT * FROM automation_subscriptions WHERE source_id=? AND thread_key=? AND enabled=1 ORDER BY id;
-- name: InsertAutomationSubscription :exec
INSERT INTO automation_subscriptions(id,task_id,source_id,thread_key,enabled,revision) VALUES(?,?,?,?,1,1) ON CONFLICT(task_id,source_id,thread_key) DO NOTHING;
-- name: InsertAutomationDelivery :exec
INSERT INTO automation_deliveries(id,task_id,source_id,policy_id,revision,status,value,credential_ref,created_at) VALUES(?,?,?,?,1,'pending',?,?,?) ON CONFLICT(id) DO NOTHING;
-- name: GetAutomationDelivery :one
SELECT * FROM automation_deliveries WHERE id=?;
-- name: ListAutomationDeliveries :many
SELECT * FROM automation_deliveries WHERE task_id=? ORDER BY created_at,id;
-- name: PendingAutomationDeliveries :many
SELECT * FROM automation_deliveries d WHERE status IN ('pending','reconcile') AND NOT EXISTS (SELECT 1 FROM automation_deliveries earlier WHERE earlier.task_id=d.task_id AND earlier.source_id=d.source_id AND json_extract(earlier.value,'$.number')=json_extract(d.value,'$.number') AND earlier.status NOT IN ('sent','failed','withdrawn') AND (earlier.created_at<d.created_at OR (earlier.created_at=d.created_at AND earlier.id<d.id))) ORDER BY created_at,id LIMIT 20;
-- name: StaleAutomationDeliveries :many
SELECT d.* FROM automation_deliveries d
JOIN collaboration_tasks t ON t.id=d.task_id
WHERE d.status IN ('pending','blocked','failed','unknown')
 AND (t.status IN ('cancelling','cancelled') OR (
  coalesce(json_extract(d.value,'$.questionId'),'')<>''
  AND (t.status<>'waiting_for_person'
   OR coalesce(json_extract(t.snapshot,'$.question.id'),'')<>json_extract(d.value,'$.questionId')
   OR coalesce(json_extract(t.snapshot,'$.question.answer'),'')<>'')
 ))
ORDER BY d.created_at,d.id LIMIT 20;
-- name: UpdateAutomationDelivery :exec
UPDATE automation_deliveries SET status=?,revision=revision+1,value=?,credential_ref=? WHERE id=?;
-- name: RecoverAutomationDeliveries :exec
UPDATE automation_deliveries SET status='reconcile',revision=revision+1 WHERE status='sending';
-- name: RunningAutomationEvaluations :many
SELECT * FROM automation_evaluations WHERE status='running';
-- name: CloseTaskAutomationSubscriptions :exec
UPDATE automation_subscriptions SET enabled=0,revision=revision+1,closed_after=(SELECT coalesce(max(e.seq),0) FROM automation_events e WHERE e.source_id=automation_subscriptions.source_id AND e.thread_key=automation_subscriptions.thread_key) WHERE automation_subscriptions.task_id=? AND enabled=1;
-- name: RetireCancelledAutomationEvents :exec
UPDATE automation_events SET status='context',reason='task_cancelled'
WHERE status IN ('pending','evaluating')
 AND EXISTS (SELECT 1 FROM automation_subscriptions s WHERE s.task_id=? AND s.source_id=automation_events.source_id AND s.thread_key=automation_events.thread_key AND automation_events.seq<=s.closed_after)
 AND NOT EXISTS (
  SELECT 1 FROM automation_subscriptions live
  JOIN collaboration_tasks t ON t.id=live.task_id
  WHERE live.enabled=1 AND live.source_id=automation_events.source_id
   AND live.thread_key=automation_events.thread_key
   AND t.room_id=automation_events.room_id
   AND t.status NOT IN ('completed','cancelled','interrupted','cancelling')
 );
-- name: AutomationDeliveriesPage :many
SELECT * FROM automation_deliveries
WHERE task_id=sqlc.arg(task_id)
 AND (sqlc.arg(before_at)='' OR created_at<sqlc.arg(before_at)
  OR (created_at=sqlc.arg(before_at) AND id<sqlc.arg(before_id)))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: AutomationTaskBatchDeliveries :many
SELECT DISTINCT source_event_id FROM task_inputs
WHERE task_id=sqlc.arg(task_id)
 AND source_event_id IN (SELECT CAST(value AS TEXT) FROM json_each(sqlc.arg(event_ids)))
ORDER BY source_event_id;
-- name: AutomationActivityAscending :many
SELECT e.*,
 CAST(coalesce(CASE WHEN e.source_id='room' THEN json_extract(e.value,'$.content')
  WHEN e.source_id='task' THEN m.content ELSE json_extract(g.value,'$.title') END,'') AS TEXT) AS preview,
 CAST(coalesce(json_extract(a.assessment,'$.outcome'),'') AS TEXT) AS assessment_outcome,
 CAST(coalesce(json_extract(a.assessment,'$.reason'),'') AS TEXT) AS assessment_reason
FROM automation_events e
LEFT JOIN messages m ON m.id=e.message_id AND m.room_id=e.room_id AND e.source_id='task'
LEFT JOIN github_events g ON g.id=e.external_event_id AND g.room_id=e.room_id AND g.source_id=e.source_id
LEFT JOIN automation_evaluations a ON a.id=e.evaluation_id AND a.source_id=e.source_id AND a.thread_key=e.thread_key
WHERE e.room_id=sqlc.arg(room_id) AND e.seq<=sqlc.arg(through_seq)
 AND e.seq>sqlc.arg(after_seq)
ORDER BY e.seq ASC LIMIT sqlc.arg(page_limit);
-- name: AutomationActivityDescending :many
SELECT e.*,
 CAST(coalesce(CASE WHEN e.source_id='room' THEN json_extract(e.value,'$.content')
  WHEN e.source_id='task' THEN m.content ELSE json_extract(g.value,'$.title') END,'') AS TEXT) AS preview,
 CAST(coalesce(json_extract(a.assessment,'$.outcome'),'') AS TEXT) AS assessment_outcome,
 CAST(coalesce(json_extract(a.assessment,'$.reason'),'') AS TEXT) AS assessment_reason
FROM automation_events e
LEFT JOIN messages m ON m.id=e.message_id AND m.room_id=e.room_id AND e.source_id='task'
LEFT JOIN github_events g ON g.id=e.external_event_id AND g.room_id=e.room_id AND g.source_id=e.source_id
LEFT JOIN automation_evaluations a ON a.id=e.evaluation_id AND a.source_id=e.source_id AND a.thread_key=e.thread_key
WHERE e.room_id=sqlc.arg(room_id) AND e.seq<=sqlc.arg(through_seq)
 AND e.seq<=sqlc.arg(before_seq)
ORDER BY e.seq DESC LIMIT sqlc.arg(page_limit);
-- name: AutomationActivitySourceAscending :many
SELECT e.*,
 CAST(coalesce(CASE WHEN e.source_id='room' THEN json_extract(e.value,'$.content')
  WHEN e.source_id='task' THEN m.content ELSE json_extract(g.value,'$.title') END,'') AS TEXT) AS preview,
 CAST(coalesce(json_extract(a.assessment,'$.outcome'),'') AS TEXT) AS assessment_outcome,
 CAST(coalesce(json_extract(a.assessment,'$.reason'),'') AS TEXT) AS assessment_reason
FROM automation_events e
LEFT JOIN messages m ON m.id=e.message_id AND m.room_id=e.room_id AND e.source_id='task'
LEFT JOIN github_events g ON g.id=e.external_event_id AND g.room_id=e.room_id AND g.source_id=e.source_id
LEFT JOIN automation_evaluations a ON a.id=e.evaluation_id AND a.source_id=e.source_id AND a.thread_key=e.thread_key
WHERE e.room_id=sqlc.arg(room_id) AND e.seq<=sqlc.arg(through_seq)
 AND e.source_id=sqlc.arg(source_id)
 AND e.seq>sqlc.arg(after_seq)
ORDER BY e.seq ASC LIMIT sqlc.arg(page_limit);
-- name: AutomationActivitySourceDescending :many
SELECT e.*,
 CAST(coalesce(CASE WHEN e.source_id='room' THEN json_extract(e.value,'$.content')
  WHEN e.source_id='task' THEN m.content ELSE json_extract(g.value,'$.title') END,'') AS TEXT) AS preview,
 CAST(coalesce(json_extract(a.assessment,'$.outcome'),'') AS TEXT) AS assessment_outcome,
 CAST(coalesce(json_extract(a.assessment,'$.reason'),'') AS TEXT) AS assessment_reason
FROM automation_events e
LEFT JOIN messages m ON m.id=e.message_id AND m.room_id=e.room_id AND e.source_id='task'
LEFT JOIN github_events g ON g.id=e.external_event_id AND g.room_id=e.room_id AND g.source_id=e.source_id
LEFT JOIN automation_evaluations a ON a.id=e.evaluation_id AND a.source_id=e.source_id AND a.thread_key=e.thread_key
WHERE e.room_id=sqlc.arg(room_id) AND e.seq<=sqlc.arg(through_seq)
 AND e.source_id=sqlc.arg(source_id)
 AND e.seq<=sqlc.arg(before_seq)
ORDER BY e.seq DESC LIMIT sqlc.arg(page_limit);
