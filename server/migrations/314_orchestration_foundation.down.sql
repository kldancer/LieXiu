ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS orchestration_run_id;
DROP TABLE IF EXISTS orchestration_activity;
DROP TABLE IF EXISTS review_verdict;
DROP TABLE IF EXISTS artifact;
DROP TABLE IF EXISTS orchestration_run;
DROP TABLE IF EXISTS orchestration_assignment;
DROP TABLE IF EXISTS task_node;
DROP TABLE IF EXISTS mission;
