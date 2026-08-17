-- name: GetMissionInWorkspace :one
SELECT * FROM mission
WHERE issue_id = sqlc.arg('issue_id')
  AND workspace_id = sqlc.arg('workspace_id');

-- name: LockMissionInWorkspace :one
SELECT * FROM mission
WHERE issue_id = sqlc.arg('issue_id')
  AND workspace_id = sqlc.arg('workspace_id')
FOR UPDATE;

-- name: CreateMissionRecord :one
INSERT INTO mission (
    issue_id, workspace_id, status, limits, created_by
) VALUES (
    sqlc.arg('issue_id'), sqlc.arg('workspace_id'), 'draft',
    sqlc.arg('limits'), sqlc.arg('created_by')
)
RETURNING *;

-- name: AcceptMissionPlan :one
UPDATE mission
SET status = 'ready',
    plan_key = sqlc.arg('plan_key'),
    plan_schema_version = sqlc.arg('plan_schema_version'),
    plan = sqlc.arg('plan'),
    limits = sqlc.arg('limits'),
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('issue_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'draft'
  AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: StartMissionRecord :one
UPDATE mission
SET status = 'running',
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('issue_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'ready'
  AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: CancelMissionRecord :one
UPDATE mission
SET status = 'cancelled',
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('issue_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status IN ('draft', 'ready', 'running', 'blocked')
  AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: AllocateMissionActivitySequence :one
UPDATE mission
SET next_activity_sequence = next_activity_sequence + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('issue_id')
  AND workspace_id = sqlc.arg('workspace_id')
RETURNING (next_activity_sequence - 1)::bigint;

-- name: CreateTaskNodeRecord :one
INSERT INTO task_node (
    issue_id, workspace_id, mission_id, node_key, role,
    acceptance_criteria, artifact_kinds, priority, status,
    budget_estimate_tokens, budget_estimate_cost_usd_ticks
) VALUES (
    sqlc.arg('issue_id'), sqlc.arg('workspace_id'), sqlc.arg('mission_id'),
    sqlc.arg('node_key'), sqlc.arg('role'), sqlc.arg('acceptance_criteria'),
    sqlc.arg('artifact_kinds'), sqlc.arg('priority'), 'pending',
    sqlc.arg('budget_estimate_tokens'), sqlc.arg('budget_estimate_cost_usd_ticks')
)
RETURNING *;

-- name: GetMissionBudgetUsage :one
WITH per_run AS (
    SELECT
        run.id,
        run.status,
        node.budget_estimate_tokens,
        node.budget_estimate_cost_usd_ticks,
        COALESCE(SUM(
            COALESCE(usage.input_tokens, 0)
            + COALESCE(usage.output_tokens, 0)
            + COALESCE(usage.cache_read_tokens, 0)
            + COALESCE(usage.cache_write_tokens, 0)
        ), 0)::bigint AS actual_tokens,
        COALESCE(SUM(usage.cost_usd_ticks) FILTER (WHERE usage.cost_usd_ticks IS NOT NULL), 0)::bigint AS actual_cost_usd_ticks,
        COUNT(usage.task_id)::bigint AS usage_rows,
        COUNT(usage.cost_usd_ticks) FILTER (WHERE usage.cost_usd_ticks IS NOT NULL)::bigint AS authoritative_cost_rows
    FROM orchestration_run run
    JOIN task_node node
      ON node.issue_id = run.task_node_id
     AND node.workspace_id = run.workspace_id
     AND node.mission_id = run.mission_id
    LEFT JOIN agent_task_queue task
      ON task.orchestration_run_id = run.id
    LEFT JOIN task_usage usage
      ON usage.task_id = task.id
    WHERE run.workspace_id = sqlc.arg('workspace_id')
      AND run.mission_id = sqlc.arg('mission_id')
    GROUP BY run.id, run.status, node.budget_estimate_tokens, node.budget_estimate_cost_usd_ticks
)
SELECT
    COALESCE(SUM(actual_tokens), 0)::bigint AS consumed_tokens,
    COALESCE(SUM(CASE
        WHEN status IN ('queued', 'dispatched', 'running')
            THEN GREATEST(budget_estimate_tokens - actual_tokens, 0)
        WHEN usage_rows = 0 THEN budget_estimate_tokens
        ELSE 0
    END), 0)::bigint AS reserved_tokens,
    COALESCE(SUM(actual_cost_usd_ticks), 0)::bigint AS consumed_cost_usd_ticks,
    COALESCE(SUM(CASE
        WHEN status IN ('queued', 'dispatched', 'running')
            THEN GREATEST(budget_estimate_cost_usd_ticks - actual_cost_usd_ticks, 0)
        WHEN authoritative_cost_rows = 0 THEN budget_estimate_cost_usd_ticks
        ELSE 0
    END), 0)::bigint AS reserved_cost_usd_ticks
FROM per_run;

-- name: SetMissionBudgetGate :one
UPDATE mission
SET status = 'blocked',
    budget_gate_status = sqlc.arg('budget_gate_status'),
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('mission_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status IN ('running', 'blocked')
RETURNING *;

-- name: ApproveMissionBudgetRecord :one
UPDATE mission
SET status = 'running',
    budget_gate_status = 'approved',
    budget_grant_tokens = budget_grant_tokens + sqlc.arg('grant_tokens'),
    budget_grant_cost_usd_ticks = budget_grant_cost_usd_ticks + sqlc.arg('grant_cost_usd_ticks'),
    budget_approved_by = sqlc.arg('approved_by'),
    budget_approved_at = now(),
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('mission_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'blocked'
  AND budget_gate_status = 'approval_required'
  AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: ListTaskNodesByMission :many
SELECT * FROM task_node
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
ORDER BY priority DESC, created_at ASC, node_key ASC;

-- name: MarkTaskNodeReady :one
UPDATE task_node
SET status = 'ready',
    block_reason = NULL,
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('issue_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status = 'pending'
  AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: CancelMissionTaskNodes :many
UPDATE task_node
SET status = 'cancelled',
    revision = revision + 1,
    updated_at = now()
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status NOT IN ('completed', 'failed', 'cancelled')
RETURNING *;

-- name: RevokeMissionAssignments :many
UPDATE orchestration_assignment
SET status = 'revoked',
    ended_at = now()
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status = 'active'
RETURNING *;

-- name: ListActiveMissionRuns :many
SELECT * FROM orchestration_run
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status IN ('queued', 'dispatched', 'running')
ORDER BY created_at ASC, id ASC;

-- name: LockOrchestrationRunForEnqueue :one
SELECT
    run.id AS run_id,
    run.mission_id,
    run.task_node_id,
    run.assignment_id,
    run.purpose,
    run.attempt,
    run.status AS run_status,
    assignment.agent_id,
    assignment.runtime_id,
    node.priority AS task_priority
FROM orchestration_run run
JOIN orchestration_assignment assignment
  ON assignment.id = run.assignment_id
 AND assignment.workspace_id = run.workspace_id
 AND assignment.mission_id = run.mission_id
 AND assignment.task_node_id = run.task_node_id
JOIN task_node node
  ON node.issue_id = run.task_node_id
 AND node.workspace_id = run.workspace_id
 AND node.mission_id = run.mission_id
WHERE run.id = sqlc.arg('run_id')
  AND run.workspace_id = sqlc.arg('workspace_id')
  AND run.status = 'queued'
  AND assignment.status = 'active'
  AND (
    (run.purpose = 'review' AND node.status = 'review')
    OR
    (run.purpose IN ('execute', 'integrate') AND node.status = 'assigned')
  )
FOR UPDATE OF run;

-- name: LockAgentForOrchestrationEnqueue :one
SELECT * FROM agent
WHERE id = sqlc.arg('agent_id')
  AND workspace_id = sqlc.arg('workspace_id')
FOR UPDATE;

-- name: GetOrchestrationRunInWorkspace :one
SELECT * FROM orchestration_run
WHERE id = sqlc.arg('run_id')
  AND workspace_id = sqlc.arg('workspace_id');

-- name: GetOrchestrationRunExecutionInWorkspace :one
SELECT task.*
FROM agent_task_queue task
JOIN orchestration_run run
  ON run.id = task.orchestration_run_id
WHERE run.id = sqlc.arg('run_id')
  AND run.workspace_id = sqlc.arg('workspace_id');

-- name: ListReconcilableOrchestrationRunsAfter :many
SELECT run.id, run.workspace_id, run.created_at
FROM orchestration_run run
WHERE (run.created_at, run.id) > (
        sqlc.arg('after_created_at')::timestamptz,
        sqlc.arg('after_run_id')::uuid
      )
  AND (
    run.status IN ('queued', 'dispatched', 'running')
    OR (
      run.status IN ('failed', 'cancelled')
      AND EXISTS (
        SELECT 1
        FROM agent_task_queue task
        WHERE task.orchestration_run_id = run.id
          AND task.status IN ('queued', 'deferred', 'dispatched', 'waiting_local_directory', 'running')
      )
    )
  )
ORDER BY run.created_at ASC, run.id ASC
LIMIT sqlc.arg('batch_size');

-- name: LockOrchestrationRunForReconcile :one
SELECT * FROM orchestration_run
WHERE id = sqlc.arg('run_id')
  AND workspace_id = sqlc.arg('workspace_id')
FOR UPDATE;

-- name: LockTaskNodeForReconcile :one
SELECT * FROM task_node
WHERE issue_id = sqlc.arg('task_node_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
FOR UPDATE;

-- name: GetTaskNodeInMission :one
SELECT * FROM task_node
WHERE issue_id = sqlc.arg('task_node_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id');

-- name: ResumeMissionForTaskRetry :one
UPDATE mission
SET status = 'running',
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('mission_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status IN ('running', 'blocked')
  AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: RetryBlockedTaskNode :one
UPDATE task_node
SET status = 'pending',
    block_reason = NULL,
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('task_node_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status = 'blocked'
  AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: LockAgentTaskByOrchestrationRun :one
SELECT task.*
FROM agent_task_queue task
JOIN issue task_issue
  ON task_issue.id = task.issue_id
 AND task_issue.workspace_id = sqlc.arg('workspace_id')
WHERE task.orchestration_run_id = sqlc.arg('run_id')
FOR UPDATE OF task;

-- name: UpdateOrchestrationRunFromReconcile :one
UPDATE orchestration_run
SET status = sqlc.arg('target_status'),
    failure_kind = sqlc.narg('failure_kind'),
    failure_message = sqlc.narg('failure_message'),
    started_at = COALESCE(started_at, sqlc.narg('started_at')),
    finished_at = sqlc.narg('finished_at')
WHERE id = sqlc.arg('run_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status = sqlc.arg('expected_status')
RETURNING *;

-- name: UpdateTaskNodeFromReconcile :one
UPDATE task_node
SET status = sqlc.arg('target_status'),
    block_reason = sqlc.narg('block_reason'),
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('task_node_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status = sqlc.arg('expected_status')
RETURNING *;

-- name: RevokeAssignmentFromReconcile :one
UPDATE orchestration_assignment
SET status = 'revoked',
    ended_at = now()
WHERE id = sqlc.arg('assignment_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND task_node_id = sqlc.arg('task_node_id')
  AND status = 'active'
RETURNING *;

-- name: ListOrchestrationAssignmentsByMission :many
SELECT * FROM orchestration_assignment
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
ORDER BY task_node_id, role, sequence;

-- name: ListOrchestrationRunsByMission :many
SELECT * FROM orchestration_run
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
ORDER BY created_at, id;

-- name: ListArtifactsByMission :many
SELECT * FROM artifact
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
ORDER BY task_node_id, kind, version, created_at, id;

-- name: ListReviewVerdictsByMission :many
SELECT * FROM review_verdict
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
ORDER BY created_at, id;

-- name: SelectOrchestrationAgentCandidate :one
SELECT agent.id AS agent_id, agent.runtime_id
FROM agent
JOIN agent_runtime runtime
  ON runtime.id = agent.runtime_id
 AND runtime.workspace_id = agent.workspace_id
WHERE agent.workspace_id = sqlc.arg('workspace_id')
  AND agent.archived_at IS NULL
  AND agent.runtime_id IS NOT NULL
  AND runtime.status = 'online'
  AND (
    (SELECT count(*) FROM orchestration_run active_run
     JOIN orchestration_assignment active_assignment ON active_assignment.id = active_run.assignment_id
     WHERE active_assignment.agent_id = agent.id
       AND active_run.status IN ('queued', 'dispatched', 'running'))
    +
    (SELECT count(*) FROM agent_task_queue active_task
     WHERE active_task.agent_id = agent.id
       AND active_task.orchestration_run_id IS NULL
       AND active_task.status IN ('queued', 'deferred', 'dispatched', 'waiting_local_directory', 'running'))
  ) < agent.max_concurrent_tasks
ORDER BY (agent.id = sqlc.arg('avoid_agent_id')::uuid) ASC,
         agent.created_at ASC,
         agent.id ASC
LIMIT 1
FOR UPDATE OF agent;

-- name: CreateOrchestrationAssignment :one
INSERT INTO orchestration_assignment (
    workspace_id, mission_id, task_node_id, role, agent_id, runtime_id,
    status, sequence, supersedes_id, created_by
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('mission_id'), sqlc.arg('task_node_id'),
    sqlc.arg('role'), sqlc.arg('agent_id'), sqlc.arg('runtime_id'), 'active',
    sqlc.arg('sequence'), sqlc.narg('supersedes_id'), sqlc.narg('created_by')
)
RETURNING *;

-- name: CreateOrchestrationRun :one
INSERT INTO orchestration_run (
    workspace_id, mission_id, task_node_id, assignment_id, purpose,
    attempt, status, input, retry_of_id, dispatch_deadline_at, timeout_seconds
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('mission_id'), sqlc.arg('task_node_id'),
    sqlc.arg('assignment_id'), sqlc.arg('purpose'), sqlc.arg('attempt'), 'queued',
    sqlc.arg('input'), sqlc.narg('retry_of_id'), sqlc.arg('dispatch_deadline_at'),
    sqlc.arg('timeout_seconds')
)
RETURNING *;

-- name: TransitionTaskNodeState :one
UPDATE task_node
SET status = sqlc.arg('target_status'),
    block_reason = sqlc.narg('block_reason'),
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('task_node_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status = sqlc.arg('expected_status')
RETURNING *;

-- name: TransitionTaskNodeForRework :one
UPDATE task_node
SET status = sqlc.arg('target_status'),
    block_reason = NULL,
    rework_count = rework_count + 1,
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('task_node_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status = 'review'
  AND rework_count = sqlc.arg('expected_rework_count')
RETURNING *;

-- name: TransitionMissionState :one
UPDATE mission
SET status = sqlc.arg('target_status'),
    revision = revision + 1,
    updated_at = now()
WHERE issue_id = sqlc.arg('mission_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status = sqlc.arg('expected_status')
RETURNING *;

-- name: EndOrchestrationAssignment :one
UPDATE orchestration_assignment
SET status = sqlc.arg('target_status'),
    ended_at = now()
WHERE id = sqlc.arg('assignment_id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND status = 'active'
RETURNING *;

-- name: ListQueuedOrchestrationRunsWithoutTask :many
SELECT run.*
FROM orchestration_run run
LEFT JOIN agent_task_queue task ON task.orchestration_run_id = run.id
WHERE run.workspace_id = sqlc.arg('workspace_id')
  AND run.mission_id = sqlc.arg('mission_id')
  AND run.status = 'queued'
  AND task.id IS NULL
ORDER BY run.created_at, run.id;

-- name: GetOrchestrationAssignmentInWorkspace :one
SELECT * FROM orchestration_assignment
WHERE id = sqlc.arg('assignment_id')
  AND workspace_id = sqlc.arg('workspace_id');

-- name: GetArtifactInWorkspace :one
SELECT * FROM artifact
WHERE id = sqlc.arg('artifact_id')
  AND workspace_id = sqlc.arg('workspace_id');

-- name: GetReviewVerdictByRun :one
SELECT * FROM review_verdict
WHERE review_run_id = sqlc.arg('review_run_id');

-- name: NextArtifactVersion :one
SELECT COALESCE(max(version), 0)::int + 1
FROM artifact
WHERE workspace_id = sqlc.arg('workspace_id')
  AND task_node_id = sqlc.arg('task_node_id')
  AND kind = sqlc.arg('kind');

-- name: CreateArtifactRecord :one
INSERT INTO artifact (
    workspace_id, mission_id, task_node_id, run_id, kind, version,
    uri, content_hash, summary, metadata
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('mission_id'), sqlc.arg('task_node_id'),
    sqlc.arg('run_id'), sqlc.arg('kind'), sqlc.arg('version'), sqlc.arg('uri'),
    sqlc.narg('content_hash'), sqlc.arg('summary'), sqlc.arg('metadata')
)
RETURNING *;

-- name: CreateReviewVerdictRecord :one
INSERT INTO review_verdict (
    workspace_id, mission_id, task_node_id, review_run_id, artifact_id,
    decision, evidence, requested_changes
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('mission_id'), sqlc.arg('task_node_id'),
    sqlc.arg('review_run_id'), sqlc.arg('artifact_id'), sqlc.arg('decision'),
    sqlc.arg('evidence'), sqlc.arg('requested_changes')
)
RETURNING *;

-- name: CreateOrchestrationIssueDependency :one
INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
VALUES (
    sqlc.arg('issue_id'), sqlc.arg('depends_on_issue_id'), 'blocked_by'
)
RETURNING *;

-- name: CreateOrchestrationActivity :one
INSERT INTO orchestration_activity (
    workspace_id, mission_id, task_node_id, run_id, type,
    actor_type, actor_id, subject_type, subject_id,
    causation_id, correlation_id, payload_version, payload,
    dedupe_key, sequence
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('mission_id'), sqlc.narg('task_node_id'),
    sqlc.narg('run_id'), sqlc.arg('type'), sqlc.arg('actor_type'),
    sqlc.narg('actor_id'), sqlc.arg('subject_type'), sqlc.arg('subject_id'),
    sqlc.arg('causation_id'), sqlc.arg('correlation_id'),
    sqlc.arg('payload_version'), sqlc.arg('payload'), sqlc.arg('dedupe_key'),
    sqlc.arg('sequence')
)
RETURNING *;

-- name: GetOrchestrationActivityByDedupeKey :one
SELECT * FROM orchestration_activity
WHERE workspace_id = sqlc.arg('workspace_id')
  AND dedupe_key = sqlc.arg('dedupe_key');

-- name: ListOrchestrationActivitiesByCausation :many
SELECT * FROM orchestration_activity
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND causation_id = sqlc.arg('causation_id')
ORDER BY sequence ASC;

-- name: ListOrchestrationActivitiesAfterSequence :many
SELECT * FROM orchestration_activity
WHERE workspace_id = sqlc.arg('workspace_id')
  AND mission_id = sqlc.arg('mission_id')
  AND sequence > sqlc.arg('after_sequence')
ORDER BY sequence ASC
LIMIT sqlc.arg('page_size');

-- name: ListRecentOrchestrationActivities :many
SELECT *
FROM (
    SELECT * FROM orchestration_activity
    WHERE workspace_id = sqlc.arg('workspace_id')
      AND mission_id = sqlc.arg('mission_id')
    ORDER BY sequence DESC
    LIMIT sqlc.arg('page_size')
) recent
ORDER BY sequence ASC;

-- name: ListOrchestrationIssueDependencies :many
SELECT dependency.*
FROM issue_dependency dependency
JOIN task_node node ON node.issue_id = dependency.issue_id
WHERE node.workspace_id = sqlc.arg('workspace_id')
  AND node.mission_id = sqlc.arg('mission_id')
  AND dependency.type = 'blocked_by'
ORDER BY dependency.issue_id, dependency.depends_on_issue_id;
