DELETE FROM orchestration_activity
WHERE subject_type = 'mailbox_message';

ALTER TABLE orchestration_activity
    DROP CONSTRAINT orchestration_activity_subject_type_check,
    ADD CONSTRAINT orchestration_activity_subject_type_check CHECK (
        subject_type IN ('mission', 'task_node', 'assignment', 'run', 'artifact', 'review')
    );

DROP TABLE IF EXISTS orchestration_mailbox_message;
