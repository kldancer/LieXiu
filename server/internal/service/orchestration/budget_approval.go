package orchestration

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

type ApproveMissionBudgetCommand struct {
	WorkspaceID       pgtype.UUID
	MissionID         pgtype.UUID
	CommandID         pgtype.UUID
	CorrelationID     pgtype.UUID
	ActorID           pgtype.UUID
	ExpectedRevision  int64
	GrantTokens       int64
	GrantCostUSDTicks int64
	Reason            string
}

func (s *Service) ApproveMissionBudget(ctx context.Context, command ApproveMissionBudgetCommand) (ApproveMissionBudgetResult, error) {
	errs := validateCommandIdentity(command.WorkspaceID, command.MissionID, command.CommandID, command.CorrelationID, command.ActorID, true)
	if command.ExpectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	if command.GrantTokens < 0 {
		errs = append(errs, ValidationError{Path: "grant_tokens", Code: "invalid_budget_grant", Message: "grant_tokens cannot be negative"})
	}
	if command.GrantCostUSDTicks < 0 {
		errs = append(errs, ValidationError{Path: "grant_cost_usd_ticks", Code: "invalid_budget_grant", Message: "grant_cost_usd_ticks cannot be negative"})
	}
	if command.GrantTokens == 0 && command.GrantCostUSDTicks == 0 {
		errs = append(errs, ValidationError{Path: "budget_grant", Code: "missing_budget_grant", Message: "at least one positive budget grant is required"})
	}
	if len(errs) > 0 {
		return ApproveMissionBudgetResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return ApproveMissionBudgetResult{}, err
	}
	result, err := s.repository.ApproveMissionBudget(ctx, ApproveMissionBudgetParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID,
		CommandID: command.CommandID, CorrelationID: command.CorrelationID, ActorID: command.ActorID,
		ExpectedRevision: command.ExpectedRevision, GrantTokens: command.GrantTokens,
		GrantCostUSDTicks: command.GrantCostUSDTicks, Reason: strings.TrimSpace(command.Reason),
	})
	if err != nil {
		return ApproveMissionBudgetResult{}, err
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
