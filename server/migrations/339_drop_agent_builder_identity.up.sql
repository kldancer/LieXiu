-- Global Chat, Mika and Agent Builder are retired. Personal-v1 has one visible
-- Agent entity; RolePolicy/AgentTeam describe orchestration roles separately.
LOCK TABLE agent IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    hidden_count bigint;
    system_identity_count bigint;
BEGIN
    SELECT count(*) INTO hidden_count FROM agent WHERE kind <> 'user';
    SELECT count(*) INTO system_identity_count FROM agent WHERE system_key IS NOT NULL;

    IF hidden_count <> 0 OR system_identity_count <> 0 THEN
        RAISE EXCEPTION USING
            MESSAGE = format(
                'migration 339 refused: hidden_agents=%s, system_identity_agents=%s; export or convert retired Agent Builder/Mika rows before removal',
                hidden_count,
                system_identity_count
            );
    END IF;
END
$$;

DROP INDEX IF EXISTS agent_system_identity_unique;
ALTER TABLE agent
    DROP COLUMN system_key,
    DROP COLUMN kind;
