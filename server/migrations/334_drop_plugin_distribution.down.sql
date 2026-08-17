-- Plugin distribution state includes signed artifacts, immutable snapshots,
-- grants, bindings and execution pins. Recreating empty tables would not
-- restore that state, so this product-removal migration is forward-only.
DO $$
BEGIN
    RAISE EXCEPTION 'migration 334 is irreversible; restore a pre-334 database backup';
END
$$;
