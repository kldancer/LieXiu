CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_planning_assignment_active_unique
    ON orchestration_assignment(workspace_id, mission_id, role)
    WHERE task_node_id IS NULL AND role = 'planner' AND status = 'active';
