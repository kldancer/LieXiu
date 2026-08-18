-- RolePolicySnapshot rows are immutable Mission/Duty facts. Relational
-- integrity is owned by the orchestration service; there are no foreign keys.

-- name: CreateMissionRolePolicySnapshot :one
INSERT INTO mission_role_policy_snapshot (
    workspace_id, mission_id, duty, role_profile_id, role_profile_key,
    role_profile_version, profile_name, profile_description, config, agent_id,
    schema_version, content_hash, frozen_by
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('mission_id'), sqlc.arg('duty'),
    sqlc.arg('role_profile_id'), sqlc.arg('role_profile_key'),
    sqlc.arg('role_profile_version'), sqlc.arg('profile_name'),
    sqlc.arg('profile_description'), sqlc.arg('config'), sqlc.arg('agent_id'),
    sqlc.arg('schema_version'), sqlc.arg('content_hash'), sqlc.arg('frozen_by')
)
RETURNING *;

-- name: GetMissionRolePolicySnapshot :one
SELECT * FROM mission_role_policy_snapshot
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND duty = sqlc.arg('duty');

-- name: ListMissionRolePolicySnapshots :many
SELECT * FROM mission_role_policy_snapshot
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
ORDER BY duty ASC, id ASC;
