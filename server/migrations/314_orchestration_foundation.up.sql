-- Orchestration domain foundation for the DIY mission walking skeleton.
--
-- Mission and task_node extend existing issue rows by identity. Relationships
-- are application-owned: this migration deliberately adds no foreign keys or
-- cascades. Explicit indexes live in follow-up concurrent migrations.

CREATE TABLE mission (
    issue_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (
        status IN ('draft', 'ready', 'running', 'blocked', 'completed', 'failed', 'cancelled')
    ),
    plan_key TEXT,
    plan_schema_version INTEGER,
    plan JSONB,
    limits JSONB NOT NULL DEFAULT '{"max_parallel_runs":2,"max_task_attempts":2,"max_rework_cycles":1}'::jsonb
        CHECK (jsonb_typeof(limits) = 'object'),
    next_activity_sequence BIGINT NOT NULL DEFAULT 1 CHECK (next_activity_sequence > 0),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (plan_key IS NULL AND plan_schema_version IS NULL AND plan IS NULL)
        OR
        (
            char_length(plan_key) BETWEEN 1 AND 255
            AND plan_schema_version = 1
            AND jsonb_typeof(plan) = 'object'
        )
    )
);

CREATE TABLE task_node (
    issue_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    node_key TEXT NOT NULL CHECK (char_length(node_key) BETWEEN 1 AND 64),
    role TEXT NOT NULL CHECK (role IN ('executor', 'integrator')),
    acceptance_criteria JSONB NOT NULL CHECK (jsonb_typeof(acceptance_criteria) = 'array'),
    artifact_kinds JSONB NOT NULL CHECK (jsonb_typeof(artifact_kinds) = 'array'),
    priority INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN (
            'pending', 'ready', 'assigned', 'running', 'review', 'rework',
            'blocked', 'completed', 'failed', 'cancelled'
        )
    ),
    block_reason TEXT,
    rework_count INTEGER NOT NULL DEFAULT 0 CHECK (rework_count >= 0),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE orchestration_assignment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    task_node_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('executor', 'reviewer', 'integrator')),
    agent_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active', 'fulfilled', 'superseded', 'revoked')
    ),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    supersedes_id UUID,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ,
    CHECK (
        (status = 'active' AND ended_at IS NULL)
        OR
        (status <> 'active' AND ended_at IS NOT NULL)
    )
);

CREATE TABLE orchestration_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    task_node_id UUID NOT NULL,
    assignment_id UUID NOT NULL,
    purpose TEXT NOT NULL CHECK (purpose IN ('execute', 'review', 'integrate')),
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    status TEXT NOT NULL DEFAULT 'queued' CHECK (
        status IN ('queued', 'dispatched', 'running', 'succeeded', 'failed', 'cancelled')
    ),
    input JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(input) = 'object'),
    retry_of_id UUID,
    failure_kind TEXT,
    failure_message TEXT,
    dispatch_deadline_at TIMESTAMPTZ NOT NULL,
    timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds > 0),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (status IN ('succeeded', 'failed', 'cancelled') AND finished_at IS NOT NULL)
        OR
        (status NOT IN ('succeeded', 'failed', 'cancelled') AND finished_at IS NULL)
    )
);

CREATE TABLE artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    task_node_id UUID NOT NULL,
    run_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (
        kind IN ('branch', 'commit', 'diff', 'file', 'test_receipt', 'final_delivery')
    ),
    version INTEGER NOT NULL CHECK (version > 0),
    uri TEXT NOT NULL CHECK (char_length(uri) BETWEEN 1 AND 4096),
    content_hash TEXT,
    summary TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE review_verdict (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    task_node_id UUID NOT NULL,
    review_run_id UUID NOT NULL,
    artifact_id UUID NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'changes_requested', 'rejected')),
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence) = 'object'),
    requested_changes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (
        jsonb_typeof(requested_changes) = 'array'
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE orchestration_activity (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    task_node_id UUID,
    run_id UUID,
    type TEXT NOT NULL CHECK (char_length(type) BETWEEN 1 AND 128),
    actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'orchestrator', 'agent', 'runtime')),
    actor_id UUID,
    subject_type TEXT NOT NULL CHECK (
        subject_type IN ('mission', 'task_node', 'assignment', 'run', 'artifact', 'review')
    ),
    subject_id UUID NOT NULL,
    causation_id UUID NOT NULL,
    correlation_id UUID NOT NULL,
    payload_version INTEGER NOT NULL DEFAULT 1 CHECK (payload_version > 0),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    dedupe_key TEXT NOT NULL CHECK (char_length(dedupe_key) BETWEEN 1 AND 255),
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE agent_task_queue ADD COLUMN orchestration_run_id UUID;
