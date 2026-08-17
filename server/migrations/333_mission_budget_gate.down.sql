ALTER TABLE task_node
    DROP COLUMN IF EXISTS budget_estimate_cost_usd_ticks,
    DROP COLUMN IF EXISTS budget_estimate_tokens;

ALTER TABLE mission
    DROP COLUMN IF EXISTS budget_approved_at,
    DROP COLUMN IF EXISTS budget_approved_by,
    DROP COLUMN IF EXISTS budget_grant_cost_usd_ticks,
    DROP COLUMN IF EXISTS budget_grant_tokens,
    DROP COLUMN IF EXISTS budget_gate_status;
