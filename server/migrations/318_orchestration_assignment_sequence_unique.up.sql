CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_orchestration_assignment_sequence_unique
    ON orchestration_assignment(task_node_id, role, sequence);
