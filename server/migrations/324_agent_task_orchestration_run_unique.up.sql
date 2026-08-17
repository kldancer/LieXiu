CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_orchestration_run_unique
    ON agent_task_queue(orchestration_run_id)
    WHERE orchestration_run_id IS NOT NULL;
