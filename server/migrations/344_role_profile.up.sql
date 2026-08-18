-- Versioned, workspace-scoped configuration for the four fixed orchestration
-- duties. A profile may customize execution policy, but its duty remains one
-- of the state-machine values understood by orchestration.
--
-- Relationships are application-owned: no foreign keys or cascades.
-- Concurrent indexes live in follow-up migrations.

CREATE TABLE role_profile (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    profile_key TEXT NOT NULL CHECK (char_length(profile_key) BETWEEN 1 AND 64),
    version INTEGER NOT NULL CHECK (version > 0),
    duty TEXT NOT NULL CHECK (duty IN ('planner', 'executor', 'reviewer', 'integrator')),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 1000),
    config JSONB NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    command_id UUID NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE role_profile IS
    'Append-only RoleProfile versions. Custom names and policy never create new orchestration duties or state-machine branches.';
