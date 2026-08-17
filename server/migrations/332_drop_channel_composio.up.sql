-- Wave 1C.6 removes every external chat-channel adapter and the Composio
-- control-plane dependency. Ordinary Web Chat, attachments, generic MCP
-- configuration, runtime overlays and the AgentTask execution plane remain.

UPDATE issue
SET origin_type = NULL,
    origin_id = NULL
WHERE origin_type IN ('lark_chat', 'slack_chat', 'dingtalk_chat', 'wecom_chat');

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('autopilot', 'quick_create', 'agent_create'))
    NOT VALID;
ALTER TABLE issue VALIDATE CONSTRAINT issue_origin_type_check;

ALTER TABLE agent
    DROP COLUMN IF EXISTS composio_toolkit_allowlist;

COMMENT ON COLUMN agent_task_queue.runtime_mcp_overlay IS
    'Optional provider-neutral MCP server overlay computed at dispatch time and merged on top of agent.mcp_config. Cleared after task completion by trg_clear_runtime_mcp_overlay.';
COMMENT ON COLUMN agent_task_queue.originator_user_id IS
    'Top-of-chain human originator for attribution and A2A invocation permission checks. NULL when no human initiated the task chain.';

ALTER TABLE chat_message
    DROP COLUMN IF EXISTS channel_media_pending_until,
    DROP COLUMN IF EXISTS channel_ingested;

DROP TABLE IF EXISTS channel_binding_token;
DROP TABLE IF EXISTS channel_outbound_card_message;
DROP TABLE IF EXISTS channel_inbound_audit;
DROP TABLE IF EXISTS channel_inbound_message_dedup;
DROP TABLE IF EXISTS channel_chat_session_binding;
DROP TABLE IF EXISTS channel_user_binding;
DROP TABLE IF EXISTS dingtalk_group_route;
DROP TABLE IF EXISTS channel_media_pending_object;
DROP TABLE IF EXISTS channel_installation;

DROP TABLE IF EXISTS lark_binding_token;
DROP TABLE IF EXISTS lark_outbound_card_message;
DROP TABLE IF EXISTS lark_inbound_audit;
DROP TABLE IF EXISTS lark_inbound_message_dedup;
DROP TABLE IF EXISTS lark_chat_session_binding;
DROP TABLE IF EXISTS lark_user_binding;
DROP TABLE IF EXISTS lark_installation;

DROP TABLE IF EXISTS user_composio_connection;
