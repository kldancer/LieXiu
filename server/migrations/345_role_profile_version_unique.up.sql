CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_role_profile_workspace_key_version
    ON role_profile (workspace_id, profile_key, version);
