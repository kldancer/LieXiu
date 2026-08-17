CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mission_workspace_status
    ON mission(workspace_id, status, created_at DESC);
