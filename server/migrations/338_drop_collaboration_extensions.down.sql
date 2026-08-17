-- Recreate the empty pre-338 schema only. Historical rows must be restored from
-- a pre-338 backup after rollback; this migration deliberately invents none.

CREATE TABLE inbox_item (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    recipient_type TEXT NOT NULL CHECK (recipient_type IN ('member', 'agent')),
    recipient_id UUID NOT NULL,
    type TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'info'
        CHECK (severity IN ('action_required', 'attention', 'info')),
    issue_id UUID REFERENCES issue(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    body TEXT,
    read BOOLEAN NOT NULL DEFAULT FALSE,
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_type TEXT,
    actor_id UUID,
    details JSONB DEFAULT '{}'
);
CREATE INDEX idx_inbox_recipient
    ON inbox_item(recipient_type, recipient_id, read);
CREATE INDEX idx_inbox_recipient_archived_created
    ON inbox_item(workspace_id, recipient_type, recipient_id, archived, created_at DESC);
CREATE INDEX idx_inbox_active_by_issue
    ON inbox_item(workspace_id, recipient_type, recipient_id, issue_id)
    WHERE archived = false;
CREATE INDEX idx_inbox_item_issue_id ON inbox_item(issue_id);

CREATE TABLE issue_subscriber (
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    user_type TEXT NOT NULL CHECK (user_type IN ('member', 'agent')),
    user_id UUID NOT NULL,
    reason TEXT NOT NULL
        CHECK (reason IN ('creator', 'assignee', 'commenter', 'mentioned', 'manual', 'delegated')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    unsubscribed_at TIMESTAMPTZ,
    opt_out_scope TEXT CHECK (opt_out_scope IN ('issue', 'subtree')),
    PRIMARY KEY (issue_id, user_type, user_id)
);
CREATE INDEX idx_issue_subscriber_user
    ON issue_subscriber(user_type, user_id);

CREATE TABLE notification_preference (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    preferences JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, user_id)
);

CREATE TABLE comment_reaction (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    comment_id UUID NOT NULL REFERENCES comment(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent')),
    actor_id UUID NOT NULL,
    emoji TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (comment_id, actor_type, actor_id, emoji)
);
CREATE INDEX idx_comment_reaction_comment_id
    ON comment_reaction(comment_id);

CREATE TABLE issue_reaction (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent')),
    actor_id UUID NOT NULL,
    emoji TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issue_id, actor_type, actor_id, emoji)
);
CREATE INDEX idx_issue_reaction_issue_id
    ON issue_reaction(issue_id);

CREATE TABLE pinned_item (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    item_type TEXT NOT NULL CHECK (item_type IN ('issue', 'project')),
    item_id UUID NOT NULL,
    position DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id, item_type, item_id)
);
CREATE INDEX idx_pinned_item_user_ws
    ON pinned_item(workspace_id, user_id, position);
