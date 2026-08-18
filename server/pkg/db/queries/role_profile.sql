-- RoleProfile versions are append-only. Relational integrity for workspace and
-- actor identifiers is owned by the orchestration service; there are no DB
-- foreign keys or cascades.

-- name: LockRoleProfileSeries :exec
SELECT pg_advisory_xact_lock(
    hashtextextended(sqlc.arg('workspace_id')::uuid::text || ':' || sqlc.arg('profile_key')::text, 0)
);

-- name: LockRoleProfileCommand :exec
SELECT pg_advisory_xact_lock(
    hashtextextended(sqlc.arg('workspace_id')::uuid::text || ':command:' || sqlc.arg('command_id')::uuid::text, 0)
);

-- name: GetNextRoleProfileVersion :one
SELECT COALESCE(max(version), 0)::integer + 1
FROM role_profile
WHERE workspace_id = sqlc.arg('workspace_id')
  AND profile_key = sqlc.arg('profile_key');

-- name: CreateRoleProfileVersion :one
INSERT INTO role_profile (
    workspace_id, profile_key, version, duty, name, description, config,
    command_id, created_by
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('profile_key'), sqlc.arg('version'),
    sqlc.arg('duty'), sqlc.arg('name'), sqlc.arg('description'),
    sqlc.arg('config'), sqlc.arg('command_id'), sqlc.arg('created_by')
)
RETURNING *;

-- name: GetRoleProfileVersionByCommand :one
SELECT * FROM role_profile
WHERE workspace_id = sqlc.arg('workspace_id')
  AND command_id = sqlc.arg('command_id');

-- name: GetRoleProfileVersion :one
SELECT * FROM role_profile
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id');

-- name: GetRoleProfileVersionByKey :one
SELECT * FROM role_profile
WHERE workspace_id = sqlc.arg('workspace_id')
  AND profile_key = sqlc.arg('profile_key')
  AND version = sqlc.arg('version');

-- name: ListRoleProfileVersions :many
SELECT * FROM role_profile
WHERE workspace_id = sqlc.arg('workspace_id')
ORDER BY profile_key ASC, version DESC, id ASC;

-- name: ListLatestRoleProfiles :many
SELECT DISTINCT ON (profile_key) *
FROM role_profile
WHERE workspace_id = sqlc.arg('workspace_id')
ORDER BY profile_key ASC, version DESC, id ASC;
