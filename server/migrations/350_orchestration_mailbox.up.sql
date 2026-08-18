-- Mission-scoped bounded collaboration messages. This is deliberately separate
-- from provider execution transcripts (`task_message`) and from the retired
-- global collaboration/social tables removed by migration 338.

CREATE TABLE orchestration_mailbox_message (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    task_node_id UUID,
    run_id UUID,
    artifact_id UUID,
    reply_to_message_id UUID,
    schema_version INTEGER NOT NULL DEFAULT 1 CHECK (schema_version = 1),
    type TEXT NOT NULL CHECK (type IN (
        'context_request', 'context_response', 'handoff', 'artifact_notice',
        'review_feedback', 'blocker', 'decision_request'
    )),
    sender_type TEXT NOT NULL CHECK (sender_type IN ('member', 'agent', 'orchestrator')),
    sender_id UUID,
    recipient_type TEXT NOT NULL CHECK (recipient_type IN ('member', 'agent')),
    recipient_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'consumed', 'expired', 'cancelled')
    ),
    dedupe_key TEXT NOT NULL CHECK (
        octet_length(dedupe_key) BETWEEN 1 AND 128
        AND dedupe_key !~ E'[\\r\\n]'
    ),
    hops INTEGER NOT NULL DEFAULT 0 CHECK (hops BETWEEN 0 AND 8),
    payload_version INTEGER NOT NULL DEFAULT 1 CHECK (payload_version = 1),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    command_id UUID NOT NULL,
    created_by UUID NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    status_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (sender_type = 'orchestrator' AND sender_id IS NULL)
        OR
        (sender_type IN ('member', 'agent') AND sender_id IS NOT NULL)
    ),
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '7 days'),
    CHECK (type <> 'context_response' OR (reply_to_message_id IS NOT NULL AND hops > 0)),
    CHECK (type NOT IN ('artifact_notice', 'review_feedback') OR artifact_id IS NOT NULL)
);

CREATE UNIQUE INDEX idx_orchestration_mailbox_command_unique
    ON orchestration_mailbox_message(workspace_id, command_id);

CREATE UNIQUE INDEX idx_orchestration_mailbox_semantic_dedupe
    ON orchestration_mailbox_message(
        workspace_id,
        mission_id,
        sender_type,
        COALESCE(sender_id, '00000000-0000-0000-0000-000000000000'::uuid),
        dedupe_key
    );

CREATE INDEX idx_orchestration_mailbox_recipient_pending
    ON orchestration_mailbox_message(
        workspace_id, mission_id, recipient_type, recipient_id, created_at, id
    )
    WHERE status = 'pending';

CREATE INDEX idx_orchestration_mailbox_expiry_pending
    ON orchestration_mailbox_message(expires_at, id)
    WHERE status = 'pending';

ALTER TABLE orchestration_activity
    DROP CONSTRAINT orchestration_activity_subject_type_check,
    ADD CONSTRAINT orchestration_activity_subject_type_check CHECK (
        subject_type IN (
            'mission', 'task_node', 'assignment', 'run', 'artifact', 'review',
            'mailbox_message'
        )
    );
