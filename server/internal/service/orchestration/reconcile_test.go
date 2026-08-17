package orchestration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestNormalizeRunObservation(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	baseRun := db.OrchestrationRun{
		Status: string(RunStatusQueued), DispatchDeadlineAt: timestamptz(now.Add(time.Minute)),
		TimeoutSeconds: 60,
	}
	baseMission := db.Mission{Status: string(MissionStatusRunning)}
	baseNode := db.TaskNode{Status: string(TaskStatusAssigned)}

	tests := []struct {
		name       string
		mission    db.Mission
		run        db.OrchestrationRun
		node       db.TaskNode
		task       *db.AgentTaskQueue
		wantRun    RunStatus
		wantTask   TaskStatus
		wantKind   string
		wantCancel bool
	}{
		{
			name: "waiting local directory is dispatched", mission: baseMission, run: baseRun, node: baseNode,
			task: &db.AgentTaskQueue{Status: "waiting_local_directory"}, wantRun: RunStatusDispatched,
		},
		{
			name: "running starts task node", mission: baseMission, run: baseRun, node: baseNode,
			task:    &db.AgentTaskQueue{Status: "running", StartedAt: timestamptz(now.Add(-time.Second))},
			wantRun: RunStatusRunning, wantTask: TaskStatusRunning,
		},
		{
			name: "completion requests review", mission: baseMission, run: baseRun, node: baseNode,
			task:    &db.AgentTaskQueue{Status: "completed", StartedAt: timestamptz(now.Add(-10 * time.Second)), CompletedAt: timestamptz(now)},
			wantRun: RunStatusSucceeded, wantTask: TaskStatusReview,
		},
		{
			name: "runtime recovery is normalized", mission: baseMission,
			run:     func() db.OrchestrationRun { value := baseRun; value.Status = string(RunStatusRunning); return value }(),
			node:    db.TaskNode{Status: string(TaskStatusRunning)},
			task:    &db.AgentTaskQueue{Status: "failed", FailureReason: pgtype.Text{String: "runtime_recovery", Valid: true}, CompletedAt: timestamptz(now)},
			wantRun: RunStatusFailed, wantTask: TaskStatusAssigned, wantKind: "runtime_offline",
		},
		{
			name: "unmapped dispatch deadline leaves task assigned for policy", mission: baseMission,
			run: func() db.OrchestrationRun {
				value := baseRun
				value.DispatchDeadlineAt = timestamptz(now.Add(-time.Second))
				return value
			}(),
			node: baseNode, wantRun: RunStatusFailed, wantKind: "dispatch_timeout",
		},
		{
			name: "active execution timeout requests cancel", mission: baseMission,
			run: func() db.OrchestrationRun {
				value := baseRun
				value.Status = string(RunStatusRunning)
				value.StartedAt = timestamptz(now.Add(-2 * time.Minute))
				return value
			}(),
			node: db.TaskNode{Status: string(TaskStatusRunning)}, task: &db.AgentTaskQueue{Status: "running"},
			wantRun: RunStatusFailed, wantTask: TaskStatusAssigned, wantKind: "timeout", wantCancel: true,
		},
		{
			name:    "mission cancellation beats late completion",
			mission: db.Mission{Status: string(MissionStatusCancelled)}, run: baseRun, node: baseNode,
			task:    &db.AgentTaskQueue{Status: "completed", CompletedAt: timestamptz(now)},
			wantRun: RunStatusCancelled, wantTask: TaskStatusCancelled,
		},
		{
			name:    "mission failure cancels parallel execution",
			mission: db.Mission{Status: string(MissionStatusFailed)}, run: baseRun, node: baseNode,
			task:    &db.AgentTaskQueue{Status: "running"},
			wantRun: RunStatusCancelled, wantTask: TaskStatusCancelled, wantCancel: true,
		},
		{
			name:    "terminal timeout keeps requesting cancellation",
			mission: baseMission,
			run: func() db.OrchestrationRun {
				value := baseRun
				value.Status = string(RunStatusFailed)
				value.FailureKind = textValue("timeout")
				value.FinishedAt = timestamptz(now)
				return value
			}(),
			node: baseNode, task: &db.AgentTaskQueue{Status: "running"},
			wantRun: RunStatusFailed, wantCancel: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation, err := normalizeRunObservation(test.mission, test.run, test.node, test.task, now)
			if err != nil {
				t.Fatal(err)
			}
			if observation.status != test.wantRun {
				t.Fatalf("run status = %q, want %q", observation.status, test.wantRun)
			}
			if test.wantTask != "" && (!observation.taskStatusValid || observation.taskStatus != test.wantTask) {
				t.Fatalf("task status = %q valid=%v, want %q", observation.taskStatus, observation.taskStatusValid, test.wantTask)
			}
			if observation.failureKind.String != test.wantKind {
				t.Fatalf("failure kind = %q, want %q", observation.failureKind.String, test.wantKind)
			}
			if observation.cancelExecution != test.wantCancel {
				t.Fatalf("cancel execution = %v, want %v", observation.cancelExecution, test.wantCancel)
			}
		})
	}
}

func TestNormalizeFailureKind(t *testing.T) {
	tests := map[string]string{
		"runtime_recovery":             "runtime_offline",
		"queued_expired":               "dispatch_timeout",
		"daemon timed_out":             "timeout",
		"protocol decode failure":      "protocol_error",
		"worktree setup":               "worktree_error",
		"agent_error.provider_network": "provider_network",
		"skill_bundle_unavailable":     "skill_bundle_unavailable",
		"unknown provider error":       "agent_error",
	}
	for input, expected := range tests {
		if actual := normalizeFailureKind(input); actual != expected {
			t.Fatalf("normalizeFailureKind(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestRunReconcilerRunsImmediatelyAtStartup(t *testing.T) {
	store := &recordingReconcileStore{called: make(chan struct{})}
	reconciler := NewRunReconciler(store, nil, RunReconcilerOptions{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reconciler.Run(ctx)
		close(done)
	}()
	select {
	case <-store.called:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("startup reconciliation did not run immediately")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconciler did not stop with its context")
	}
}

func TestRunReconcilerCancelsTimedOutExecutionThroughGateway(t *testing.T) {
	runID := newTestUUID()
	workspaceID := newTestUUID()
	taskID := newTestUUID()
	createdAt := timestamptz(time.Now().UTC())
	store := &staticReconcileStore{
		runs: []ReconcilableRun{{WorkspaceID: workspaceID, RunID: runID, CreatedAt: createdAt}},
		result: ReconcileRunResult{
			Run:             db.OrchestrationRun{ID: runID, Status: string(RunStatusFailed)},
			CancelExecution: true, CancelAgentTaskID: taskID,
			CancelExecutionReason: "orchestration run failed: timeout",
		},
	}
	gateway := &recordingExecutionGateway{}
	afterCalls := 0
	reconciler := NewRunReconciler(store, gateway, RunReconcilerOptions{AfterReconcile: func(context.Context, ReconcileRunResult) error {
		afterCalls++
		return nil
	}})
	processed, err := reconciler.ReconcileBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 || gateway.cancelCalls != 1 || afterCalls != 1 {
		t.Fatalf("processed=%d cancel_calls=%d after_calls=%d, want 1, 1, 1", processed, gateway.cancelCalls, afterCalls)
	}
	if gateway.lastCancel.AgentTaskID != taskID || gateway.lastCancel.Reason != store.result.CancelExecutionReason {
		t.Fatalf("unexpected cancellation request: %#v", gateway.lastCancel)
	}
}

type recordingReconcileStore struct {
	once   sync.Once
	called chan struct{}
}

func (s *recordingReconcileStore) ListReconcilableRuns(context.Context, ReconcileCursor, int) ([]ReconcilableRun, error) {
	s.once.Do(func() { close(s.called) })
	return nil, nil
}

func (*recordingReconcileStore) ReconcileRun(context.Context, ReconcileRunParams) (ReconcileRunResult, error) {
	return ReconcileRunResult{}, nil
}

type staticReconcileStore struct {
	runs   []ReconcilableRun
	result ReconcileRunResult
}

func (s *staticReconcileStore) ListReconcilableRuns(context.Context, ReconcileCursor, int) ([]ReconcilableRun, error) {
	return s.runs, nil
}

func (s *staticReconcileStore) ReconcileRun(context.Context, ReconcileRunParams) (ReconcileRunResult, error) {
	return s.result, nil
}

type recordingExecutionGateway struct {
	cancelCalls int
	lastCancel  CancelExecutionRequest
}

func (*recordingExecutionGateway) Enqueue(context.Context, EnqueueExecutionRequest) (EnqueueExecutionResult, error) {
	return EnqueueExecutionResult{}, nil
}

func (g *recordingExecutionGateway) Cancel(_ context.Context, request CancelExecutionRequest) (CancelExecutionResult, error) {
	g.cancelCalls++
	g.lastCancel = request
	return CancelExecutionResult{AgentTaskID: request.AgentTaskID, Status: "cancelled"}, nil
}
