CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_artifact_task_kind_version_unique
    ON artifact(task_node_id, kind, version);
