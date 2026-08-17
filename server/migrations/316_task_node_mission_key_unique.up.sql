CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_task_node_mission_key_unique
    ON task_node(mission_id, node_key);
