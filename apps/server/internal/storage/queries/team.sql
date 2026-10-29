-- name: CountUsers :one
SELECT count(*) FROM users;
-- name: GetSetupToken :one
SELECT token_hash FROM team_setup WHERE id=1;
-- name: SetSetupToken :exec
INSERT INTO team_setup(id,token_hash) VALUES(1,?);
-- name: DeleteSetupToken :exec
DELETE FROM team_setup;
-- name: CreateUser :exec
INSERT INTO users(id,username,name,role,enabled,password_hash,created_at,updated_at,avatar) VALUES(?,?,?,?,?,?,?,?,?);
-- name: UpdateUserAvatar :execrows
UPDATE users SET avatar=@avatar,updated_at=@updated_at WHERE id=@id AND avatar=@expected_avatar AND enabled=1;
-- name: BackfillUserAvatar :exec
UPDATE users SET avatar=? WHERE id=? AND avatar='';
-- name: ListUserAvatarCounts :many
SELECT avatar,count(*) AS uses FROM users WHERE avatar<>'' GROUP BY avatar;
-- name: ListMissingUserAvatars :many
SELECT id FROM users WHERE avatar='' ORDER BY created_at,id;
-- name: GetUser :one
SELECT * FROM users WHERE id=?;
-- name: GetUserByUsername :one
SELECT * FROM users WHERE username=? COLLATE NOCASE;
-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at,id;
-- name: CountActiveAdmins :one
SELECT count(*) FROM users WHERE role='admin' AND enabled=1;
-- name: UpdateUserAccess :exec
UPDATE users SET role=?,enabled=?,updated_at=? WHERE id=?;
-- name: CreateSession :exec
INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES(?,?,?,?);
-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash=?;
-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash=?;
-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id=?;
-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at<=?;
-- name: CreateInvite :exec
INSERT INTO invites(id,token_hash,created_by_user_id,created_at,expires_at) VALUES(?,?,?,?,?);
-- name: GetInviteByToken :one
SELECT * FROM invites WHERE token_hash=?;
-- name: ListInvites :many
SELECT * FROM invites ORDER BY created_at DESC,id DESC LIMIT 200;
-- name: ConsumeInvite :execrows
UPDATE invites SET consumed_at=@now WHERE id=@id AND consumed_at='' AND revoked_at='' AND expires_at>@now;
-- name: RevokeInvite :execrows
UPDATE invites SET revoked_at=? WHERE id=? AND consumed_at='' AND revoked_at='';
-- name: GetUserPreferences :one
SELECT value FROM user_preferences WHERE user_id=?;
-- name: SaveUserPreferences :exec
INSERT INTO user_preferences(user_id,value) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET value=excluded.value;
-- name: GetUserLayout :one
SELECT value FROM user_layouts WHERE user_id=? AND client_id=?;
-- name: SaveUserLayout :exec
INSERT INTO user_layouts(user_id,client_id,value) VALUES(?,?,?) ON CONFLICT(user_id,client_id) DO UPDATE SET value=excluded.value;
-- name: GetDraft :one
SELECT * FROM user_drafts WHERE user_id=? AND kind=? AND draft_key=?;
-- name: SaveDraft :exec
INSERT INTO user_drafts(user_id,kind,draft_key,value,revision,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(user_id,kind,draft_key) DO UPDATE SET value=excluded.value,revision=excluded.revision,updated_at=excluded.updated_at;
-- name: ListRoomReads :many
SELECT * FROM room_reads WHERE user_id=?;
-- name: SaveRoomRead :exec
INSERT INTO room_reads(user_id,room_id,seq) VALUES(?,?,?) ON CONFLICT(user_id,room_id) DO UPDATE SET seq=max(room_reads.seq,excluded.seq);
-- name: CreateInboxItem :exec
INSERT INTO inbox(id,user_id,kind,room_id,message_id,run_id,review_id,task_id,created_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,kind,message_id) DO NOTHING;
-- name: ListInbox :many
SELECT i.*,room.name AS room_name,COALESCE(review.title,'') AS review_title
FROM inbox i JOIN rooms room ON room.id=i.room_id LEFT JOIN reviews review ON review.id=i.review_id
WHERE i.user_id=? ORDER BY i.created_at DESC,i.id DESC LIMIT 200;
-- name: ReadInboxItem :execrows
UPDATE inbox SET read_at=? WHERE id=? AND user_id=?;
-- name: ListUserActiveRuns :many
SELECT snapshot FROM runs WHERE requested_by_user_id=? AND status IN ('queued','preparing','running','checking','cancelling') ORDER BY created_at,id;

-- name: ListDrafts :many
SELECT kind,draft_key,revision,updated_at,COALESCE(json_extract(value,'$.title'),'') AS title,COALESCE(json_extract(value,'$.resourceId'),'') AS resource_id,COALESCE(json_extract(value,'$.path'),'') AS path FROM user_drafts WHERE user_id=? AND kind=? AND json_type(value)='object' ORDER BY updated_at DESC,draft_key LIMIT 100;

-- name: GetTeamProfile :one
SELECT * FROM team_profile WHERE id=1;
-- name: SaveTeamName :exec
UPDATE team_profile SET name=?,updated_at=? WHERE id=1;

-- name: CreateManagementEvent :exec
INSERT INTO management_events(user_id,action,object_id,detail,created_at) VALUES(?,?,?,?,?);
-- name: ListManagementEvents :many
SELECT * FROM management_events ORDER BY id DESC LIMIT 100;
