-- Workspace deletion is application-owned. These statements form a fixed,
-- bottom-up deletion plan; the legacy foreign keys remain only as a
-- compatibility safety net during the expand phase.

-- name: SetWorkspaceTeardownMode :exec
-- The setting is transaction-local. Migration 242 makes only the three DELETE
-- dirty triggers skip their per-row work while this flag is on.
SELECT set_config('liexiu.workspace_teardown', 'on', true);

-- name: LockTaskUsageRollupForWorkspaceDelete :exec
-- Advisory lock 4246 is the hourly rollup family's key: the rollup function
-- takes it per transaction (migration 272) and the backfill commands take it
-- per session. An in-flight rollup finishes first, then no new rollup can
-- write the workspace's aggregates until this delete commits. The caller
-- bounds this wait with SET LOCAL lock_timeout — batch jobs hold 4246 for
-- minutes, and an unbounded wait here hangs the delete request (MUL-5983).
SELECT pg_advisory_xact_lock(4246);

-- name: LockWorkspaceTaskOwnerAgents :exec
-- The three LockWorkspaceTaskOwner* statements are a write fence, not a lookup —
-- they deliberately return nothing, so a workspace with a huge owner set costs
-- the API process no memory at all.
--
-- agent_task_queue has no workspace_id, so a task becomes this workspace's
-- problem through agent_id, issue_id or runtime_id — and inserting such a task
-- requires FOR KEY SHARE on the referenced owner row, which conflicts with
-- FOR UPDATE. Holding these locks for the rest of the teardown transaction
-- therefore blocks any enqueue (or reassignment) that would add a task to this
-- workspace after we have swept it, which row locks on the tasks alone cannot
-- do. The handler holds the workspace row lock for the full delete window.
--
-- This fence is defence in depth, not the only guarantee: deleteWorkspaceTasks
-- re-verifies every owner after sweeping it and fails closed if tasks keep
-- appearing, so teardown does not silently leak rows even if the fence is ever
-- weakened (for example by removing the legacy FKs this one leans on).
SELECT 1 FROM agent WHERE agent.workspace_id = $1 FOR UPDATE;

-- name: LockWorkspaceTaskOwnerIssues :exec
SELECT 1 FROM issue WHERE issue.workspace_id = $1 FOR UPDATE;

-- name: LockWorkspaceTaskOwnerRuntimes :exec
SELECT 1 FROM agent_runtime WHERE agent_runtime.workspace_id = $1 FOR UPDATE;

-- name: ListWorkspaceAgentIDFirstPage :many
-- First page of the same walk. Split from the keyset query rather than seeded
-- with the all-zero uuid, because that value is itself a valid uuid: a row whose
-- id happened to be all zeros would be skipped forever by `id > $2`.
SELECT id FROM agent
WHERE agent.workspace_id = $1
ORDER BY id
LIMIT $2;

-- name: ListWorkspaceAgentIDPage :many
-- Owner enumeration in bounded, keyset-paged chunks. The rows are already locked
-- by LockWorkspaceTaskOwnerAgents above; this only walks them.
--
-- Keyset on id, not on a natural key: the cursor column must be immutable, and
-- renaming an agent mid-teardown would otherwise let it sort behind the cursor
-- and escape the sweep. Uses idx_agent_workspace_id_keyset (migration 281), which
-- exists so this page does not sort the whole owner set.
SELECT id FROM agent
WHERE agent.workspace_id = $1 AND id > $2
ORDER BY id
LIMIT $3;

-- name: ListWorkspaceIssueIDFirstPage :many
-- First page of the same walk. Split from the keyset query rather than seeded
-- with the all-zero uuid, because that value is itself a valid uuid: a row whose
-- id happened to be all zeros would be skipped forever by `id > $2`.
SELECT id FROM issue
WHERE issue.workspace_id = $1
ORDER BY id
LIMIT $2;

-- name: ListWorkspaceIssueIDPage :many
-- Uses idx_issue_workspace_id_keyset (migration 282).
SELECT id FROM issue
WHERE issue.workspace_id = $1 AND id > $2
ORDER BY id
LIMIT $3;

-- name: ListWorkspaceRuntimeIDFirstPage :many
-- First page of the same walk. Split from the keyset query rather than seeded
-- with the all-zero uuid, because that value is itself a valid uuid: a row whose
-- id happened to be all zeros would be skipped forever by `id > $2`.
SELECT id FROM agent_runtime
WHERE agent_runtime.workspace_id = $1
ORDER BY id
LIMIT $2;

-- name: ListWorkspaceRuntimeIDPage :many
-- Uses idx_agent_runtime_workspace_id_keyset (migration 283).
SELECT id FROM agent_runtime
WHERE agent_runtime.workspace_id = $1 AND id > $2
ORDER BY id
LIMIT $3;

-- name: ListTaskIDsByAgentFirstPage :many
-- First page of the same walk; see ListWorkspaceAgentIDFirstPage for why this is
-- a separate query rather than a zero-uuid cursor, and ListTaskIDsByAgentPage for
-- the single-key / keyset / FOR UPDATE rationale.
SELECT id FROM agent_task_queue
WHERE agent_id = $1
ORDER BY id
LIMIT $2
FOR UPDATE;

-- name: ListTaskIDsByAgentPage :many
-- One owner key per call, one keyset-paged chunk per call.
--
-- Single-key equality is deliberate. All three ownership paths must stay (nothing
-- enforces that a task's agent/runtime/issue share a workspace), but expressing
-- them as `OR agent_id IN (subquery) OR …` — or as a UNION of joins, or as
-- `= ANY($1::uuid[])` over a whole workspace's agents — makes the planner
-- estimate a proportional share of the global table and fall back to a Seq Scan
-- on agent_task_queue. A single-key equality is the only form whose row estimate
-- stays small enough to be index-driven regardless of how the workspace's tasks
-- are distributed (MUL-5999).
--
-- `id > $2 ORDER BY id LIMIT $3` against idx_agent_task_queue_agent_id_keyset
-- (migration 278) bounds the SCAN, not just the result: each page is an index
-- range scan that starts where the previous one stopped. Bounding only the result
-- — `ORDER BY id LIMIT n` against the (agent_id, status) index — puts a Sort over
-- the agent's entire task set in front of every page, so a busy agent costs
-- roughly O(N² / page) for the sweep.
--
-- FOR UPDATE is load-bearing for correctness, not throughput. It locks the rows
-- this page is about to delete, and under READ COMMITTED PostgreSQL re-checks the
-- WHERE clause after acquiring each lock: a task a concurrent committed UPDATE
-- has already moved to another owner no longer matches and is skipped, and one we
-- did lock cannot be moved away before we delete it.
SELECT id FROM agent_task_queue
WHERE agent_id = $1 AND id > $2
ORDER BY id
LIMIT $3
FOR UPDATE;

-- name: ListTaskIDsByIssueFirstPage :many
-- First page of the same walk; see ListWorkspaceAgentIDFirstPage for why this is
-- a separate query rather than a zero-uuid cursor, and ListTaskIDsByAgentPage for
-- the single-key / keyset / FOR UPDATE rationale.
SELECT id FROM agent_task_queue
WHERE issue_id = $1
ORDER BY id
LIMIT $2
FOR UPDATE;

-- name: ListTaskIDsByIssuePage :many
-- Uses idx_agent_task_queue_issue_id_keyset (migration 279). See
-- ListTaskIDsByAgentPage for the single-key / keyset / FOR UPDATE rationale.
SELECT id FROM agent_task_queue
WHERE issue_id = $1 AND id > $2
ORDER BY id
LIMIT $3
FOR UPDATE;

-- name: ListTaskIDsByRuntimeFirstPage :many
-- First page of the same walk; see ListWorkspaceAgentIDFirstPage for why this is
-- a separate query rather than a zero-uuid cursor, and ListTaskIDsByAgentPage for
-- the single-key / keyset / FOR UPDATE rationale.
SELECT id FROM agent_task_queue
WHERE runtime_id = $1
ORDER BY id
LIMIT $2
FOR UPDATE;

-- name: ListTaskIDsByRuntimePage :many
-- Uses idx_agent_task_queue_runtime_id (migration 273, (runtime_id, id)); before
-- it every runtime_id index was partial, so this path had none at all. See
-- ListTaskIDsByAgentPage for the single-key / keyset / FOR UPDATE rationale.
SELECT id FROM agent_task_queue
WHERE runtime_id = $1 AND id > $2
ORDER BY id
LIMIT $3
FOR UPDATE;

-- name: DetachTaskBatchReferences :exec
-- Break inbound task parent references before deleting the batch in the
-- application layer rather than through the FK's ON DELETE SET NULL.
UPDATE agent_task_queue
SET parent_task_id = NULL
WHERE parent_task_id = ANY(@task_ids::uuid[]);

-- name: DeleteTaskBatch :exec
-- Deletes one bounded batch of tasks together with everything that hangs off
-- them, every arm keyed by task_id against an existing index, then the task rows
-- by primary key. The legacy FK cascades stay a safety net only: this statement
-- is what actually removes the rows, so teardown keeps working when they go.
WITH
batch AS MATERIALIZED (
    SELECT id FROM unnest(@task_ids::uuid[]) AS t(id)
),
deleted_task_usage AS (
    DELETE FROM task_usage WHERE task_id IN (SELECT id FROM batch)
),
deleted_task_messages AS (
    DELETE FROM task_message WHERE task_id IN (SELECT id FROM batch)
),
deleted_task_tokens AS (
    DELETE FROM task_token WHERE task_id IN (SELECT id FROM batch)
)
DELETE FROM agent_task_queue WHERE id IN (SELECT id FROM batch);

-- name: DeleteTaskTokensByAgent :exec
-- The third explicit task_token path. workspace_id (in DeleteWorkspaceLeafData)
-- and task_id (in DeleteTaskBatch) do not cover a token whose own workspace_id
-- points at a neighbour while its agent lives here, and teardown must not fall
-- back on the agent_id cascade for it. Uses idx_task_token_agent_id
-- (migration 275); one agent key per call for the same planner reason as
-- ListTaskIDsByAgentBatch.
DELETE FROM task_token WHERE agent_id = $1;

-- name: PrepareWorkspaceDeletionLinks :exec
-- Break self-references and the task/run cycle explicitly before deleting
-- either side. This keeps their ordering in the application-owned graph
-- instead of relying on ON DELETE SET NULL actions.
--
-- The task half of this step now lives in DetachTaskBatchReferences, which runs
-- once per bounded batch instead of once for a whole workspace's task set.
WITH
detached_comments AS (
    UPDATE comment
    SET parent_id = NULL
    WHERE comment.workspace_id = $1
      AND parent_id IS NOT NULL
)
UPDATE issue
SET parent_issue_id = NULL
WHERE issue.workspace_id = $1
  AND parent_issue_id IS NOT NULL;

-- name: DeleteWorkspaceLeafData :exec
-- Everything task-keyed moved to DeleteTaskBatch, which runs in bounded batches
-- before this step; what is left is keyed by the workspace or by one of the
-- workspace-scoped id sets below.
WITH
delete_target AS (
    SELECT $1::uuid AS target_id
),
ws_agents AS MATERIALIZED (
    SELECT id FROM agent WHERE workspace_id = (SELECT target_id FROM delete_target)
),
ws_issues AS MATERIALIZED (
    SELECT id FROM issue WHERE workspace_id = (SELECT target_id FROM delete_target)
),
ws_labels AS MATERIALIZED (
    SELECT id FROM issue_label WHERE workspace_id = (SELECT target_id FROM delete_target)
),
ws_skills AS MATERIALIZED (
    SELECT id FROM skill WHERE workspace_id = (SELECT target_id FROM delete_target)
),
ws_github_prs AS MATERIALIZED (
    SELECT id FROM github_pull_request WHERE workspace_id = (SELECT target_id FROM delete_target)
),
ws_vcs_prs AS MATERIALIZED (
    SELECT id FROM vcs_pull_request WHERE workspace_id = (SELECT target_id FROM delete_target)
),
ws_vcs_connections AS MATERIALIZED (
    SELECT id FROM vcs_connection WHERE workspace_id = (SELECT target_id FROM delete_target)
),
-- One of three explicit task_token paths. This is the workspace-keyed one;
-- DeleteTaskBatch covers task_id and DeleteTaskTokensByAgent covers agent_id, so
-- a token whose workspace_id points at a neighbour while its task or agent lives
-- here is still removed by this teardown rather than by the FK cascade. The
-- former single statement combined all three with OR, which cost a full scan of
-- task_token (MUL-5999); split, each path is an index scan.
deleted_task_tokens AS (
    DELETE FROM task_token
    WHERE workspace_id = (SELECT target_id FROM delete_target)
),
deleted_hourly_dirty AS (
    DELETE FROM task_usage_hourly_dirty WHERE workspace_id = (SELECT target_id FROM delete_target)
),
deleted_hourly AS (
    DELETE FROM task_usage_hourly WHERE workspace_id = (SELECT target_id FROM delete_target)
),
deleted_attachments AS (
    DELETE FROM attachment WHERE workspace_id = (SELECT target_id FROM delete_target)
),
deleted_activity AS (
    DELETE FROM activity_log WHERE workspace_id = (SELECT target_id FROM delete_target)
),
deleted_issue_dependencies AS (
    DELETE FROM issue_dependency
    WHERE issue_id IN (SELECT id FROM ws_issues)
       OR depends_on_issue_id IN (SELECT id FROM ws_issues)
),
deleted_issue_labels AS (
    DELETE FROM issue_to_label
    WHERE issue_id IN (SELECT id FROM ws_issues)
       OR label_id IN (SELECT id FROM ws_labels)
),
deleted_agent_labels AS (
    DELETE FROM agent_to_label
    WHERE agent_id IN (SELECT id FROM ws_agents)
       OR label_id IN (SELECT id FROM ws_labels)
),
deleted_skill_labels AS (
    DELETE FROM skill_to_label
    WHERE skill_id IN (SELECT id FROM ws_skills)
       OR label_id IN (SELECT id FROM ws_labels)
),
deleted_issue_github_links AS (
    DELETE FROM issue_pull_request
    WHERE issue_id IN (SELECT id FROM ws_issues)
       OR pull_request_id IN (SELECT id FROM ws_github_prs)
),
deleted_issue_vcs_links AS (
    DELETE FROM issue_vcs_pull_request
    WHERE issue_id IN (SELECT id FROM ws_issues)
       OR pull_request_id IN (SELECT id FROM ws_vcs_prs)
),
deleted_agent_invocation_targets AS (
    DELETE FROM agent_invocation_target
    WHERE agent_id IN (SELECT id FROM ws_agents)
),
deleted_agent_skills AS (
    DELETE FROM agent_skill
    WHERE agent_id IN (SELECT id FROM ws_agents)
       OR skill_id IN (SELECT id FROM ws_skills)
),
deleted_skill_files AS (
    DELETE FROM skill_file
    WHERE skill_id IN (SELECT id FROM ws_skills)
),
deleted_daemon_connections AS (
    DELETE FROM daemon_connection
    WHERE agent_id IN (SELECT id FROM ws_agents)
),
deleted_project_resources AS (
    DELETE FROM project_resource WHERE workspace_id = (SELECT target_id FROM delete_target)
),
deleted_github_check_runs AS (
    DELETE FROM github_pull_request_check_run
    WHERE pr_id IN (SELECT id FROM ws_github_prs)
),
deleted_github_check_suites AS (
    DELETE FROM github_pull_request_check_suite
    WHERE pr_id IN (SELECT id FROM ws_github_prs)
),
deleted_pending_github_suites AS (
    DELETE FROM github_pending_check_suite WHERE workspace_id = (SELECT target_id FROM delete_target)
),
deleted_vcs_commit_statuses AS (
    DELETE FROM vcs_commit_status
    WHERE connection_id IN (SELECT id FROM ws_vcs_connections)
)
SELECT 1;

-- name: DeleteWorkspaceComments :exec
DELETE FROM comment WHERE comment.workspace_id = $1;

-- name: DeleteWorkspaceOrchestrationData :exec
-- Orchestration tables intentionally have no database cascades. Remove the
-- relationship graph before deleting the Issue rows that provide Mission and
-- TaskNode identity, so workspace teardown cannot leave cross-domain history.
WITH
deleted_human_gates AS (
    DELETE FROM orchestration_human_gate WHERE orchestration_human_gate.workspace_id = $1
),
deleted_review_verdicts AS (
    DELETE FROM review_verdict WHERE review_verdict.workspace_id = $1
),
deleted_artifacts AS (
    DELETE FROM artifact WHERE artifact.workspace_id = $1
),
deleted_activities AS (
    DELETE FROM orchestration_activity WHERE orchestration_activity.workspace_id = $1
),
deleted_runs AS (
    DELETE FROM orchestration_run WHERE orchestration_run.workspace_id = $1
),
deleted_assignments AS (
    DELETE FROM orchestration_assignment WHERE orchestration_assignment.workspace_id = $1
),
deleted_role_policy_snapshots AS (
    DELETE FROM mission_role_policy_snapshot
    WHERE mission_role_policy_snapshot.workspace_id = $1
),
deleted_role_profiles AS (
    DELETE FROM role_profile WHERE role_profile.workspace_id = $1
),
deleted_task_nodes AS (
    DELETE FROM task_node WHERE task_node.workspace_id = $1
)
DELETE FROM mission WHERE mission.workspace_id = $1;

-- name: DeleteWorkspaceIssueRoots :exec
WITH
deleted_issues AS (
    DELETE FROM issue WHERE issue.workspace_id = $1
),
deleted_labels AS (
    DELETE FROM issue_label WHERE issue_label.workspace_id = $1
)
DELETE FROM quick_action WHERE quick_action.workspace_id = $1;

-- name: DeleteWorkspacePullRequests :exec
WITH deleted_github_prs AS (
    DELETE FROM github_pull_request
    WHERE github_pull_request.workspace_id = $1
)
DELETE FROM vcs_pull_request WHERE vcs_pull_request.workspace_id = $1;

-- name: DeleteWorkspaceConnections :exec
WITH deleted_github_installations AS (
    DELETE FROM github_installation
    WHERE github_installation.workspace_id = $1
)
DELETE FROM vcs_connection WHERE vcs_connection.workspace_id = $1;

-- name: DeleteWorkspaceSkills :exec
DELETE FROM skill WHERE skill.workspace_id = $1;

-- name: DeleteWorkspaceAgents :exec
DELETE FROM agent WHERE agent.workspace_id = $1;

-- name: DeleteWorkspaceRuntimesAndProjects :exec
WITH
deleted_runtimes AS (
    DELETE FROM agent_runtime WHERE agent_runtime.workspace_id = $1
),
deleted_profiles AS (
    DELETE FROM runtime_profile WHERE runtime_profile.workspace_id = $1
)
DELETE FROM project WHERE project.workspace_id = $1;

-- name: DeleteWorkspaceAdministration :exec
WITH
deleted_members AS (
    DELETE FROM member WHERE member.workspace_id = $1
),
deleted_daemon_tokens AS (
    DELETE FROM daemon_token WHERE daemon_token.workspace_id = $1
),
detached_client_usage AS (
    UPDATE client_usage_daily
    SET workspace_id = NULL
    WHERE client_usage_daily.workspace_id = $1
)
DELETE FROM workspace_invitation
WHERE workspace_invitation.workspace_id = $1;
