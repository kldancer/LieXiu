-- Personal-v1 keeps Activity, TaskMessage, Artifact, Review and Human Gate as
-- project facts, but removes the generic social-notification product. Refuse
-- physical deletion unless every retired table is empty; data recovery belongs
-- to the pre-338 database backup, not to the down migration.
LOCK TABLE
    inbox_item,
    issue_subscriber,
    notification_preference,
    comment_reaction,
    issue_reaction,
    pinned_item
IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    blockers jsonb;
BEGIN
    SELECT COALESCE(jsonb_object_agg(object_name, row_count), '{}'::jsonb)
    INTO blockers
    FROM (
        SELECT object_name, row_count
        FROM (
            SELECT 'inbox_item' AS object_name, count(*)::bigint AS row_count FROM inbox_item
            UNION ALL SELECT 'issue_subscriber', count(*) FROM issue_subscriber
            UNION ALL SELECT 'notification_preference', count(*) FROM notification_preference
            UNION ALL SELECT 'comment_reaction', count(*) FROM comment_reaction
            UNION ALL SELECT 'issue_reaction', count(*) FROM issue_reaction
            UNION ALL SELECT 'pinned_item', count(*) FROM pinned_item
        ) AS counts
        WHERE row_count <> 0
    ) AS nonempty;

    IF blockers <> '{}'::jsonb THEN
        RAISE EXCEPTION USING
            MESSAGE = format(
                'migration 338 refused: retired collaboration tables are not empty: %s; export or restore from a pre-338 backup before removal',
                blockers::text
            );
    END IF;
END
$$;

DROP TABLE pinned_item;
DROP TABLE issue_reaction;
DROP TABLE comment_reaction;
DROP TABLE notification_preference;
DROP TABLE issue_subscriber;
DROP TABLE inbox_item;
