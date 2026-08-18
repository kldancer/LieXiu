CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_plan_proposal_version_unique
    ON artifact(workspace_id, mission_id, kind, version)
    WHERE task_node_id IS NULL AND kind = 'plan_proposal';
