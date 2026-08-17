-- Wave 1C.8 removes Multica's marketplace/catalog/plugin distribution
-- control plane. Local Skills, generic MCP configuration, Runtime Adapters and
-- the AgentTask execution plane remain independent of these tables.

DROP TRIGGER IF EXISTS trg_agent_task_queue_plugin_execution_manifest ON agent_task_queue;
DROP FUNCTION IF EXISTS pin_plugin_execution_manifest();

DROP INDEX IF EXISTS idx_agent_task_plugin_execution_manifest;
ALTER TABLE agent_task_queue
    DROP COLUMN IF EXISTS plugin_execution_manifest_id;

DROP TABLE IF EXISTS plugin_execution_manifest;
DROP TABLE IF EXISTS plugin_health;
DROP TABLE IF EXISTS plugin_capability_snapshot;
DROP TABLE IF EXISTS plugin_workspace_capability_state;
DROP TABLE IF EXISTS plugin_artifact_file;
DROP TABLE IF EXISTS plugin_binding;
DROP TABLE IF EXISTS plugin_grant;
DROP TABLE IF EXISTS plugin_installation;
DROP TABLE IF EXISTS plugin_contribution;
DROP TABLE IF EXISTS plugin_release;
DROP TABLE IF EXISTS plugin_identity;

DROP FUNCTION IF EXISTS enforce_plugin_release_immutable();
DROP FUNCTION IF EXISTS reject_plugin_append_only_update();
