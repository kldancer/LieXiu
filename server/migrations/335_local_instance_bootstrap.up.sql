CREATE TABLE local_instance (
    singleton_key BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton_key),
    owner_user_id UUID NOT NULL,
    canonical_workspace_id UUID NOT NULL,
    bootstrap_version INTEGER NOT NULL DEFAULT 1 CHECK (bootstrap_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
