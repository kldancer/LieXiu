\set ON_ERROR_STOP on

-- Read-only gate for the destructive part of the personal-v1 schema migration.
-- Run with psql against the target database. A non-zero row count or a legacy
-- AgentTask link aborts the script; this file never mutates application data.

WITH legacy_rows AS (
    SELECT 'autopilot' AS object_name, count(*)::bigint AS row_count FROM autopilot
    UNION ALL SELECT 'autopilot_collaborator', count(*) FROM autopilot_collaborator
    UNION ALL SELECT 'autopilot_rule_version', count(*) FROM autopilot_rule_version
    UNION ALL SELECT 'autopilot_run', count(*) FROM autopilot_run
    UNION ALL SELECT 'autopilot_subscriber', count(*) FROM autopilot_subscriber
    UNION ALL SELECT 'autopilot_trigger', count(*) FROM autopilot_trigger
    UNION ALL SELECT 'webhook_delivery', count(*) FROM webhook_delivery
    UNION ALL SELECT 'agent_builder_draft', count(*) FROM agent_builder_draft
    UNION ALL SELECT 'chat_draft_restore', count(*) FROM chat_draft_restore
    UNION ALL SELECT 'chat_message', count(*) FROM chat_message
    UNION ALL SELECT 'chat_pinned_agent', count(*) FROM chat_pinned_agent
    UNION ALL SELECT 'chat_session', count(*) FROM chat_session
    UNION ALL SELECT 'inbox_item', count(*) FROM inbox_item
    UNION ALL SELECT 'issue_subscriber', count(*) FROM issue_subscriber
    UNION ALL SELECT 'notification_preference', count(*) FROM notification_preference
    UNION ALL SELECT 'comment_reaction', count(*) FROM comment_reaction
    UNION ALL SELECT 'issue_reaction', count(*) FROM issue_reaction
    UNION ALL SELECT 'pinned_item', count(*) FROM pinned_item
    UNION ALL SELECT 'squad', count(*) FROM squad
    UNION ALL SELECT 'squad_member', count(*) FROM squad_member
), legacy_task_links AS (
    SELECT 'agent.kind<>user' AS object_name, count(*)::bigint AS row_count
    FROM agent WHERE kind <> 'user'
    UNION ALL
    SELECT 'agent.system_key', count(*)
    FROM agent WHERE system_key IS NOT NULL
    UNION ALL
    SELECT 'agent_task_queue.autopilot_run_id' AS object_name, count(*)::bigint AS row_count
    FROM agent_task_queue WHERE autopilot_run_id IS NOT NULL
    UNION ALL
    SELECT 'agent_task_queue.chat_session_id', count(*)
    FROM agent_task_queue WHERE chat_session_id IS NOT NULL
    UNION ALL
    SELECT 'agent_task_queue.squad_id', count(*)
    FROM agent_task_queue WHERE squad_id IS NOT NULL
    UNION ALL
    SELECT 'agent_task_queue.chat_input_task_id', count(*)
    FROM agent_task_queue WHERE chat_input_task_id IS NOT NULL
    UNION ALL
    SELECT 'agent_task_queue.chat_finalize_deferred_at', count(*)
    FROM agent_task_queue WHERE chat_finalize_deferred_at IS NOT NULL
    UNION ALL
    SELECT 'agent_task_queue.is_leader_task', count(*)
    FROM agent_task_queue WHERE is_leader_task
    UNION ALL
    SELECT 'agent_task_queue.rule_version_id', count(*)
    FROM agent_task_queue WHERE rule_version_id IS NOT NULL
    UNION ALL
    SELECT 'attachment.chat_session_id', count(*)
    FROM attachment WHERE chat_session_id IS NOT NULL
    UNION ALL
    SELECT 'attachment.chat_message_id', count(*)
    FROM attachment WHERE chat_message_id IS NOT NULL
    UNION ALL
    SELECT 'issue.assignee_type=squad', count(*)
    FROM issue WHERE assignee_type = 'squad'
    UNION ALL
    SELECT 'issue.origin_type=retired_product', count(*)
    FROM issue
    WHERE origin_type IN ('autopilot', 'lark_chat', 'slack_chat', 'dingtalk_chat', 'wecom_chat')
    UNION ALL
    SELECT 'issue_subscriber.reason=autopilot', count(*)
    FROM issue_subscriber WHERE reason = 'autopilot'
    UNION ALL
    SELECT 'quick_action.assignee_type=squad', count(*)
    FROM quick_action WHERE assignee_type = 'squad'
    UNION ALL
    SELECT 'activity_log.action=squad_leader_evaluated', count(*)
    FROM activity_log WHERE action = 'squad_leader_evaluated'
)
SELECT object_name, row_count
FROM (
    SELECT * FROM legacy_rows
    UNION ALL
    SELECT * FROM legacy_task_links
) AS gate
WHERE row_count > 0
ORDER BY object_name;

-- Distinguish live historical relationships from rows whose parent has already
-- been removed. Both classes still block physical deletion: this diagnostic is
-- only evidence for the later archive/migrate/delete decision, not permission
-- to discard orphaned rows automatically.
WITH relationship_diagnostics AS (
    SELECT
        'autopilot_rule_version' AS object_name,
        count(*) FILTER (WHERE a.id IS NOT NULL)::bigint AS live_rows,
        count(*) FILTER (WHERE a.id IS NULL)::bigint AS orphan_rows
    FROM autopilot_rule_version row
    LEFT JOIN autopilot a ON a.id = row.autopilot_id
    UNION ALL
    SELECT
        'autopilot_subscriber',
        count(*) FILTER (WHERE a.id IS NOT NULL),
        count(*) FILTER (WHERE a.id IS NULL)
    FROM autopilot_subscriber row
    LEFT JOIN autopilot a ON a.id = row.autopilot_id
    UNION ALL
    SELECT
        'chat_draft_restore',
        count(*) FILTER (WHERE task.id IS NOT NULL),
        count(*) FILTER (WHERE task.id IS NULL)
    FROM chat_draft_restore row
    LEFT JOIN agent_task_queue task ON task.id = row.task_id
    UNION ALL
    SELECT
        'inbox_item',
        count(*) FILTER (WHERE workspace.id IS NOT NULL),
        count(*) FILTER (WHERE workspace.id IS NULL)
    FROM inbox_item row
    LEFT JOIN workspace ON workspace.id = row.workspace_id
    UNION ALL
    SELECT
        'issue_subscriber',
        count(*) FILTER (WHERE issue.id IS NOT NULL),
        count(*) FILTER (WHERE issue.id IS NULL)
    FROM issue_subscriber row
    LEFT JOIN issue ON issue.id = row.issue_id
)
SELECT object_name, live_rows, orphan_rows
FROM relationship_diagnostics
WHERE live_rows > 0 OR orphan_rows > 0
ORDER BY object_name;

DO $preflight$
DECLARE
    blocked_count bigint;
BEGIN
    SELECT
        (SELECT count(*) FROM autopilot) +
        (SELECT count(*) FROM autopilot_collaborator) +
        (SELECT count(*) FROM autopilot_rule_version) +
        (SELECT count(*) FROM autopilot_run) +
        (SELECT count(*) FROM autopilot_subscriber) +
        (SELECT count(*) FROM autopilot_trigger) +
        (SELECT count(*) FROM webhook_delivery) +
        (SELECT count(*) FROM agent_builder_draft) +
        (SELECT count(*) FROM chat_draft_restore) +
        (SELECT count(*) FROM chat_message) +
        (SELECT count(*) FROM chat_pinned_agent) +
        (SELECT count(*) FROM chat_session) +
        (SELECT count(*) FROM inbox_item) +
        (SELECT count(*) FROM issue_subscriber) +
        (SELECT count(*) FROM notification_preference) +
        (SELECT count(*) FROM comment_reaction) +
        (SELECT count(*) FROM issue_reaction) +
        (SELECT count(*) FROM pinned_item) +
        (SELECT count(*) FROM squad) +
        (SELECT count(*) FROM squad_member) +
        (SELECT count(*) FROM agent WHERE kind <> 'user') +
        (SELECT count(*) FROM agent WHERE system_key IS NOT NULL) +
        (SELECT count(*) FROM agent_task_queue WHERE autopilot_run_id IS NOT NULL) +
        (SELECT count(*) FROM agent_task_queue WHERE chat_session_id IS NOT NULL) +
        (SELECT count(*) FROM agent_task_queue WHERE squad_id IS NOT NULL) +
        (SELECT count(*) FROM agent_task_queue WHERE chat_input_task_id IS NOT NULL) +
        (SELECT count(*) FROM agent_task_queue WHERE chat_finalize_deferred_at IS NOT NULL) +
        (SELECT count(*) FROM agent_task_queue WHERE is_leader_task) +
        (SELECT count(*) FROM agent_task_queue WHERE rule_version_id IS NOT NULL) +
        (SELECT count(*) FROM attachment WHERE chat_session_id IS NOT NULL) +
        (SELECT count(*) FROM attachment WHERE chat_message_id IS NOT NULL) +
        (SELECT count(*) FROM issue WHERE assignee_type = 'squad') +
        (SELECT count(*) FROM issue WHERE origin_type IN ('autopilot', 'lark_chat', 'slack_chat', 'dingtalk_chat', 'wecom_chat')) +
        (SELECT count(*) FROM issue_subscriber WHERE reason = 'autopilot') +
        (SELECT count(*) FROM quick_action WHERE assignee_type = 'squad') +
        (SELECT count(*) FROM activity_log WHERE action = 'squad_leader_evaluated')
    INTO blocked_count;

    IF blocked_count > 0 THEN
        RAISE EXCEPTION
            'personal-v1 physical schema compression is blocked by % legacy rows or task links; classify/archive them before adding DROP migrations',
            blocked_count;
    END IF;
END
$preflight$;
