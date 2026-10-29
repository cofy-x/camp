-- name: ListResources :many
SELECT * FROM resources WHERE NOT EXISTS(SELECT 1 FROM room_uploads u WHERE u.resource_id=resources.id) ORDER BY created_at,id;
-- name: GetResource :one
SELECT * FROM resources WHERE id=?;
-- name: CreateResource :exec
INSERT INTO resources(id,name,source_path,current_commit,environment,created_at,updated_at) VALUES(?,?,?,?,?,?,?);
-- name: UpdateResource :exec
UPDATE resources SET name=?,environment=?,updated_at=? WHERE id=?;
-- name: PublishResource :execrows
UPDATE resources SET current_commit=?,updated_at=? WHERE id=? AND current_commit=?;
-- name: ListAgents :many
SELECT * FROM agents ORDER BY created_at,id;
-- name: GetAgent :one
SELECT * FROM agents WHERE id=?;
-- name: GetAgentByHandle :one
SELECT * FROM agents WHERE handle=?;
-- name: CreateAgent :exec
INSERT INTO agents(id,name,handle,icon,instructions,access,enabled,revision,created_at,updated_at,avatar) VALUES(?,?,?,?,?,?,?,?,?,?,?);
-- name: UpdateAgent :execrows
UPDATE agents SET name=?,handle=?,icon=?,instructions=?,access=?,enabled=?,avatar=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?;
-- name: BackfillAgentAvatar :exec
UPDATE agents SET avatar=? WHERE id=? AND avatar='';
-- name: ListAgentAvatarCounts :many
SELECT avatar,count(*) AS uses FROM agents WHERE avatar<>'' GROUP BY avatar;
-- name: ListMissingAgentAvatars :many
SELECT id FROM agents WHERE avatar='' ORDER BY created_at,id;
-- name: ListRooms :many
SELECT * FROM rooms ORDER BY created_at,id;
-- name: GetRoom :one
SELECT * FROM rooms WHERE id=?;
-- name: CreateRoom :exec
INSERT INTO rooms(id,name,description,resource_id,default_agent_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?);
-- name: UpdateRoom :exec
UPDATE rooms SET name=?,description=?,resource_id=?,default_agent_id=?,updated_at=? WHERE id=?;
-- name: TouchRoom :exec
UPDATE rooms SET activity_seq=?,updated_at=? WHERE id=?;
-- name: ClearDefaultAgent :exec
UPDATE rooms SET default_agent_id=NULL WHERE id=? AND default_agent_id=?;
-- name: ListMembers :many
SELECT a.* FROM agents a JOIN memberships m ON m.agent_id=a.id WHERE m.room_id=? ORDER BY m.created_at,a.id;
-- name: GetMembership :one
SELECT * FROM memberships WHERE room_id=? AND agent_id=?;
-- name: JoinRoom :exec
INSERT INTO memberships(room_id,agent_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING;
-- name: LeaveRoom :exec
DELETE FROM memberships WHERE room_id=? AND agent_id=?;
-- name: GetMessage :one
SELECT * FROM messages WHERE id=?;
-- name: FindMessageRequest :one
SELECT * FROM messages WHERE room_id=? AND author_user_id=? AND request_id=?;
-- name: NextMessageSequence :one
SELECT CAST(COALESCE(MAX(seq),0)+1 AS INTEGER) FROM messages WHERE room_id=?;
-- name: CreateMessage :exec
INSERT INTO messages(id,room_id,thread_root_id,seq,author_kind,agent_id,run_id,content,request_id,fingerprint,created_at,updated_at,references_json,author_user_id,author_name,review_id,task_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);
-- name: ListMessages :many
SELECT * FROM messages WHERE room_id=@room_id AND (COALESCE(thread_root_id,'')=@thread_root_id OR id=@thread_root_id) AND seq>@after_seq ORDER BY seq LIMIT 500;
-- name: ContextMessages :many
SELECT * FROM messages WHERE room_id=@room_id AND (COALESCE(thread_root_id,'')=@thread_root_id OR id=@thread_root_id) AND seq>@after_seq AND seq<=@through_seq ORDER BY seq DESC LIMIT 100;
-- name: GetRun :one
SELECT snapshot FROM runs WHERE id=?;
-- name: CreateRun :exec
INSERT INTO runs(id,room_id,thread_root_id,origin_message_id,agent_id,resource_id,status,snapshot,created_at,updated_at,requested_by_user_id) VALUES(?,?,?,?,?,?,?,?,?,?,?);
-- name: SaveRun :exec
UPDATE runs SET status=?,snapshot=?,updated_at=? WHERE id=?;
-- name: ListQueuedRuns :many
SELECT snapshot FROM runs WHERE status='queued' ORDER BY created_at,id;
-- name: ListUnfinishedRuns :many
SELECT snapshot FROM runs WHERE status IN ('preparing','running','checking','cancelling') ORDER BY created_at,id;
-- name: ListRoomRuns :many
SELECT snapshot FROM runs WHERE room_id=? ORDER BY created_at,id;
-- name: ListMessageRuns :many
SELECT snapshot FROM runs WHERE origin_message_id=? ORDER BY created_at,id;
-- name: ResourceRevisionKnown :one
SELECT EXISTS(SELECT 1 FROM resources r WHERE r.id=@resource_id AND r.current_commit=@commit_id)
    OR EXISTS(SELECT 1 FROM runs w WHERE w.resource_id=@resource_id
        AND (json_extract(w.snapshot,'$.baselineCommit')=@commit_id OR json_extract(w.snapshot,'$.resultCommit')=@commit_id))
    OR EXISTS(SELECT 1 FROM resource_publications p WHERE p.resource_id=@resource_id AND (p.commit_id=@commit_id OR p.previous_commit=@commit_id))
    OR EXISTS(SELECT 1 FROM review_events e JOIN reviews v ON v.id=e.review_id WHERE v.resource_id=@resource_id AND e.commit_id=@commit_id);
-- name: RoomHasResource :one
SELECT EXISTS(SELECT 1 FROM rooms r WHERE r.id=@room_id AND r.resource_id=@resource_id)
    OR EXISTS(SELECT 1 FROM runs w WHERE w.room_id=@room_id AND w.resource_id=@resource_id)
    OR EXISTS(SELECT 1 FROM room_uploads u WHERE u.room_id=@room_id AND u.resource_id=@resource_id);
-- name: ResourceResultRun :one
SELECT snapshot FROM runs WHERE room_id=@room_id AND resource_id=@resource_id
    AND json_extract(snapshot,'$.resultCommit')=@commit_id ORDER BY created_at DESC,id DESC LIMIT 1;
-- name: ActiveAgentRun :one
SELECT id FROM runs WHERE agent_id=? AND status IN ('preparing','running','checking','cancelling') LIMIT 1;
-- name: NextRunEventSequence :one
SELECT CAST(COALESCE(MAX(seq),0)+1 AS INTEGER) FROM run_events WHERE run_id=?;
-- name: CreateRunEvent :exec
INSERT INTO run_events(run_id,seq,type,text,created_at) VALUES(?,?,?,?,?);
-- name: ListRunEvents :many
SELECT * FROM run_events WHERE run_id=? AND seq>? ORDER BY seq LIMIT 500;
-- name: GetConversation :one
SELECT * FROM agent_conversations WHERE agent_id=? AND room_id=? AND thread_root_id=?;
-- name: SaveConversation :exec
INSERT INTO agent_conversations(id,agent_id,room_id,thread_root_id,resource_id,agent_revision,runtime_key,generation,session_id,context_seq,resumable) VALUES(?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(agent_id,room_id,thread_root_id) DO UPDATE SET resource_id=excluded.resource_id,agent_revision=excluded.agent_revision,runtime_key=excluded.runtime_key,generation=excluded.generation,session_id=excluded.session_id,context_seq=excluded.context_seq,resumable=excluded.resumable;
-- name: FinishConversation :exec
UPDATE agent_conversations SET context_seq=?,resumable=? WHERE agent_id=? AND room_id=? AND thread_root_id=? AND generation=? AND session_id=?;
-- name: CreateWorkspaceEvent :one
INSERT INTO workspace_events(type,room_id,entity_id,created_at) VALUES(?,?,?,?) RETURNING id;
-- name: ListWorkspaceEvents :many
SELECT * FROM workspace_events WHERE id>? ORDER BY id LIMIT 500;

-- name: GetRunNotice :one
SELECT id FROM messages WHERE run_id=? AND author_kind='system';

-- name: SetRoomMessageSequence :exec
UPDATE rooms SET last_message_seq=? WHERE id=?;
