CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_orchestration_run_attempt_unique
    ON orchestration_run(assignment_id, attempt);
