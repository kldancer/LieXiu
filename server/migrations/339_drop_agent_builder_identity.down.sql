-- Recreate the empty compatibility columns only. Historical hidden/system
-- agents must be restored from a pre-339 backup after rollback.
ALTER TABLE agent
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'user'
        CHECK (kind IN ('user', 'system')),
    ADD COLUMN system_key TEXT;

CREATE UNIQUE INDEX agent_system_identity_unique
    ON agent(workspace_id, owner_id, runtime_id, system_key)
    WHERE system_key IS NOT NULL;
