CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_orchestration_activity_sequence_unique
    ON orchestration_activity(mission_id, sequence);
