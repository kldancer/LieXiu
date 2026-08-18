CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_role_profile_workspace_command
    ON role_profile (workspace_id, command_id);
