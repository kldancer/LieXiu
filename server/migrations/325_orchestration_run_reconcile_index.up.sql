CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orchestration_run_reconcile
    ON orchestration_run(created_at, id)
    WHERE status IN ('queued', 'dispatched', 'running', 'failed', 'cancelled');
