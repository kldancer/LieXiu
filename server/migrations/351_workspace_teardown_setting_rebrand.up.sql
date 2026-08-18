-- Migration 243 predates the LieXiu rename and its trigger predicates still
-- inspect the legacy multica.workspace_teardown setting. The application has
-- since moved the transaction-local guard to liexiu.workspace_teardown, so
-- recreate the triggers against the active setting without rewriting history.

DROP TRIGGER IF EXISTS trg_atq_dirty_hourly ON agent_task_queue;
CREATE TRIGGER trg_atq_dirty_hourly
BEFORE UPDATE OF runtime_id, issue_id OR DELETE ON agent_task_queue
FOR EACH ROW
WHEN (current_setting('liexiu.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION enqueue_task_usage_hourly_dirty_for_atq();

DROP TRIGGER IF EXISTS trg_issue_delete_dirty_hourly ON issue;
CREATE TRIGGER trg_issue_delete_dirty_hourly
BEFORE DELETE ON issue
FOR EACH ROW
WHEN (current_setting('liexiu.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION enqueue_task_usage_hourly_dirty_for_issue_delete();

DROP TRIGGER IF EXISTS trg_tu_dirty_hourly ON task_usage;
CREATE TRIGGER trg_tu_dirty_hourly
BEFORE DELETE ON task_usage
FOR EACH ROW
WHEN (current_setting('liexiu.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION enqueue_task_usage_hourly_dirty_for_tu();
