package orchestration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	defaultDispatchTimeout = time.Minute
	defaultRunTimeout      = 30 * time.Minute
)

type AdvanceMissionCommand struct {
	WorkspaceID   pgtype.UUID
	MissionID     pgtype.UUID
	CorrelationID pgtype.UUID
}

type AdvanceMissionParams struct {
	WorkspaceID    pgtype.UUID
	MissionID      pgtype.UUID
	CorrelationID  pgtype.UUID
	ObservedAt     time.Time
	DispatchWindow time.Duration
	RunTimeout     time.Duration
}

type AdvanceMissionResult struct {
	Mission       db.Mission
	TaskNodes     []db.TaskNode
	CreatedRuns   []db.OrchestrationRun
	RunsToEnqueue []db.OrchestrationRun
	Activities    []db.OrchestrationActivity
	ActorID       pgtype.UUID
	Changed       bool
}

func (s *Service) AdvanceMission(ctx context.Context, command AdvanceMissionCommand) (AdvanceMissionResult, error) {
	if s == nil || s.repository == nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: service is not configured")
	}
	if !validUUID(command.WorkspaceID) || !validUUID(command.MissionID) || !validUUID(command.CorrelationID) {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: workspace_id, mission_id, and correlation_id are required")
	}
	result, err := s.repository.AdvanceMission(ctx, AdvanceMissionParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID,
		CorrelationID: command.CorrelationID, ObservedAt: time.Now().UTC(),
		DispatchWindow: defaultDispatchTimeout, RunTimeout: defaultRunTimeout,
	})
	if err != nil {
		return AdvanceMissionResult{}, err
	}
	if len(result.RunsToEnqueue) > 0 && s.execution == nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: execution gateway is not configured")
	}
	var enqueueErrors []error
	for _, run := range result.RunsToEnqueue {
		if _, enqueueErr := s.execution.Enqueue(ctx, EnqueueExecutionRequest{
			WorkspaceID: command.WorkspaceID, RunID: run.ID, ActorID: result.ActorID,
		}); enqueueErr != nil {
			enqueueErrors = append(enqueueErrors, fmt.Errorf("enqueue run %s: %w", uuidText(run.ID), enqueueErr))
		}
	}
	if err := errors.Join(enqueueErrors...); err != nil {
		return result, err
	}
	return result, nil
}
