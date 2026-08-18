package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	"github.com/kailonyang/liexiu/server/internal/util"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

var (
	ErrOrchestrationRunNotDispatchable = errors.New("orchestration run is not dispatchable")
	ErrNotOrchestrationTask            = errors.New("agent task is not linked to an orchestration run")
)

type EnqueueOrchestrationRunParams struct {
	WorkspaceID pgtype.UUID
	RunID       pgtype.UUID
	ActorID     pgtype.UUID
}

type EnqueueOrchestrationRunResult struct {
	Task       db.AgentTaskQueue
	Idempotent bool
}

// EnqueueOrchestrationRun atomically binds one queued orchestration Run to one
// AgentTask. The run row is locked before insertion, and the unique run mapping
// makes a retry after an uncertain commit return the original task.
func (s *TaskService) EnqueueOrchestrationRun(ctx context.Context, params EnqueueOrchestrationRunParams) (EnqueueOrchestrationRunResult, error) {
	if s == nil || s.Queries == nil || s.TxStarter == nil {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: task service is not transactionally configured")
	}
	if !validOrchestrationUUID(params.WorkspaceID) || !validOrchestrationUUID(params.RunID) || !validOrchestrationUUID(params.ActorID) {
		return EnqueueOrchestrationRunResult{}, ErrOrchestrationRunNotDispatchable
	}
	if existing, err := s.loadOrchestrationAgentTask(ctx, params.WorkspaceID, params.RunID); err == nil {
		if existing.Status == "queued" {
			s.notifyTaskAvailable(existing)
		}
		return EnqueueOrchestrationRunResult{Task: existing, Idempotent: true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: load existing mapping: %w", err)
	}

	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.Queries.WithTx(tx)
	dispatch, err := qtx.LockOrchestrationRunForEnqueue(ctx, db.LockOrchestrationRunForEnqueueParams{
		RunID: params.RunID, WorkspaceID: params.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EnqueueOrchestrationRunResult{}, ErrOrchestrationRunNotDispatchable
	}
	if err != nil {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: lock dispatch context: %w", err)
	}
	if existing, err := qtx.GetAgentTaskByOrchestrationRunInWorkspace(ctx, db.GetAgentTaskByOrchestrationRunInWorkspaceParams{
		OrchestrationRunID: params.RunID, WorkspaceID: params.WorkspaceID,
	}); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: commit replay: %w", err)
		}
		if existing.Status == "queued" {
			s.notifyTaskAvailable(existing)
		}
		return EnqueueOrchestrationRunResult{Task: existing, Idempotent: true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: recheck mapping: %w", err)
	}
	agent, err := qtx.LockAgentForOrchestrationEnqueue(ctx, db.LockAgentForOrchestrationEnqueueParams{
		AgentID: dispatch.AgentID, WorkspaceID: params.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EnqueueOrchestrationRunResult{}, ErrOrchestrationRunNotDispatchable
	}
	if err != nil {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: lock agent: %w", err)
	}
	if agent.ArchivedAt.Valid || !agent.RuntimeID.Valid || agent.RuntimeID != dispatch.RuntimeID {
		return EnqueueOrchestrationRunResult{}, ErrOrchestrationRunNotDispatchable
	}
	if _, err := qtx.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID: dispatch.IssueID, WorkspaceID: params.WorkspaceID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return EnqueueOrchestrationRunResult{}, ErrOrchestrationRunNotDispatchable
	} else if err != nil {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: validate task issue: %w", err)
	}

	task, err := qtx.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: dispatch.AgentID, RuntimeID: dispatch.RuntimeID, IssueID: dispatch.IssueID,
		Priority:             dispatch.TaskPriority,
		TaskContext:          dispatch.RunInput,
		TriggerSummary:       pgtype.Text{String: fmt.Sprintf("orchestration %s run %s", dispatch.Purpose, util.UUIDToString(params.RunID)), Valid: true},
		ForceFreshSession:    pgtype.Bool{Bool: true, Valid: true},
		OriginatorUserID:     params.ActorID,
		AccountableUserID:    params.ActorID,
		OriginatorSource:     pgtype.Text{String: "direct_human", Valid: true},
		TriggerEvidenceKind:  pgtype.Text{String: "orchestration_run", Valid: true},
		TriggerEvidenceRefID: params.RunID,
		OrchestrationRunID:   params.RunID,
	})
	if err != nil {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: create agent task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EnqueueOrchestrationRunResult{}, fmt.Errorf("enqueue orchestration run: commit: %w", err)
	}
	if s.Bus != nil {
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	}
	s.NotifyTaskEnqueued(ctx, task)
	return EnqueueOrchestrationRunResult{Task: task}, nil
}

func (s *TaskService) loadOrchestrationAgentTask(ctx context.Context, workspaceID, runID pgtype.UUID) (db.AgentTaskQueue, error) {
	return s.Queries.GetAgentTaskByOrchestrationRunInWorkspace(ctx, db.GetAgentTaskByOrchestrationRunInWorkspaceParams{
		OrchestrationRunID: runID, WorkspaceID: workspaceID,
	})
}

// TaskExecutionGateway adapts the existing TaskService execution plane to the
// orchestration package's vendor-neutral contract.
type TaskExecutionGateway struct {
	tasks *TaskService
}

func NewTaskExecutionGateway(tasks *TaskService) *TaskExecutionGateway {
	return &TaskExecutionGateway{tasks: tasks}
}

func (g *TaskExecutionGateway) Enqueue(ctx context.Context, request orchestration.EnqueueExecutionRequest) (orchestration.EnqueueExecutionResult, error) {
	if g == nil || g.tasks == nil {
		return orchestration.EnqueueExecutionResult{}, fmt.Errorf("enqueue execution: gateway is not configured")
	}
	result, err := g.tasks.EnqueueOrchestrationRun(ctx, EnqueueOrchestrationRunParams{
		WorkspaceID: request.WorkspaceID, RunID: request.RunID, ActorID: request.ActorID,
	})
	if err != nil {
		return orchestration.EnqueueExecutionResult{}, err
	}
	return orchestration.EnqueueExecutionResult{
		AgentTaskID: result.Task.ID, Status: result.Task.Status, Idempotent: result.Idempotent,
	}, nil
}

func (g *TaskExecutionGateway) Cancel(ctx context.Context, request orchestration.CancelExecutionRequest) (orchestration.CancelExecutionResult, error) {
	if g == nil || g.tasks == nil || g.tasks.Queries == nil {
		return orchestration.CancelExecutionResult{}, fmt.Errorf("cancel execution: gateway is not configured")
	}
	existing, err := g.tasks.Queries.GetAgentTask(ctx, request.AgentTaskID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !existing.OrchestrationRunID.Valid) {
		return orchestration.CancelExecutionResult{}, ErrNotOrchestrationTask
	}
	if err != nil {
		return orchestration.CancelExecutionResult{}, fmt.Errorf("cancel execution: validate agent task: %w", err)
	}
	task, err := g.tasks.CancelTaskWithReason(ctx, request.AgentTaskID, request.Reason, "orchestration_cancelled")
	if err != nil {
		return orchestration.CancelExecutionResult{}, err
	}
	return orchestration.CancelExecutionResult{AgentTaskID: task.ID, Status: task.Status}, nil
}

var _ orchestration.ExecutionGateway = (*TaskExecutionGateway)(nil)

func validOrchestrationUUID(value pgtype.UUID) bool {
	return value.Valid && value.Bytes != [16]byte{}
}
