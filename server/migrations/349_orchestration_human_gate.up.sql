-- Task-scoped Owner decision points for orchestration conditions that must not
-- be bypassed by an automatic retry or a same-Agent review. Historical state
-- is retained in resolved rows and the Activity stream; at most one Gate may
-- be pending for a TaskNode at a time.

CREATE TABLE orchestration_human_gate (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    task_node_id UUID NOT NULL,
    artifact_id UUID NOT NULL,
    source_run_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (
        kind IN ('reviewer_unavailable', 'rework_limit_exceeded')
    ),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'resolved')
    ),
    reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 1024),
    context JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(context) = 'object'),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    resolved_by UUID,
    resolution TEXT CHECK (resolution IN ('retry')),
    resolution_reason TEXT,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (status = 'pending' AND resolved_by IS NULL AND resolution IS NULL AND resolved_at IS NULL)
        OR
        (status = 'resolved' AND resolved_by IS NOT NULL AND resolution IS NOT NULL AND resolved_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX idx_orchestration_human_gate_task_pending
    ON orchestration_human_gate(workspace_id, mission_id, task_node_id)
    WHERE status = 'pending';

CREATE INDEX idx_orchestration_human_gate_mission
    ON orchestration_human_gate(workspace_id, mission_id, created_at, id);
