-- Wave 2.5 / Wave 3: remove saved views and arbitrary issue properties.
-- This is deliberately fail-closed. A non-empty legacy product must be
-- exported/restored from a pre-336 backup before this migration is applied.
DO $$
DECLARE
    property_count bigint;
    nonempty_issue_properties bigint;
    view_count bigint;
    preference_count bigint;
    view_pin_count bigint;
BEGIN
    SELECT COUNT(*) INTO property_count FROM issue_property;
    SELECT COUNT(*) INTO nonempty_issue_properties
    FROM issue
    WHERE properties <> '{}'::jsonb;
    SELECT COUNT(*) INTO view_count FROM issue_view;
    SELECT COUNT(*) INTO preference_count FROM issue_view_preference;
    SELECT COUNT(*) INTO view_pin_count
    FROM pinned_item
    WHERE item_type = 'view';

    IF property_count <> 0
       OR nonempty_issue_properties <> 0
       OR view_count <> 0
       OR preference_count <> 0
       OR view_pin_count <> 0 THEN
        RAISE EXCEPTION USING
            MESSAGE = format(
                'migration 336 refused: issue_property=%s, nonempty_issue_properties=%s, issue_view=%s, issue_view_preference=%s, view_pins=%s; restore/export legacy data before removal',
                property_count,
                nonempty_issue_properties,
                view_count,
                preference_count,
                view_pin_count
            );
    END IF;
END
$$;

ALTER TABLE pinned_item DROP CONSTRAINT IF EXISTS pinned_item_item_type_check;
ALTER TABLE pinned_item ADD CONSTRAINT pinned_item_item_type_check
    CHECK (item_type IN ('issue', 'project'));

DROP INDEX IF EXISTS idx_issue_view_owner;
DROP INDEX IF EXISTS idx_issue_view_shared;
DROP INDEX IF EXISTS idx_issue_properties_gin;
DROP INDEX IF EXISTS idx_issue_property_ws_name;
DROP INDEX IF EXISTS idx_issue_property_workspace;

DROP TABLE IF EXISTS issue_view_preference;
DROP TABLE IF EXISTS issue_view;

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_properties_size_limit;
ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_properties_is_object;
ALTER TABLE issue DROP COLUMN IF EXISTS properties;

DROP TABLE IF EXISTS issue_property;
