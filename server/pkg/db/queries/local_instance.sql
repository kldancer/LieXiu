-- name: LockLocalInstanceBootstrap :exec
SELECT pg_advisory_xact_lock(631945889);

-- name: GetLocalInstance :one
SELECT * FROM local_instance
WHERE singleton_key = TRUE;

-- name: CreateLocalInstance :one
INSERT INTO local_instance (singleton_key, owner_user_id, canonical_workspace_id, bootstrap_version)
VALUES (TRUE, $1, $2, 1)
RETURNING *;

-- name: CountLocalBootstrapUsers :one
SELECT count(*) FROM "user";

-- name: CountLocalBootstrapWorkspaces :one
SELECT count(*) FROM workspace;

-- name: CountLocalOwnerMemberships :one
SELECT count(*)
FROM member
WHERE role = 'owner';

-- name: ListLocalOwnerCandidatesByEmail :many
SELECT u.id AS user_id, w.id AS workspace_id
FROM "user" u
JOIN member m ON m.user_id = u.id AND m.role = 'owner'
JOIN workspace w ON w.id = m.workspace_id
WHERE lower(u.email) = lower($1)
ORDER BY w.id;

-- name: GetLocalOwnerCandidate :one
SELECT u.id AS user_id, w.id AS workspace_id
FROM "user" u
JOIN member m ON m.user_id = u.id AND m.role = 'owner'
JOIN workspace w ON w.id = m.workspace_id
WHERE lower(u.email) = lower($1)
  AND w.id = $2;
