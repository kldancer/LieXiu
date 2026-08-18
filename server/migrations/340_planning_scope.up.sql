ALTER TABLE orchestration_assignment
    ALTER COLUMN task_node_id DROP NOT NULL,
    DROP CONSTRAINT orchestration_assignment_role_check,
    ADD CONSTRAINT orchestration_assignment_scope_role_check CHECK (
        (task_node_id IS NULL AND role = 'planner')
        OR
        (task_node_id IS NOT NULL AND role IN ('executor', 'reviewer', 'integrator'))
    );

ALTER TABLE orchestration_run
    ALTER COLUMN task_node_id DROP NOT NULL,
    DROP CONSTRAINT orchestration_run_purpose_check,
    ADD CONSTRAINT orchestration_run_scope_purpose_check CHECK (
        (task_node_id IS NULL AND purpose = 'plan')
        OR
        (task_node_id IS NOT NULL AND purpose IN ('execute', 'review', 'integrate'))
    );

ALTER TABLE artifact
    ALTER COLUMN task_node_id DROP NOT NULL,
    DROP CONSTRAINT artifact_kind_check,
    ADD CONSTRAINT artifact_scope_kind_check CHECK (
        (task_node_id IS NULL AND kind = 'plan_proposal')
        OR
        (task_node_id IS NOT NULL AND kind IN ('branch', 'commit', 'diff', 'file', 'test_receipt', 'final_delivery'))
    );
