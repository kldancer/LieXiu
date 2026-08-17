CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_orchestration_assignment_active_unique
    ON orchestration_assignment(task_node_id, role)
    WHERE status = 'active';
