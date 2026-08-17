CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_review_verdict_mission
    ON review_verdict(workspace_id, mission_id, task_node_id, created_at, id);
