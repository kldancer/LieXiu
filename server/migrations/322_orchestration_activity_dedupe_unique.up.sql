CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_orchestration_activity_dedupe_unique
    ON orchestration_activity(workspace_id, dedupe_key);
