package orchestration

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestRepositoryReconcileRunTerminalFactsAreAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping run reconciler integration test")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)
	var schemaReady bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('orchestration_run') IS NOT NULL`).Scan(&schemaReady); err != nil {
		t.Fatal(err)
	}
	if !schemaReady {
		t.Skip("orchestration migrations are not applied")
	}

	fixture := newReconcileIntegrationFixture(t, ctx, pool)
	t.Cleanup(func() { fixture.cleanup(t, pool) })
	repository := NewRepository(db.New(pool), pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("concurrent late completion requests review once", func(t *testing.T) {
		item := fixture.createRun(t, ctx, pool, reconcileRunSpec{
			missionStatus: MissionStatusRunning, nodeStatus: TaskStatusAssigned,
			runStatus: RunStatusQueued, taskStatus: "completed",
			startedAt: now.Add(-10 * time.Second), completedAt: now,
		})
		start := make(chan struct{})
		results := make(chan ReconcileRunResult, 8)
		errors := make(chan error, 8)
		var workers sync.WaitGroup
		for range 8 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				result, reconcileErr := repository.ReconcileRun(ctx, ReconcileRunParams{
					WorkspaceID: fixture.workspaceID, RunID: item.runID, ObservedAt: now,
				})
				results <- result
				errors <- reconcileErr
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		close(errors)
		for reconcileErr := range errors {
			if reconcileErr != nil {
				t.Fatalf("concurrent ReconcileRun: %v", reconcileErr)
			}
		}
		changed := 0
		for result := range results {
			if result.Changed {
				changed++
			}
		}
		if changed != 1 {
			t.Fatalf("changed results = %d, want 1", changed)
		}
		assertReconciledState(t, ctx, pool, item, RunStatusSucceeded, TaskStatusReview, "")
		assertActivityTypes(t, ctx, pool, item.runID, []string{activityRunSucceeded, activityTaskReviewRequested})
		replayed, err := repository.ReconcileRun(ctx, ReconcileRunParams{
			WorkspaceID: fixture.workspaceID, RunID: item.runID, ObservedAt: now.Add(time.Minute),
		})
		if err != nil || replayed.Changed || len(replayed.Activities) != 0 {
			t.Fatalf("terminal replay changed state: result=%#v error=%v", replayed, err)
		}
	})

	t.Run("runtime recovery becomes retryable offline failure", func(t *testing.T) {
		item := fixture.createRun(t, ctx, pool, reconcileRunSpec{
			missionStatus: MissionStatusRunning, nodeStatus: TaskStatusRunning,
			runStatus: RunStatusRunning, taskStatus: "failed",
			startedAt: now.Add(-5 * time.Second), completedAt: now,
			failureReason: "runtime_recovery", failureMessage: "runtime restarted while task was active",
		})
		result, err := repository.ReconcileRun(ctx, ReconcileRunParams{
			WorkspaceID: fixture.workspaceID, RunID: item.runID, ObservedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed || result.CancelExecution {
			t.Fatalf("unexpected offline result: %#v", result)
		}
		assertReconciledState(t, ctx, pool, item, RunStatusFailed, TaskStatusAssigned, "runtime_offline")
		assertActivityTypes(t, ctx, pool, item.runID, []string{activityRunFailed, activityTaskAssigned})
	})

	t.Run("protocol failure fails closed without a retry run", func(t *testing.T) {
		item := fixture.createRun(t, ctx, pool, reconcileRunSpec{
			missionStatus: MissionStatusRunning, nodeStatus: TaskStatusRunning,
			runStatus: RunStatusRunning, taskStatus: "failed",
			failureReason: "protocol decode failure", completedAt: now,
		})
		if _, err := repository.ReconcileRun(ctx, ReconcileRunParams{
			WorkspaceID: fixture.workspaceID, RunID: item.runID, ObservedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		advanced, err := repository.AdvanceMission(ctx, AdvanceMissionParams{
			WorkspaceID: fixture.workspaceID, MissionID: item.missionID,
			CorrelationID: newTestUUID(), ObservedAt: now,
			DispatchWindow: time.Minute, RunTimeout: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(advanced.CreatedRuns) != 0 {
			t.Fatalf("protocol failure created retry runs: %#v", advanced.CreatedRuns)
		}
		assertReconciledState(t, ctx, pool, item, RunStatusFailed, TaskStatusFailed, "protocol_error")
	})

	t.Run("mission cancellation wins over late success", func(t *testing.T) {
		item := fixture.createRun(t, ctx, pool, reconcileRunSpec{
			missionStatus: MissionStatusCancelled, nodeStatus: TaskStatusCancelled,
			runStatus: RunStatusQueued, taskStatus: "completed",
			startedAt: now.Add(-time.Second), completedAt: now,
		})
		result, err := repository.ReconcileRun(ctx, ReconcileRunParams{
			WorkspaceID: fixture.workspaceID, RunID: item.runID, ObservedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed || result.Run.Status != string(RunStatusCancelled) {
			t.Fatalf("late success overrode cancellation: %#v", result)
		}
		assertReconciledState(t, ctx, pool, item, RunStatusCancelled, TaskStatusCancelled, "")
		assertActivityTypes(t, ctx, pool, item.runID, []string{activityRunCancelled})
	})

	t.Run("missed enqueue deadline remains assigned for retry policy", func(t *testing.T) {
		item := fixture.createRun(t, ctx, pool, reconcileRunSpec{
			missionStatus: MissionStatusRunning, nodeStatus: TaskStatusAssigned,
			runStatus: RunStatusQueued, withoutTask: true, dispatchDeadline: now.Add(-time.Second),
		})
		result, err := repository.ReconcileRun(ctx, ReconcileRunParams{
			WorkspaceID: fixture.workspaceID, RunID: item.runID, ObservedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed || result.CancelExecution {
			t.Fatalf("unexpected dispatch timeout result: %#v", result)
		}
		assertReconciledState(t, ctx, pool, item, RunStatusFailed, TaskStatusAssigned, "dispatch_timeout")
		assertActivityTypes(t, ctx, pool, item.runID, []string{activityRunFailed})
		advanced, err := repository.AdvanceMission(ctx, AdvanceMissionParams{
			WorkspaceID: fixture.workspaceID, MissionID: item.missionID,
			CorrelationID: newTestUUID(), ObservedAt: now,
			DispatchWindow: time.Minute, RunTimeout: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(advanced.CreatedRuns) != 1 || advanced.CreatedRuns[0].Attempt != 2 || advanced.CreatedRuns[0].RetryOfID != item.runID {
			t.Fatalf("dispatch timeout retry lost lineage: %#v", advanced.CreatedRuns)
		}
		assertReconciledState(t, ctx, pool, item, RunStatusFailed, TaskStatusAssigned, "dispatch_timeout")
	})

	t.Run("active timeout returns a durable cancellation request", func(t *testing.T) {
		item := fixture.createRun(t, ctx, pool, reconcileRunSpec{
			missionStatus: MissionStatusRunning, nodeStatus: TaskStatusRunning,
			runStatus: RunStatusRunning, taskStatus: "running",
			startedAt: now.Add(-2 * time.Minute), timeoutSeconds: 30,
		})
		result, err := repository.ReconcileRun(ctx, ReconcileRunParams{
			WorkspaceID: fixture.workspaceID, RunID: item.runID, ObservedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed || !result.CancelExecution || result.CancelAgentTaskID != item.agentTaskID {
			t.Fatalf("timeout did not return its cancellation request: %#v", result)
		}
		assertReconciledState(t, ctx, pool, item, RunStatusFailed, TaskStatusAssigned, "timeout")
		assertActivityTypes(t, ctx, pool, item.runID, []string{activityRunFailed, activityTaskAssigned})
		pending, err := repository.ListReconcilableRuns(ctx, ReconcileCursor{}, 256)
		if err != nil {
			t.Fatal(err)
		}
		if !containsReconcilableRun(pending, item.runID) {
			t.Fatal("terminal timeout with an active AgentTask was not retained for cancellation retry")
		}
		replayed, err := repository.ReconcileRun(ctx, ReconcileRunParams{
			WorkspaceID: fixture.workspaceID, RunID: item.runID, ObservedAt: now.Add(time.Second),
		})
		if err != nil || replayed.Changed || !replayed.CancelExecution {
			t.Fatalf("timeout cancellation replay is not durable: result=%#v error=%v", replayed, err)
		}
	})
}

type reconcileIntegrationFixture struct {
	userID      pgtype.UUID
	workspaceID pgtype.UUID
	runtimeID   pgtype.UUID
	agentID     pgtype.UUID
	nextNumber  int
}

type reconcileRunSpec struct {
	missionStatus    MissionStatus
	nodeStatus       TaskStatus
	runStatus        RunStatus
	taskStatus       string
	startedAt        time.Time
	completedAt      time.Time
	dispatchDeadline time.Time
	timeoutSeconds   int
	failureReason    string
	failureMessage   string
	withoutTask      bool
}

type reconcileRunFixture struct {
	missionID   pgtype.UUID
	taskNodeID  pgtype.UUID
	runID       pgtype.UUID
	agentTaskID pgtype.UUID
}

func newReconcileIntegrationFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *reconcileIntegrationFixture {
	t.Helper()
	suffix := uuid.NewString()
	fixture := &reconcileIntegrationFixture{nextNumber: 1}
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Run reconciler test', $1) RETURNING id`, "run-reconciler-"+suffix+"@liexiu.test").Scan(&fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ('Run reconciler test', $1, '', 'RRC') RETURNING id`, "run-reconciler-"+suffix).Scan(&fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id)
		VALUES ($1, 'Run reconciler runtime', 'cloud', 'test', 'online', 'test', '{}', now(), 'private', $2)
		RETURNING id
	`, fixture.workspaceID, fixture.userID).Scan(&fixture.runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
		VALUES ($1, 'Run reconciler agent', '', 'cloud', '{}', $2, 'private', 1, $3)
		RETURNING id
	`, fixture.workspaceID, fixture.runtimeID, fixture.userID).Scan(&fixture.agentID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *reconcileIntegrationFixture) createRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, spec reconcileRunSpec) reconcileRunFixture {
	t.Helper()
	if spec.dispatchDeadline.IsZero() {
		spec.dispatchDeadline = time.Now().UTC().Add(time.Minute)
	}
	if spec.timeoutSeconds == 0 {
		spec.timeoutSeconds = 60
	}
	var item reconcileRunFixture
	missionNumber := f.nextNumber
	taskNumber := f.nextNumber + 1
	f.nextNumber += 2
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, position, number)
		VALUES ($1, 'Reconcile mission', 'in_progress', 'none', 'member', $2, 0, $3)
		RETURNING id
	`, f.workspaceID, f.userID, missionNumber).Scan(&item.missionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, parent_issue_id, position, number)
		VALUES ($1, 'Reconcile task', 'in_progress', 'none', 'member', $2, $3, 0, $4)
		RETURNING id
	`, f.workspaceID, f.userID, item.missionID, taskNumber).Scan(&item.taskNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mission (issue_id, workspace_id, status, created_by) VALUES ($1, $2, $3, $4)`, item.missionID, f.workspaceID, spec.missionStatus, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_node (issue_id, workspace_id, mission_id, node_key, role, acceptance_criteria, artifact_kinds, priority, status)
		VALUES ($1, $2, $3, $4, 'executor', '["done"]', '["commit"]', 10, $5)
	`, item.taskNodeID, f.workspaceID, item.missionID, "node-"+uuid.NewString(), spec.nodeStatus); err != nil {
		t.Fatal(err)
	}
	var assignmentID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO orchestration_assignment (workspace_id, mission_id, task_node_id, role, agent_id, runtime_id, status, sequence, created_by)
		VALUES ($1, $2, $3, 'executor', $4, $5, 'active', 1, $6)
		RETURNING id
	`, f.workspaceID, item.missionID, item.taskNodeID, f.agentID, f.runtimeID, f.userID).Scan(&assignmentID); err != nil {
		t.Fatal(err)
	}
	var startedAt any
	if !spec.startedAt.IsZero() {
		startedAt = spec.startedAt
	}
	var finishedAt any
	if spec.runStatus == RunStatusSucceeded || spec.runStatus == RunStatusFailed || spec.runStatus == RunStatusCancelled {
		finishedAt = time.Now().UTC()
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orchestration_run (
			workspace_id, mission_id, task_node_id, assignment_id, purpose, attempt, status,
			dispatch_deadline_at, timeout_seconds, started_at, finished_at
		) VALUES ($1, $2, $3, $4, 'execute', 1, $5, $6, $7, $8, $9)
		RETURNING id
	`, f.workspaceID, item.missionID, item.taskNodeID, assignmentID, spec.runStatus, spec.dispatchDeadline, spec.timeoutSeconds, startedAt, finishedAt).Scan(&item.runID); err != nil {
		t.Fatal(err)
	}
	if spec.withoutTask {
		return item
	}
	var completedAt any
	if !spec.completedAt.IsZero() {
		completedAt = spec.completedAt
	}
	var failureReason any
	if spec.failureReason != "" {
		failureReason = spec.failureReason
	}
	var failureMessage any
	if spec.failureMessage != "" {
		failureMessage = spec.failureMessage
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, issue_id, status, runtime_id, started_at, completed_at,
			failure_reason, error, orchestration_run_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, f.agentID, item.taskNodeID, spec.taskStatus, f.runtimeID, startedAt, completedAt, failureReason, failureMessage, item.runID).Scan(&item.agentTaskID); err != nil {
		t.Fatal(err)
	}
	return item
}

func (f *reconcileIntegrationFixture) cleanup(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`DELETE FROM orchestration_activity WHERE workspace_id = $1`,
		`DELETE FROM orchestration_run WHERE workspace_id = $1`,
		`DELETE FROM orchestration_assignment WHERE workspace_id = $1`,
		`DELETE FROM task_node WHERE workspace_id = $1`,
		`DELETE FROM mission WHERE workspace_id = $1`,
		`DELETE FROM workspace WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, statement, f.workspaceID); err != nil {
			t.Errorf("cleanup %q: %v", statement, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, f.userID); err != nil {
		t.Errorf("cleanup user: %v", err)
	}
}

func assertReconciledState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, item reconcileRunFixture, runStatus RunStatus, taskStatus TaskStatus, failureKind string) {
	t.Helper()
	var actualRunStatus, actualFailureKind, actualTaskStatus string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(failure_kind, '') FROM orchestration_run WHERE id = $1`, item.runID).Scan(&actualRunStatus, &actualFailureKind); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM task_node WHERE issue_id = $1`, item.taskNodeID).Scan(&actualTaskStatus); err != nil {
		t.Fatal(err)
	}
	if actualRunStatus != runStatus.String() || actualTaskStatus != taskStatus.String() || actualFailureKind != failureKind {
		t.Fatalf("state = run:%s task:%s failure:%s, want run:%s task:%s failure:%s", actualRunStatus, actualTaskStatus, actualFailureKind, runStatus, taskStatus, failureKind)
	}
}

func assertActivityTypes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID pgtype.UUID, expected []string) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT type FROM orchestration_activity WHERE run_id = $1 ORDER BY sequence`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var activityType string
		if err := rows.Scan(&activityType); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, activityType)
	}
	if len(actual) != len(expected) {
		t.Fatalf("activity types = %#v, want %#v", actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("activity types = %#v, want %#v", actual, expected)
		}
	}
}

func containsReconcilableRun(runs []ReconcilableRun, runID pgtype.UUID) bool {
	for _, run := range runs {
		if run.RunID == runID {
			return true
		}
	}
	return false
}
