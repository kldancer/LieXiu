package service

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

func TestTaskExecutionGatewayEnqueueIsRunIdempotent(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping orchestration execution gateway integration test")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)
	var schemaReady bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('orchestration_run') IS NOT NULL`).Scan(&schemaReady); err != nil {
		t.Fatalf("check orchestration schema: %v", err)
	}
	if !schemaReady {
		t.Skip("orchestration migrations are not applied")
	}

	fixture := createExecutionGatewayFixture(t, ctx, pool)
	t.Cleanup(func() { cleanupExecutionGatewayFixture(t, pool, fixture) })
	bus := events.New()
	var queuedEvents atomic.Int32
	bus.Subscribe(protocol.EventTaskQueued, func(events.Event) { queuedEvents.Add(1) })
	taskService := NewTaskService(db.New(pool), pool, nil, bus)
	gateway := NewTaskExecutionGateway(taskService)

	request := orchestration.EnqueueExecutionRequest{
		WorkspaceID: fixture.workspaceID,
		RunID:       fixture.runID,
		ActorID:     fixture.userID,
	}
	type outcome struct {
		result orchestration.EnqueueExecutionResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 8)
	for range 8 {
		go func() {
			<-start
			result, enqueueErr := gateway.Enqueue(ctx, request)
			outcomes <- outcome{result: result, err: enqueueErr}
		}()
	}
	close(start)
	var taskID pgtype.UUID
	idempotentCount := 0
	for range 8 {
		current := <-outcomes
		if current.err != nil {
			t.Fatalf("concurrent Enqueue: %v", current.err)
		}
		if !taskID.Valid {
			taskID = current.result.AgentTaskID
		} else if current.result.AgentTaskID != taskID {
			t.Fatalf("run mapped to multiple tasks: %v and %v", taskID, current.result.AgentTaskID)
		}
		if current.result.Idempotent {
			idempotentCount++
		}
	}
	if idempotentCount != 7 {
		t.Fatalf("idempotent enqueue results = %d, want 7", idempotentCount)
	}
	if queuedEvents.Load() != 1 {
		t.Fatalf("task queued events = %d, want 1", queuedEvents.Load())
	}

	var mappingCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE orchestration_run_id = $1`, fixture.runID).Scan(&mappingCount); err != nil {
		t.Fatal(err)
	}
	if mappingCount != 1 {
		t.Fatalf("run mapping count = %d, want 1", mappingCount)
	}
	task, err := db.New(pool).GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatalf("load bridged task: %v", err)
	}
	if task.AgentID != fixture.agentID || task.RuntimeID != fixture.runtimeID || task.IssueID != fixture.taskNodeID {
		t.Fatalf("bridged task does not match frozen assignment: %#v", task)
	}
	if task.OrchestrationRunID != fixture.runID || !task.ForceFreshSession {
		t.Fatalf("bridged task is missing run identity or fresh-session fence: %#v", task)
	}
	if !task.TriggerEvidenceKind.Valid || task.TriggerEvidenceKind.String != "orchestration_run" || task.TriggerEvidenceRefID != fixture.runID {
		t.Fatalf("bridged task has wrong trigger evidence: %#v", task)
	}

	replayed, err := gateway.Enqueue(ctx, request)
	if err != nil {
		t.Fatalf("replay Enqueue after committed result: %v", err)
	}
	if !replayed.Idempotent || replayed.AgentTaskID != taskID || queuedEvents.Load() != 1 {
		t.Fatalf("committed replay changed observable result: %#v events=%d", replayed, queuedEvents.Load())
	}
	_, err = gateway.Enqueue(ctx, orchestration.EnqueueExecutionRequest{
		WorkspaceID: orchestrationTestUUID(), RunID: fixture.runID, ActorID: fixture.userID,
	})
	if !errors.Is(err, ErrOrchestrationRunNotDispatchable) {
		t.Fatalf("cross-workspace Enqueue error = %v, want ErrOrchestrationRunNotDispatchable", err)
	}
	_, err = gateway.Enqueue(ctx, orchestration.EnqueueExecutionRequest{
		WorkspaceID: fixture.workspaceID, RunID: fixture.runID,
	})
	if !errors.Is(err, ErrOrchestrationRunNotDispatchable) {
		t.Fatalf("invalid actor Enqueue error = %v, want ErrOrchestrationRunNotDispatchable", err)
	}
	_, err = gateway.Cancel(ctx, orchestration.CancelExecutionRequest{AgentTaskID: orchestrationTestUUID()})
	if !errors.Is(err, ErrNotOrchestrationTask) {
		t.Fatalf("unmapped Cancel error = %v, want ErrNotOrchestrationTask", err)
	}

	orchestrator := orchestration.NewService(
		db.New(pool), orchestration.NewRepository(db.New(pool), pool), gateway,
		orchestration.DefaultPlanHardLimits(),
	)
	cancelCommandID := orchestrationTestUUID()
	cancelledMission, err := orchestrator.CancelMission(ctx, orchestration.CancelMissionCommand{
		WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID,
		CommandID: cancelCommandID, ActorID: fixture.userID, ExpectedRevision: 1,
		Reason: "mission cancelled",
	})
	if err != nil {
		t.Fatalf("CancelMission through execution gateway: %v", err)
	}
	if cancelledMission.Mission.Status != string(orchestration.MissionStatusCancelled) || len(cancelledMission.ActiveRuns) != 1 {
		t.Fatalf("unexpected cancelled mission result: %#v", cancelledMission)
	}
	cancelledTask, err := db.New(pool).GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatalf("load cancelled execution task: %v", err)
	}
	if cancelledTask.Status != "cancelled" {
		t.Fatalf("cancelled execution task status = %q, want cancelled", cancelledTask.Status)
	}
	replayedCancel, err := orchestrator.CancelMission(ctx, orchestration.CancelMissionCommand{
		WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID,
		CommandID: cancelCommandID, ActorID: fixture.userID, ExpectedRevision: 1,
		Reason: "mission cancelled",
	})
	if err != nil {
		t.Fatalf("replay CancelMission: %v", err)
	}
	if !replayedCancel.Idempotent || replayedCancel.Mission.Status != string(orchestration.MissionStatusCancelled) {
		t.Fatalf("cancel replay changed result: %#v", replayedCancel)
	}
}

type executionGatewayFixture struct {
	userID       pgtype.UUID
	workspaceID  pgtype.UUID
	runtimeID    pgtype.UUID
	agentID      pgtype.UUID
	missionID    pgtype.UUID
	taskNodeID   pgtype.UUID
	assignmentID pgtype.UUID
	runID        pgtype.UUID
}

func createExecutionGatewayFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) executionGatewayFixture {
	t.Helper()
	suffix := uuid.NewString()
	var fixture executionGatewayFixture
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Execution gateway test', $1) RETURNING id`, "execution-gateway-"+suffix+"@liexiu.test").Scan(&fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix, issue_counter) VALUES ('Execution gateway test', $1, '', 'EGW', 2) RETURNING id`, "execution-gateway-"+suffix).Scan(&fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, name, runtime_mode, provider, status, device_info,
			metadata, last_seen_at, visibility, owner_id
		) VALUES ($1, 'Execution gateway runtime', 'cloud', 'test', 'online', 'test', '{}', now(), 'private', $2)
		RETURNING id
	`, fixture.workspaceID, fixture.userID).Scan(&fixture.runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		) VALUES ($1, 'Execution gateway agent', '', 'cloud', '{}', $2, 'private', 1, $3)
		RETURNING id
	`, fixture.workspaceID, fixture.runtimeID, fixture.userID).Scan(&fixture.agentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, position, number)
		VALUES ($1, 'Execution gateway mission', 'in_progress', 'none', 'member', $2, 0, 1)
		RETURNING id
	`, fixture.workspaceID, fixture.userID).Scan(&fixture.missionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, description, status, priority, creator_type, creator_id, parent_issue_id, position, number)
		VALUES ($1, 'Execution gateway task', 'Execute the assigned run', 'todo', 'none', 'member', $2, $3, 0, 2)
		RETURNING id
	`, fixture.workspaceID, fixture.userID, fixture.missionID).Scan(&fixture.taskNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mission (issue_id, workspace_id, status, created_by) VALUES ($1, $2, 'running', $3)`, fixture.missionID, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_node (
			issue_id, workspace_id, mission_id, node_key, role,
			acceptance_criteria, artifact_kinds, priority, status
		) VALUES ($1, $2, $3, 'A', 'executor', '["artifact exists"]', '["commit"]', 10, 'assigned')
	`, fixture.taskNodeID, fixture.workspaceID, fixture.missionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orchestration_assignment (
			workspace_id, mission_id, task_node_id, role, agent_id,
			runtime_id, status, sequence, created_by
		) VALUES ($1, $2, $3, 'executor', $4, $5, 'active', 1, $6)
		RETURNING id
	`, fixture.workspaceID, fixture.missionID, fixture.taskNodeID, fixture.agentID, fixture.runtimeID, fixture.userID).Scan(&fixture.assignmentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orchestration_run (
			workspace_id, mission_id, task_node_id, assignment_id, purpose,
			attempt, status, input, dispatch_deadline_at, timeout_seconds
		) VALUES ($1, $2, $3, $4, 'execute', 1, 'queued', '{}', now() + interval '5 minutes', 300)
		RETURNING id
	`, fixture.workspaceID, fixture.missionID, fixture.taskNodeID, fixture.assignmentID).Scan(&fixture.runID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func cleanupExecutionGatewayFixture(t *testing.T, pool *pgxpool.Pool, fixture executionGatewayFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM agent_task_queue WHERE orchestration_run_id = $1`, fixture.runID); err != nil {
		t.Errorf("cleanup agent task: %v", err)
	}
	for _, statement := range []string{
		`DELETE FROM orchestration_run WHERE workspace_id = $1`,
		`DELETE FROM orchestration_assignment WHERE workspace_id = $1`,
		`DELETE FROM orchestration_activity WHERE workspace_id = $1`,
		`DELETE FROM task_node WHERE workspace_id = $1`,
		`DELETE FROM mission WHERE workspace_id = $1`,
		`DELETE FROM workspace WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, statement, fixture.workspaceID); err != nil {
			t.Errorf("cleanup %q: %v", statement, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, fixture.userID); err != nil {
		t.Errorf("cleanup user: %v", err)
	}
}

func orchestrationTestUUID() pgtype.UUID {
	value := uuid.New()
	return pgtype.UUID{Bytes: value, Valid: true}
}
