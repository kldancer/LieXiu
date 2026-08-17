package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

var ErrOwnerRequired = errors.New("workspace owner permission is required")

// CommandValidationError reports all input errors that can be determined
// without changing orchestration state.
type CommandValidationError struct {
	Errors []ValidationError
}

func (e CommandValidationError) Error() string {
	return fmt.Sprintf("command validation failed with %d error(s)", len(e.Errors))
}

// Service is the only application entry point for orchestration commands.
// It owns authorization and input validation; Repository owns atomic writes.
type Service struct {
	queries    *db.Queries
	repository *Repository
	execution  ExecutionGateway
	hardLimits PlanLimits
}

func NewService(queries *db.Queries, repository *Repository, execution ExecutionGateway, hardLimits PlanLimits) *Service {
	return &Service{queries: queries, repository: repository, execution: execution, hardLimits: hardLimits}
}

// NewRunReconciler builds the background reconciler from the same repository
// and execution gateway used by this service.
func (s *Service) NewRunReconciler(options RunReconcilerOptions) *RunReconciler {
	if s == nil {
		return NewRunReconciler(nil, nil, options)
	}
	return NewRunReconciler(s.repository, s.execution, options)
}

type CreateMissionCommand struct {
	WorkspaceID   pgtype.UUID
	CommandID     pgtype.UUID
	CorrelationID pgtype.UUID
	ActorID       pgtype.UUID
	Title         string
	Description   pgtype.Text
	ProjectID     pgtype.UUID
	Limits        PlanLimits
}

type SubmitPlanCommand struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ActorID          pgtype.UUID
	ExpectedRevision int64
	Plan             Plan
}

type StartMissionCommand struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ActorID          pgtype.UUID
	ExpectedRevision int64
}

type CancelMissionCommand struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ActorID          pgtype.UUID
	ExpectedRevision int64
	Reason           string
}

func (s *Service) CreateMission(ctx context.Context, command CreateMissionCommand) (CreateMissionResult, error) {
	errs := validateCommandIdentity(command.WorkspaceID, pgtype.UUID{}, command.CommandID, command.CorrelationID, command.ActorID, false)
	if strings.TrimSpace(command.Title) == "" {
		errs = append(errs, ValidationError{Path: "title", Code: "missing_title", Message: "title is required"})
	}
	errs = append(errs, validatePlanLimits(command.Limits, s.hardLimits)...)
	if len(errs) > 0 {
		return CreateMissionResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return CreateMissionResult{}, err
	}
	if command.ProjectID.Valid {
		if _, err := s.queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
			ID: command.ProjectID, WorkspaceID: command.WorkspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreateMissionResult{}, CommandValidationError{Errors: []ValidationError{{
					Path: "project_id", Code: "project_not_found", Message: "project does not belong to the workspace",
				}}}
			}
			return CreateMissionResult{}, fmt.Errorf("create mission: validate project: %w", err)
		}
	}
	return s.repository.CreateMission(ctx, CreateMissionParams{
		WorkspaceID: command.WorkspaceID, CommandID: command.CommandID,
		CorrelationID: command.CorrelationID, ActorID: command.ActorID,
		Title: strings.TrimSpace(command.Title), Description: command.Description,
		ProjectID: command.ProjectID, Limits: command.Limits,
	})
}

func (s *Service) SubmitPlan(ctx context.Context, command SubmitPlanCommand) (SubmitPlanResult, error) {
	errs := validateCommandIdentity(command.WorkspaceID, command.MissionID, command.CommandID, command.CorrelationID, command.ActorID, true)
	if command.ExpectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	if len(errs) > 0 {
		return SubmitPlanResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return SubmitPlanResult{}, err
	}
	if planErrs := ValidatePlan(command.Plan, uuidText(command.MissionID), s.hardLimits); len(planErrs) > 0 {
		return SubmitPlanResult{}, CommandValidationError{Errors: planErrs}
	}
	return s.repository.SubmitPlan(ctx, SubmitPlanParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID,
		CommandID: command.CommandID, CorrelationID: command.CorrelationID,
		ActorType: "user", ActorID: command.ActorID,
		ExpectedRevision: command.ExpectedRevision, Plan: command.Plan,
	})
}

func (s *Service) StartMission(ctx context.Context, command StartMissionCommand) (StartMissionResult, error) {
	errs := validateCommandIdentity(command.WorkspaceID, command.MissionID, command.CommandID, command.CorrelationID, command.ActorID, true)
	if command.ExpectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	if len(errs) > 0 {
		return StartMissionResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return StartMissionResult{}, err
	}
	return s.repository.StartMission(ctx, StartMissionParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID,
		CommandID: command.CommandID, CorrelationID: command.CorrelationID,
		ActorID: command.ActorID, ExpectedRevision: command.ExpectedRevision,
	})
}

func (s *Service) CancelMission(ctx context.Context, command CancelMissionCommand) (CancelMissionResult, error) {
	errs := validateCommandIdentity(command.WorkspaceID, command.MissionID, command.CommandID, command.CorrelationID, command.ActorID, true)
	if command.ExpectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	if len(errs) > 0 {
		return CancelMissionResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return CancelMissionResult{}, err
	}
	result, err := s.repository.CancelMission(ctx, CancelMissionParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID,
		CommandID: command.CommandID, CorrelationID: command.CorrelationID,
		ActorID: command.ActorID, ExpectedRevision: command.ExpectedRevision,
		Reason: strings.TrimSpace(command.Reason),
	})
	if err != nil {
		return CancelMissionResult{}, err
	}
	for _, run := range result.ActiveRuns {
		mapping, lookupErr := s.repository.GetRunExecutionMapping(ctx, command.WorkspaceID, run.ID)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			continue
		}
		if lookupErr != nil {
			return CancelMissionResult{}, fmt.Errorf("cancel mission: load run execution mapping: %w", lookupErr)
		}
		if s.execution == nil {
			return CancelMissionResult{}, fmt.Errorf("cancel mission: execution gateway is not configured")
		}
		if _, cancelErr := s.execution.Cancel(ctx, CancelExecutionRequest{
			AgentTaskID: mapping.AgentTaskID, Reason: strings.TrimSpace(command.Reason),
		}); cancelErr != nil {
			return CancelMissionResult{}, fmt.Errorf("cancel mission: cancel run execution: %w", cancelErr)
		}
	}
	return result, nil
}

func (s *Service) requireOwner(ctx context.Context, workspaceID, actorID pgtype.UUID) error {
	if s == nil || s.queries == nil || s.repository == nil {
		return fmt.Errorf("orchestration service is not configured")
	}
	member, err := s.queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID: actorID, WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOwnerRequired
		}
		return fmt.Errorf("authorize orchestration command: %w", err)
	}
	if member.Role != "owner" {
		return ErrOwnerRequired
	}
	return nil
}

func validateCommandIdentity(workspaceID, missionID, commandID, correlationID, actorID pgtype.UUID, requireMission bool) []ValidationError {
	var errs []ValidationError
	check := func(path string, value pgtype.UUID) {
		if !validUUID(value) {
			errs = append(errs, ValidationError{Path: path, Code: "invalid_uuid", Message: path + " must be a non-zero UUID"})
		}
	}
	check("workspace_id", workspaceID)
	if requireMission {
		check("mission_id", missionID)
	}
	check("command_id", commandID)
	check("actor_id", actorID)
	if correlationID.Valid && !validUUID(correlationID) {
		errs = append(errs, ValidationError{Path: "correlation_id", Code: "invalid_uuid", Message: "correlation_id must be a non-zero UUID"})
	}
	return errs
}

func validUUID(value pgtype.UUID) bool {
	return value.Valid && value.Bytes != [16]byte{}
}
