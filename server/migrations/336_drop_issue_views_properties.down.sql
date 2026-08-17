-- The removed rows are not recoverable from this downgrade. This down
-- migration recreates empty legacy schema only; restore a pre-336 backup to
-- recover saved views, preferences, property definitions, or values.
-- The historical 192/194/195/266/267 indexes are intentionally not rebuilt:
-- this downgrade has no recoverable data to serve, and recreating those
-- production indexes belongs in their separate CONCURRENTLY migrations rather
-- than turning an empty-schema downgrade into a blocking index build.
ALTER TABLE pinned_item DROP CONSTRAINT IF EXISTS pinned_item_item_type_check;
ALTER TABLE pinned_item ADD CONSTRAINT pinned_item_item_type_check
    CHECK (item_type IN ('issue', 'project', 'view'));

ALTER TABLE issue ADD COLUMN properties JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE issue ADD CONSTRAINT issue_properties_is_object
    CHECK (jsonb_typeof(properties) = 'object');
ALTER TABLE issue ADD CONSTRAINT issue_properties_size_limit
    CHECK (pg_column_size(properties) <= 16384);

CREATE TABLE issue_property (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('text', 'number', 'select', 'multi_select', 'date', 'checkbox', 'url')),
    description TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
    position FLOAT NOT NULL DEFAULT 0,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    icon TEXT NOT NULL DEFAULT ''
);

CREATE TABLE issue_view (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    owner_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    scope_type TEXT NOT NULL CHECK (scope_type IN ('workspace', 'my', 'project')),
    scope_id UUID,
    scope_variant TEXT CHECK (scope_variant IN ('assigned', 'created', 'involved', 'any', 'members', 'agents')),
    visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'workspace')),
    definition_version INTEGER NOT NULL DEFAULT 1,
    query JSONB NOT NULL CHECK (jsonb_typeof(query) = 'object'),
    display JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(display) = 'object'),
    revision INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (scope_type = 'project' AND scope_id IS NOT NULL)
        OR (scope_type IN ('workspace', 'my') AND scope_id IS NULL)
    ),
    CHECK (
        (scope_type = 'my' AND scope_variant IN ('assigned', 'created', 'involved', 'any'))
        OR (scope_type = 'workspace' AND (scope_variant IS NULL OR scope_variant IN ('members', 'agents')))
        OR (scope_type = 'project' AND (scope_variant IS NULL OR scope_variant IN ('members', 'agents')))
    ),
    CHECK (scope_type <> 'my' OR visibility = 'private')
);

CREATE TABLE issue_view_preference (
    workspace_id UUID NOT NULL,
    user_id UUID NOT NULL,
    scope_type TEXT NOT NULL CHECK (scope_type IN ('workspace', 'my', 'project')),
    scope_id UUID NOT NULL,
    prefs JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(prefs) = 'object'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id, scope_type, scope_id)
);
