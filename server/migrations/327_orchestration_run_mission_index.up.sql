CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orchestration_run_mission
    ON orchestration_run(workspace_id, mission_id, created_at, id);
