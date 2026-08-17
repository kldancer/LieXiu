-- Wave 3 personal-v1 schema compression for retired execution products.
-- Inbox and ordinary Issue subscribers are intentionally out of scope.
-- The table locks keep the zero-row gate and destructive DDL in one migration
-- execution boundary so a retired writer cannot race between them.
LOCK TABLE
    agent_task_queue,
    attachment,
    issue,
    issue_subscriber,
    quick_action,
    activity_log,
    agent_builder_draft,
    chat_draft_restore,
    chat_message,
    chat_pinned_agent,
    chat_session,
    autopilot,
    autopilot_collaborator,
    autopilot_rule_version,
    autopilot_run,
    autopilot_subscriber,
    autopilot_trigger,
    webhook_delivery,
    squad,
    squad_member
IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    blockers jsonb;
BEGIN
    SELECT jsonb_object_agg(object_name, row_count ORDER BY object_name)
    INTO blockers
    FROM (
        SELECT 'agent_builder_draft' AS object_name, count(*)::bigint AS row_count FROM agent_builder_draft
        UNION ALL SELECT 'autopilot', count(*) FROM autopilot
        UNION ALL SELECT 'autopilot_collaborator', count(*) FROM autopilot_collaborator
        UNION ALL SELECT 'autopilot_rule_version', count(*) FROM autopilot_rule_version
        UNION ALL SELECT 'autopilot_run', count(*) FROM autopilot_run
        UNION ALL SELECT 'autopilot_subscriber', count(*) FROM autopilot_subscriber
        UNION ALL SELECT 'autopilot_trigger', count(*) FROM autopilot_trigger
        UNION ALL SELECT 'chat_draft_restore', count(*) FROM chat_draft_restore
        UNION ALL SELECT 'chat_message', count(*) FROM chat_message
        UNION ALL SELECT 'chat_pinned_agent', count(*) FROM chat_pinned_agent
        UNION ALL SELECT 'chat_session', count(*) FROM chat_session
        UNION ALL SELECT 'squad', count(*) FROM squad
        UNION ALL SELECT 'squad_member', count(*) FROM squad_member
        UNION ALL SELECT 'webhook_delivery', count(*) FROM webhook_delivery
        UNION ALL SELECT 'agent_task_queue.autopilot_run_id', count(*) FROM agent_task_queue WHERE autopilot_run_id IS NOT NULL
        UNION ALL SELECT 'agent_task_queue.chat_finalize_deferred_at', count(*) FROM agent_task_queue WHERE chat_finalize_deferred_at IS NOT NULL
        UNION ALL SELECT 'agent_task_queue.chat_input_task_id', count(*) FROM agent_task_queue WHERE chat_input_task_id IS NOT NULL
        UNION ALL SELECT 'agent_task_queue.chat_session_id', count(*) FROM agent_task_queue WHERE chat_session_id IS NOT NULL
        UNION ALL SELECT 'agent_task_queue.is_leader_task', count(*) FROM agent_task_queue WHERE is_leader_task
        UNION ALL SELECT 'agent_task_queue.rule_version_id', count(*) FROM agent_task_queue WHERE rule_version_id IS NOT NULL
        UNION ALL SELECT 'agent_task_queue.squad_id', count(*) FROM agent_task_queue WHERE squad_id IS NOT NULL
        UNION ALL SELECT 'attachment.chat_message_id', count(*) FROM attachment WHERE chat_message_id IS NOT NULL
        UNION ALL SELECT 'attachment.chat_session_id', count(*) FROM attachment WHERE chat_session_id IS NOT NULL
        UNION ALL SELECT 'issue.assignee_type=squad', count(*) FROM issue WHERE assignee_type = 'squad'
        UNION ALL SELECT 'issue.origin_type=retired_product', count(*) FROM issue
            WHERE origin_type IN ('autopilot', 'lark_chat', 'slack_chat', 'dingtalk_chat', 'wecom_chat')
        UNION ALL SELECT 'issue_subscriber.reason=autopilot', count(*) FROM issue_subscriber WHERE reason = 'autopilot'
        UNION ALL SELECT 'quick_action.assignee_type=squad', count(*) FROM quick_action WHERE assignee_type = 'squad'
        UNION ALL SELECT 'activity_log.action=squad_leader_evaluated', count(*) FROM activity_log WHERE action = 'squad_leader_evaluated'
    ) AS counts
    WHERE row_count > 0;

    IF blockers IS NOT NULL THEN
        RAISE EXCEPTION USING
            MESSAGE = format(
                'migration 337 refused: retired Chat/Autopilot/Squad data remains: %s; export, migrate, or delete it before physical compression',
                blockers
            );
    END IF;
END
$$;

DROP INDEX IF EXISTS idx_activity_log_squad_no_action_task;

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_assignee_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_assignee_type_check
    CHECK (assignee_type IN ('member', 'agent'));

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('quick_create', 'agent_create'));

ALTER TABLE issue_subscriber DROP CONSTRAINT IF EXISTS issue_subscriber_reason_check;
ALTER TABLE issue_subscriber ADD CONSTRAINT issue_subscriber_reason_check
    CHECK (reason IN ('creator', 'assignee', 'commenter', 'mentioned', 'manual', 'delegated'));

ALTER TABLE quick_action DROP CONSTRAINT IF EXISTS quick_action_assignee_type_check;
ALTER TABLE quick_action ADD CONSTRAINT quick_action_assignee_type_check
    CHECK (assignee_type = 'agent');

ALTER TABLE attachment
    DROP COLUMN IF EXISTS chat_message_id,
    DROP COLUMN IF EXISTS chat_session_id;

ALTER TABLE agent_task_queue
    DROP COLUMN IF EXISTS autopilot_run_id,
    DROP COLUMN IF EXISTS chat_finalize_deferred_at,
    DROP COLUMN IF EXISTS chat_input_task_id,
    DROP COLUMN IF EXISTS chat_session_id,
    DROP COLUMN IF EXISTS is_leader_task,
    DROP COLUMN IF EXISTS rule_version_id,
    DROP COLUMN IF EXISTS squad_id;

DROP TABLE IF EXISTS agent_builder_draft;
DROP TABLE IF EXISTS chat_draft_restore;
DROP TABLE IF EXISTS chat_pinned_agent;
DROP TABLE IF EXISTS webhook_delivery;
DROP TABLE IF EXISTS autopilot_collaborator;
DROP TABLE IF EXISTS autopilot_rule_version;
DROP TABLE IF EXISTS autopilot_subscriber;
DROP TABLE IF EXISTS autopilot_run;
DROP TABLE IF EXISTS autopilot_trigger;
DROP TABLE IF EXISTS autopilot;
DROP TABLE IF EXISTS chat_message;
DROP TABLE IF EXISTS chat_session;
DROP TABLE IF EXISTS squad_member;
DROP TABLE IF EXISTS squad;
