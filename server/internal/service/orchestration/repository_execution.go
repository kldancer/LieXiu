package orchestration

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

type RunExecutionMapping struct {
	AgentTaskID pgtype.UUID
	Status      string
}

func (r *Repository) GetRunExecutionMapping(ctx context.Context, workspaceID, runID pgtype.UUID) (RunExecutionMapping, error) {
	task, err := r.queries.GetAgentTaskByOrchestrationRunInWorkspace(ctx, db.GetAgentTaskByOrchestrationRunInWorkspaceParams{
		OrchestrationRunID: runID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return RunExecutionMapping{}, err
	}
	return RunExecutionMapping{AgentTaskID: task.ID, Status: task.Status}, nil
}
