-- The external-channel and Composio removal is intentionally forward-only.
-- Restoring provider credentials, webhook state, media ledgers and OAuth
-- account records from an application rollback would be unsafe and incomplete.
-- Roll back with a database backup taken before migration 332 instead.
DO $$
BEGIN
    RAISE EXCEPTION 'migration 332 is irreversible; restore a pre-332 database backup';
END
$$;
