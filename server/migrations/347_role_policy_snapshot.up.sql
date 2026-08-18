-- Immutable Mission/Duty policy facts captured before each duty is first used.
-- Relationships are application-owned: this table deliberately has no
-- foreign keys or cascading actions.

CREATE TABLE mission_role_policy_snapshot (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    mission_id UUID NOT NULL,
    duty TEXT NOT NULL CHECK (duty IN ('planner', 'executor', 'reviewer', 'integrator')),
    role_profile_id UUID NOT NULL,
    role_profile_key TEXT NOT NULL CHECK (char_length(role_profile_key) BETWEEN 1 AND 64),
    role_profile_version INTEGER NOT NULL CHECK (role_profile_version > 0),
    profile_name TEXT NOT NULL CHECK (char_length(profile_name) BETWEEN 1 AND 120),
    profile_description TEXT NOT NULL DEFAULT '' CHECK (char_length(profile_description) <= 1000),
    config JSONB NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    agent_id UUID,
    schema_version INTEGER NOT NULL DEFAULT 1 CHECK (schema_version = 1),
    content_hash TEXT NOT NULL CHECK (char_length(content_hash) = 64),
    frozen_by UUID NOT NULL,
    frozen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE mission_role_policy_snapshot IS
    'Immutable Mission/Duty RolePolicySnapshot facts; application code appends rows and never mutates them.';
