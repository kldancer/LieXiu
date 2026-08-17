package orchestration

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// ExecutionGateway is the only dependency the orchestrator has on the
// existing AgentTask execution plane.
type ExecutionGateway interface {
	Enqueue(ctx context.Context, request EnqueueExecutionRequest) (EnqueueExecutionResult, error)
	Cancel(ctx context.Context, request CancelExecutionRequest) (CancelExecutionResult, error)
}

type EnqueueExecutionRequest struct {
	WorkspaceID pgtype.UUID
	RunID       pgtype.UUID
	ActorID     pgtype.UUID
}

type EnqueueExecutionResult struct {
	AgentTaskID pgtype.UUID
	Status      string
	Idempotent  bool
}

type CancelExecutionRequest struct {
	AgentTaskID pgtype.UUID
	Reason      string
}

type CancelExecutionResult struct {
	AgentTaskID pgtype.UUID
	Status      string
}
