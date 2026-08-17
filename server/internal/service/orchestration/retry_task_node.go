package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const activityTaskRetryRequested = "task.retry_requested"

var (
	ErrMissionNotRetryable  = errors.New("mission is not running or blocked")
	ErrTaskNodeNotRetryable = errors.New("task node is not blocked")
	ErrTaskRevisionConflict = errors.New("task node revision conflict")
)

type RetryTaskNodeCommand struct {
	WorkspaceID          pgtype.UUID
	MissionID            pgtype.UUID
	TaskNodeID           pgtype.UUID
	CommandID            pgtype.UUID
	CorrelationID        pgtype.UUID
	ActorID              pgtype.UUID
	ExpectedRevision     int64
	ExpectedTaskRevision int64
	Reason               string
}

type RetryTaskNodeParams struct {
	WorkspaceID          pgtype.UUID
	MissionID            pgtype.UUID
	TaskNodeID           pgtype.UUID
	CommandID            pgtype.UUID
	CorrelationID        pgtype.UUID
	ActorID              pgtype.UUID
	ExpectedRevision     int64
	ExpectedTaskRevision int64
	Reason               string
}

type RetryTaskNodeResult struct {
	Mission    db.Mission
	TaskNode   db.TaskNode
	Activity   db.OrchestrationActivity
	Advance    AdvanceMissionResult
	Idempotent bool
}

// RetryTaskNode is the owner command for a recoverably blocked TaskNode. It
// does not reopen failed terminal history. The command clears the block in one
// transaction, then runs the ordinary deterministic advance loop; replaying a
// partially completed request therefore safely finishes dispatch.
func (s *Service) RetryTaskNode(ctx context.Context, command RetryTaskNodeCommand) (RetryTaskNodeResult, error) {
	errs := validateCommandIdentity(command.WorkspaceID, command.MissionID, command.CommandID, command.CorrelationID, command.ActorID, true)
	if !validUUID(command.TaskNodeID) {
		errs = append(errs, ValidationError{Path: "task_node_id", Code: "invalid_uuid", Message: "task_node_id must be a non-zero UUID"})
	}
	if command.ExpectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	if command.ExpectedTaskRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_task_revision", Code: "invalid_revision", Message: "expected_task_revision must be at least 1"})
	}
	if len(errs) > 0 {
		return RetryTaskNodeResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return RetryTaskNodeResult{}, err
	}
	result, err := s.repository.RetryTaskNode(ctx, RetryTaskNodeParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, TaskNodeID: command.TaskNodeID,
		CommandID: command.CommandID, CorrelationID: command.CorrelationID, ActorID: command.ActorID,
		ExpectedRevision: command.ExpectedRevision, ExpectedTaskRevision: command.ExpectedTaskRevision,
		Reason: strings.TrimSpace(command.Reason),
	})
	if err != nil {
		return RetryTaskNodeResult{}, err
	}
	advance, err := s.AdvanceMission(ctx, AdvanceMissionCommand{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID,
		CorrelationID: correlationOrCommand(command.CorrelationID, command.CommandID),
	})
	if err != nil {
		return result, err
	}
	result.Advance = advance
	return result, nil
}

func (r *Repository) RetryTaskNode(ctx context.Context, params RetryTaskNodeParams) (RetryTaskNodeResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: repository is not configured")
	}
	dedupeKey, err := commandDedupeKey(params.CommandID)
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: %w", err)
	}
	correlationID := correlationOrCommand(params.CorrelationID, params.CommandID)
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)

	activity, err := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey,
	})
	if err == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return RetryTaskNodeResult{}, fmt.Errorf("retry task node: rollback idempotent transaction: %w", rollbackErr)
		}
		return r.loadRetryTaskNodeResult(ctx, params.WorkspaceID, params.MissionID, params.TaskNodeID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: check command: %w", err)
	}

	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{
		IssueID: params.MissionID, WorkspaceID: params.WorkspaceID,
	})
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: lock mission: %w", err)
	}
	if (MissionStatus(mission.Status) != MissionStatusRunning && MissionStatus(mission.Status) != MissionStatusBlocked) || mission.Revision != params.ExpectedRevision {
		if replay, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey}); replayErr == nil {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return RetryTaskNodeResult{}, fmt.Errorf("retry task node: rollback concurrent replay: %w", rollbackErr)
			}
			return r.loadRetryTaskNodeResult(ctx, params.WorkspaceID, params.MissionID, params.TaskNodeID, replay, true)
		} else if !errors.Is(replayErr, pgx.ErrNoRows) {
			return RetryTaskNodeResult{}, fmt.Errorf("retry task node: check concurrent replay: %w", replayErr)
		}
		if MissionStatus(mission.Status) != MissionStatusRunning && MissionStatus(mission.Status) != MissionStatusBlocked {
			return RetryTaskNodeResult{}, ErrMissionNotRetryable
		}
		return RetryTaskNodeResult{}, ErrRevisionConflict
	}
	node, err := qtx.LockTaskNodeForReconcile(ctx, db.LockTaskNodeForReconcileParams{
		TaskNodeID: params.TaskNodeID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
	})
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: lock task node: %w", err)
	}
	if TaskStatus(node.Status) != TaskStatusBlocked {
		return RetryTaskNodeResult{}, ErrTaskNodeNotRetryable
	}
	if node.Revision != params.ExpectedTaskRevision {
		return RetryTaskNodeResult{}, ErrTaskRevisionConflict
	}

	mission, err = qtx.ResumeMissionForTaskRetry(ctx, db.ResumeMissionForTaskRetryParams{
		MissionID: params.MissionID, WorkspaceID: params.WorkspaceID, ExpectedRevision: params.ExpectedRevision,
	})
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: resume mission: %w", err)
	}
	node, err = qtx.RetryBlockedTaskNode(ctx, db.RetryBlockedTaskNodeParams{
		TaskNodeID: params.TaskNodeID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
		ExpectedRevision: params.ExpectedTaskRevision,
	})
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: clear block: %w", err)
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: params.MissionID, WorkspaceID: params.WorkspaceID, Status: "in_progress"}); err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: update mission issue: %w", err)
	}

	sequence, err := allocateActivitySequence(ctx, qtx, params.WorkspaceID, params.MissionID)
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"reason": params.Reason, "task_node_id": uuidText(params.TaskNodeID)})
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: encode activity: %w", err)
	}
	activity, err = qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: params.TaskNodeID,
		Type: activityTaskRetryRequested, ActorType: "user", ActorID: params.ActorID,
		SubjectType: "task_node", SubjectID: params.TaskNodeID,
		CausationID: params.CommandID, CorrelationID: correlationID,
		PayloadVersion: 1, Payload: payload, DedupeKey: dedupeKey, Sequence: sequence,
	})
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: create activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("retry task node: commit: %w", err)
	}
	return RetryTaskNodeResult{Mission: mission, TaskNode: node, Activity: activity}, nil
}

func (r *Repository) loadRetryTaskNodeResult(ctx context.Context, workspaceID, missionID, taskNodeID pgtype.UUID, activity db.OrchestrationActivity, idempotent bool) (RetryTaskNodeResult, error) {
	if activity.Type != activityTaskRetryRequested || activity.SubjectType != "task_node" || activity.MissionID != missionID || activity.SubjectID != taskNodeID {
		return RetryTaskNodeResult{}, ErrCommandConflict
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("load retry task node mission: %w", err)
	}
	node, err := r.queries.GetTaskNodeInMission(ctx, db.GetTaskNodeInMissionParams{TaskNodeID: taskNodeID, WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return RetryTaskNodeResult{}, fmt.Errorf("load retry task node: %w", err)
	}
	return RetryTaskNodeResult{Mission: mission, TaskNode: node, Activity: activity, Idempotent: idempotent}, nil
}
