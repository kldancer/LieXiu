CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orchestration_assignment_mission
    ON orchestration_assignment(workspace_id, mission_id, task_node_id, role, sequence);
