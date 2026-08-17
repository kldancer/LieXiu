-- name: ListWorkspaces :many
SELECT w.id, w.name, w.slug, w.description, w.settings,
       w.created_at, w.updated_at, w.context, w.repos,
       w.issue_prefix, w.issue_counter, w.avatar_url, w.attribution_fail_closed
FROM member m
JOIN workspace w ON w.id = m.workspace_id
WHERE m.user_id = $1
ORDER BY w.created_at ASC;

-- name: ListDaemonWorkspaces :many
-- Daemons only need the membership set and display name to discover which
-- workspaces should have local runtimes. Keep this projection intentionally
-- narrow so the periodic consistency check never reads UI-only JSON/text
-- columns such as settings, repos, or context.
SELECT w.id, w.name
FROM member m
JOIN workspace w ON w.id = m.workspace_id
WHERE m.user_id = $1
ORDER BY w.id ASC;

-- name: GetDaemonWorkspace :one
-- Workspace-scoped daemon tokens do not carry a user ID. This narrow lookup
-- lets them use the same endpoint without widening their token scope.
SELECT id, name
FROM workspace
WHERE id = $1;

-- name: GetWorkspace :one
SELECT * FROM workspace
WHERE id = $1;

-- name: GetWorkspaceBySlug :one
SELECT * FROM workspace
WHERE slug = $1;

-- name: GetWorkspaceAttributionFailClosed :one
-- Lean read of the fail-closed attribution policy for the enqueue hot path
-- (MUL-4302 §3.5), avoiding a full workspace-row fetch.
SELECT attribution_fail_closed FROM workspace
WHERE id = $1;

-- name: CreateWorkspace :one
INSERT INTO workspace (name, slug, description, context, issue_prefix)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateWorkspace :one
UPDATE workspace SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    context = COALESCE(sqlc.narg('context'), context),
    settings = COALESCE(sqlc.narg('settings'), settings),
    repos = COALESCE(sqlc.narg('repos'), repos),
    issue_prefix = COALESCE(sqlc.narg('issue_prefix'), issue_prefix),
    avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: IncrementIssueCounter :one
UPDATE workspace SET issue_counter = issue_counter + 1
WHERE id = $1
RETURNING issue_counter;

-- name: LockWorkspaceForDelete :one
-- Taken first by DeleteWorkspace so all workspace-owned relationship cleanup
-- and the final workspace delete share one transaction-level fence.
SELECT id FROM workspace WHERE id = $1 FOR UPDATE;

-- name: DeleteWorkspace :exec
-- Resource-label junctions, custom issue property definitions, and quick
-- actions carry NO FK to workspace, so — unlike the CASCADE-backed
-- tables the DELETE below sweeps — they are not cleaned up implicitly. Remove
-- their workspace-owned rows here so they commit or roll back atomically with
-- the workspace row.
WITH ws_agents AS (
    SELECT id FROM agent WHERE workspace_id = $1
),
ws_skills AS (
    SELECT id FROM skill WHERE workspace_id = $1
),
cleared_agent_label_assignments AS (
    DELETE FROM agent_to_label WHERE agent_id IN (SELECT id FROM ws_agents)
),
cleared_skill_label_assignments AS (
    DELETE FROM skill_to_label WHERE skill_id IN (SELECT id FROM ws_skills)
),
cleared_quick_actions AS (
    DELETE FROM quick_action WHERE workspace_id = $1
),
deleted_pending_check_suites AS (
    DELETE FROM github_pending_check_suite WHERE workspace_id = $1
),
ws_github_prs AS (
    SELECT id FROM github_pull_request WHERE workspace_id = $1
),
cleared_github_pr_check_runs AS (
    -- github_pull_request_check_run intentionally has no FK. Remove its rows
    -- before the workspace delete cascades away the parent PR mirrors.
    DELETE FROM github_pull_request_check_run
    WHERE pr_id IN (SELECT id FROM ws_github_prs)
),
-- VCS tables (migration 213) carry no FK to workspace, so they are not cascaded
-- away by the DELETE below. Sweep the workspace's connections, mirrored PRs,
-- their issue links, and CI statuses here. issue_vcs_pull_request has no
-- workspace_id, so reach it through the workspace's PRs; vcs_commit_status has
-- none either, so reach it through the workspace's connections.
ws_vcs_prs AS (
    SELECT id FROM vcs_pull_request WHERE workspace_id = $1
),
ws_vcs_connections AS (
    SELECT id FROM vcs_connection WHERE workspace_id = $1
),
cleared_vcs_pr_links AS (
    DELETE FROM issue_vcs_pull_request
    WHERE pull_request_id IN (SELECT id FROM ws_vcs_prs)
),
cleared_vcs_commit_statuses AS (
    DELETE FROM vcs_commit_status
    WHERE connection_id IN (SELECT id FROM ws_vcs_connections)
),
cleared_vcs_prs AS (
    DELETE FROM vcs_pull_request WHERE workspace_id = $1
),
cleared_vcs_connections AS (
    DELETE FROM vcs_connection WHERE workspace_id = $1
),
cleared_client_usage_workspace AS (
    UPDATE client_usage_daily SET workspace_id = NULL WHERE workspace_id = $1
)
DELETE FROM workspace WHERE workspace.id = $1;
