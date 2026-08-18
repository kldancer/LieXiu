CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_mission_role_policy_snapshot_workspace_mission_duty
    ON mission_role_policy_snapshot (workspace_id, mission_id, duty);
