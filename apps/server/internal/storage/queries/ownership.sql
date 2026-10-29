-- name: SetAgentOwnership :exec
UPDATE agents SET owner_user_id=?,preferred_runner_id=? WHERE id=?;
-- name: SetRunnerOwnership :exec
UPDATE runners SET owner_user_id=?,team_shared=? WHERE id=?;
-- name: SetEnrollmentOwner :exec
UPDATE runner_enrollments SET owner_user_id=? WHERE id=?;
-- name: GetEnrollmentByID :one
SELECT * FROM runner_enrollments WHERE id=?;
-- name: RevokeOwnerEnrollments :exec
UPDATE runner_enrollments SET revoked=1 WHERE owner_user_id=?;
-- name: ListRunnerAgentGrants :many
SELECT agent_id FROM runner_agent_grants WHERE runner_id=? ORDER BY agent_id;
-- name: GetRunnerAgentGrant :one
SELECT agent_id FROM runner_agent_grants WHERE runner_id=? AND agent_id=?;
-- name: CreateRunnerAgentGrant :exec
INSERT INTO runner_agent_grants(runner_id,agent_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING;
-- name: DeleteRunnerAgentGrant :exec
DELETE FROM runner_agent_grants WHERE runner_id=? AND agent_id=?;
-- name: InitializePersonalRunnerDefault :exec
INSERT INTO personal_runner_defaults(user_id,runner_id) VALUES(?,?) ON CONFLICT DO NOTHING;
-- name: GetPersonalRunnerDefault :one
SELECT * FROM personal_runner_defaults WHERE user_id=?;
-- name: UpdatePersonalRunnerDefault :execrows
UPDATE personal_runner_defaults SET runner_id=?,revision=revision+1 WHERE user_id=? AND revision=?;
-- name: GetTeamRunnerDefault :one
SELECT * FROM team_runner_defaults WHERE id='default';
-- name: UpdateTeamRunnerDefault :execrows
UPDATE team_runner_defaults SET runner_id=?,revision=revision+1 WHERE id='default' AND revision=?;
