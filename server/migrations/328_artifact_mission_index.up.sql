CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_artifact_mission
    ON artifact(workspace_id, mission_id, task_node_id, created_at, id);
