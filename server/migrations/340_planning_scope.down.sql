ALTER TABLE artifact
    DROP CONSTRAINT artifact_scope_kind_check,
    ADD CONSTRAINT artifact_kind_check CHECK (
        kind IN ('branch', 'commit', 'diff', 'file', 'test_receipt', 'final_delivery')
    ),
    ALTER COLUMN task_node_id SET NOT NULL;

ALTER TABLE orchestration_run
    DROP CONSTRAINT orchestration_run_scope_purpose_check,
    ADD CONSTRAINT orchestration_run_purpose_check CHECK (
        purpose IN ('execute', 'review', 'integrate')
    ),
    ALTER COLUMN task_node_id SET NOT NULL;

ALTER TABLE orchestration_assignment
    DROP CONSTRAINT orchestration_assignment_scope_role_check,
    ADD CONSTRAINT orchestration_assignment_role_check CHECK (
        role IN ('executor', 'reviewer', 'integrator')
    ),
    ALTER COLUMN task_node_id SET NOT NULL;
