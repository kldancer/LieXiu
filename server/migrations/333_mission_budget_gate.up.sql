-- Provider-neutral Mission budget policy and human approval facts.
-- Usage remains owned by task_usage; these columns only hold the immutable
-- Plan reservation estimates and current Mission gate decision.

ALTER TABLE mission
    ADD COLUMN budget_gate_status TEXT NOT NULL DEFAULT 'none'
        CHECK (budget_gate_status IN ('none', 'approved', 'approval_required', 'exceeded')),
    ADD COLUMN budget_grant_tokens BIGINT NOT NULL DEFAULT 0
        CHECK (budget_grant_tokens >= 0),
    ADD COLUMN budget_grant_cost_usd_ticks BIGINT NOT NULL DEFAULT 0
        CHECK (budget_grant_cost_usd_ticks >= 0),
    ADD COLUMN budget_approved_by UUID,
    ADD COLUMN budget_approved_at TIMESTAMPTZ;

ALTER TABLE task_node
    ADD COLUMN budget_estimate_tokens BIGINT NOT NULL DEFAULT 0
        CHECK (budget_estimate_tokens >= 0),
    ADD COLUMN budget_estimate_cost_usd_ticks BIGINT NOT NULL DEFAULT 0
        CHECK (budget_estimate_cost_usd_ticks >= 0);
