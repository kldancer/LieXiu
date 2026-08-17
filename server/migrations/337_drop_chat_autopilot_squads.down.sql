-- Migration 337 is destructive by design. A downgrade recreates the
-- pre-337 catalog shape only; it cannot recover rows removed by 337.
-- Restore a pre-337 backup to recover Chat, Autopilot, Squad, Agent Builder,
-- Webhook Delivery, task-link, or attachment data.

-- Recreate parents before children. These are the final pre-337 shapes after
-- the historical additive migrations, not the original first-release shapes.
CREATE TABLE IF NOT EXISTS squad (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    leader_id UUID NOT NULL REFERENCES agent(id) ON DELETE RESTRICT,
    creator_id UUID NOT NULL,
    archived_at TIMESTAMPTZ,
    archived_by UUID,
    avatar_url TEXT,
    instructions TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS squad_member (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    squad_id UUID NOT NULL REFERENCES squad(id) ON DELETE CASCADE,
    member_type TEXT NOT NULL CHECK (member_type IN ('agent', 'member')),
    member_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (squad_id, member_type, member_id)
);

CREATE TABLE IF NOT EXISTS chat_session (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    creator_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    session_id TEXT,
    work_dir TEXT,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    project_id UUID,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    unread_since TIMESTAMPTZ,
    last_read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_agent_intro BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS chat_message (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_session_id UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    task_id UUID,
    failure_reason TEXT,
    elapsed_ms BIGINT,
    message_kind TEXT NOT NULL DEFAULT 'message',
    quick_actions JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS autopilot (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID REFERENCES project(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    description TEXT,
    assignee_id UUID NOT NULL,
    assignee_type TEXT NOT NULL DEFAULT 'agent'
        CHECK (assignee_type IN ('agent', 'squad')),
    priority TEXT NOT NULL DEFAULT 'medium'
        CHECK (priority IN ('urgent', 'high', 'medium', 'low', 'none')),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused', 'archived')),
    execution_mode TEXT NOT NULL DEFAULT 'create_issue'
        CHECK (execution_mode IN ('create_issue', 'run_only')),
    issue_title_template TEXT,
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent')),
    created_by_id UUID NOT NULL,
    last_run_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS autopilot_trigger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    autopilot_id UUID NOT NULL REFERENCES autopilot(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('schedule', 'webhook', 'api')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    cron_expression TEXT,
    timezone TEXT DEFAULT 'UTC',
    next_run_at TIMESTAMPTZ,
    webhook_token TEXT,
    label TEXT,
    last_fired_at TIMESTAMPTZ,
    provider TEXT NOT NULL DEFAULT 'generic'
        CHECK (provider IN ('generic', 'github')),
    signing_secret TEXT,
    event_filters JSONB,
    published_by_type TEXT,
    published_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS autopilot_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    autopilot_id UUID NOT NULL REFERENCES autopilot(id) ON DELETE CASCADE,
    trigger_id UUID REFERENCES autopilot_trigger(id) ON DELETE SET NULL,
    source TEXT NOT NULL CHECK (source IN ('schedule', 'manual', 'webhook', 'api')),
    status TEXT NOT NULL DEFAULT 'issue_created'
        CHECK (status IN ('issue_created', 'running', 'completed', 'failed', 'skipped')),
    issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    squad_id UUID REFERENCES squad(id) ON DELETE SET NULL,
    trigger_payload JSONB,
    result JSONB,
    failure_reason TEXT,
    webhook_delivery_id UUID,
    planned_at TIMESTAMPTZ,
    triggered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS autopilot_subscriber (
    autopilot_id UUID NOT NULL,
    user_type TEXT NOT NULL CHECK (user_type IN ('member')),
    user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (autopilot_id, user_type, user_id)
);

CREATE TABLE IF NOT EXISTS autopilot_collaborator (
    autopilot_id UUID NOT NULL,
    user_type TEXT NOT NULL CHECK (user_type IN ('member')),
    user_id UUID NOT NULL,
    granted_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (autopilot_id, user_type, user_id)
);

CREATE TABLE IF NOT EXISTS autopilot_rule_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    autopilot_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    published_by_type TEXT NOT NULL,
    published_by_id UUID,
    config_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webhook_delivery (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    autopilot_id UUID NOT NULL REFERENCES autopilot(id) ON DELETE CASCADE,
    trigger_id UUID NOT NULL REFERENCES autopilot_trigger(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('generic', 'github')),
    event TEXT NOT NULL DEFAULT 'webhook.received',
    dedupe_key TEXT,
    dedupe_source TEXT,
    signature_status TEXT NOT NULL DEFAULT 'not_required'
        CHECK (signature_status IN ('not_required', 'valid', 'invalid', 'missing')),
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'dispatched', 'rejected', 'ignored', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 1,
    selected_headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    content_type TEXT,
    raw_body BYTEA,
    response_status INTEGER,
    response_body TEXT,
    autopilot_run_id UUID REFERENCES autopilot_run(id) ON DELETE SET NULL,
    replayed_from_delivery_id UUID REFERENCES webhook_delivery(id) ON DELETE SET NULL,
    error TEXT,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    dispatch_attempts INTEGER NOT NULL DEFAULT 0,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS chat_pinned_agent (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    user_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    position FLOAT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id, agent_id)
);

CREATE TABLE IF NOT EXISTS chat_draft_restore (
    id UUID PRIMARY KEY,
    chat_session_id UUID NOT NULL,
    task_id UUID NOT NULL,
    content TEXT NOT NULL,
    attachment_ids UUID[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_builder_draft (
    chat_session_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    draft JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Restore queue links after both referenced legacy tables exist. Values are
-- intentionally NULL: pre-337 rows must come from a backup.
ALTER TABLE agent_task_queue
    ADD COLUMN IF NOT EXISTS autopilot_run_id UUID REFERENCES autopilot_run(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS chat_finalize_deferred_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS chat_input_task_id UUID,
    ADD COLUMN IF NOT EXISTS chat_session_id UUID REFERENCES chat_session(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS is_leader_task BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS rule_version_id UUID,
    ADD COLUMN IF NOT EXISTS squad_id UUID;

ALTER TABLE attachment
    ADD COLUMN IF NOT EXISTS chat_session_id UUID REFERENCES chat_session(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS chat_message_id UUID REFERENCES chat_message(id) ON DELETE CASCADE;

-- 337 narrowed these checks. Restore the last pre-337 accepted values.
ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_assignee_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_assignee_type_check
    CHECK (assignee_type IN ('member', 'agent', 'squad'));

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('autopilot', 'quick_create', 'agent_create'));

ALTER TABLE issue_subscriber DROP CONSTRAINT IF EXISTS issue_subscriber_reason_check;
ALTER TABLE issue_subscriber ADD CONSTRAINT issue_subscriber_reason_check
    CHECK (reason IN ('creator', 'assignee', 'commenter', 'mentioned', 'manual', 'autopilot', 'delegated'));

ALTER TABLE quick_action DROP CONSTRAINT IF EXISTS quick_action_assignee_type_check;
ALTER TABLE quick_action ADD CONSTRAINT quick_action_assignee_type_check
    CHECK (assignee_type IN ('agent', 'squad'));

-- Key pre-337 lookup and integrity indexes. They are intentionally ordinary
-- CREATE INDEX statements because downgrade runs inside the migration
-- transaction; PostgreSQL forbids CREATE INDEX CONCURRENTLY there.
CREATE INDEX IF NOT EXISTS idx_activity_log_squad_no_action_task
    ON activity_log (issue_id, actor_id, ((details->>'task_id')))
    WHERE actor_type = 'agent'
      AND action = 'squad_leader_evaluated'
      AND details->>'outcome' = 'no_action';
CREATE INDEX IF NOT EXISTS idx_squad_workspace ON squad(workspace_id);
CREATE INDEX IF NOT EXISTS idx_squad_member_squad ON squad_member(squad_id);
CREATE INDEX IF NOT EXISTS idx_squad_member_entity ON squad_member(member_type, member_id);
CREATE INDEX IF NOT EXISTS idx_chat_session_workspace ON chat_session(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chat_session_creator ON chat_session(creator_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_chat_message_session ON chat_message(chat_session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_chat_message_input_owner
    ON chat_message(task_id, created_at) WHERE role = 'user';
CREATE INDEX IF NOT EXISTS idx_autopilot_workspace ON autopilot(workspace_id);
CREATE INDEX IF NOT EXISTS idx_autopilot_assignee ON autopilot(assignee_id);
CREATE INDEX IF NOT EXISTS idx_autopilot_assignee_type_id ON autopilot(assignee_type, assignee_id);
CREATE INDEX IF NOT EXISTS idx_autopilot_trigger_autopilot ON autopilot_trigger(autopilot_id);
CREATE INDEX IF NOT EXISTS idx_autopilot_trigger_next_run ON autopilot_trigger(next_run_at)
    WHERE enabled = TRUE AND kind = 'schedule';
CREATE INDEX IF NOT EXISTS idx_autopilot_run_autopilot ON autopilot_run(autopilot_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_autopilot_run_status ON autopilot_run(autopilot_id, status)
    WHERE status IN ('issue_created', 'running');
CREATE INDEX IF NOT EXISTS idx_autopilot_run_issue ON autopilot_run(issue_id)
    WHERE issue_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_autopilot_run_squad_id ON autopilot_run(squad_id)
    WHERE squad_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_autopilot_run_trigger_planned
    ON autopilot_run(trigger_id, planned_at)
    WHERE trigger_id IS NOT NULL AND planned_at IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_autopilot_run_webhook_delivery
    ON autopilot_run(webhook_delivery_id)
    WHERE webhook_delivery_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_autopilot_subscriber_user
    ON autopilot_subscriber(user_type, user_id);
CREATE INDEX IF NOT EXISTS idx_autopilot_collaborator_user
    ON autopilot_collaborator(user_type, user_id);
CREATE INDEX IF NOT EXISTS idx_autopilot_rule_version_active
    ON autopilot_rule_version(workspace_id, autopilot_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_autopilot
    ON webhook_delivery(autopilot_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_delivery_dedupe
    ON webhook_delivery(trigger_id, dedupe_key)
    WHERE dedupe_key IS NOT NULL AND status NOT IN ('rejected', 'failed');
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_run
    ON webhook_delivery(autopilot_run_id) WHERE autopilot_run_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_queue
    ON webhook_delivery(available_at, created_at) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS idx_chat_draft_restore_session
    ON chat_draft_restore(chat_session_id);
CREATE INDEX IF NOT EXISTS idx_attachment_chat_session
    ON attachment(chat_session_id) WHERE chat_session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachment_chat_message
    ON attachment(chat_message_id) WHERE chat_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_task_queue_squad_id
    ON agent_task_queue(squad_id) WHERE squad_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_task_chat_finalize_deferred
    ON agent_task_queue(chat_finalize_deferred_at)
    WHERE chat_finalize_deferred_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_task_queue_chat_pending_v3
    ON agent_task_queue(chat_session_id, created_at DESC)
    WHERE chat_session_id IS NOT NULL
      AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred');
