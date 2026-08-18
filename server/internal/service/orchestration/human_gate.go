package orchestration

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

type HumanGateKind string

const (
	HumanGateReviewerUnavailable HumanGateKind = "reviewer_unavailable"
	HumanGateReworkLimitExceeded HumanGateKind = "rework_limit_exceeded"
	HumanGateResolutionRetry                   = "retry"
)

var (
	ErrHumanGateNotPending         = errors.New("human gate is not pending")
	ErrHumanGateRevisionConflict   = errors.New("human gate revision conflict")
	ErrHumanGateResolutionRequired = errors.New("pending human gate requires an explicit owner resolution")
)

type ResolveHumanGateCommand struct {
	WorkspaceID          pgtype.UUID
	MissionID            pgtype.UUID
	GateID               pgtype.UUID
	CommandID            pgtype.UUID
	CorrelationID        pgtype.UUID
	ActorID              pgtype.UUID
	ExpectedRevision     int64
	ExpectedTaskRevision int64
	ExpectedGateRevision int64
	Resolution           string
	Reason               string
}

type ResolveHumanGateResult struct {
	Mission    db.Mission
	TaskNode   db.TaskNode
	Gate       db.OrchestrationHumanGate
	Activity   db.OrchestrationActivity
	Advance    AdvanceMissionResult
	Idempotent bool
}

func (s *Service) ResolveHumanGate(ctx context.Context, command ResolveHumanGateCommand) (ResolveHumanGateResult, error) {
	errs := validateCommandIdentity(command.WorkspaceID, command.MissionID, command.CommandID, command.CorrelationID, command.ActorID, true)
	if !validUUID(command.GateID) {
		errs = append(errs, ValidationError{Path: "gate_id", Code: "invalid_uuid", Message: "gate_id must be a non-zero UUID"})
	}
	if command.ExpectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	if command.ExpectedTaskRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_task_revision", Code: "invalid_revision", Message: "expected_task_revision must be at least 1"})
	}
	if command.ExpectedGateRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_gate_revision", Code: "invalid_revision", Message: "expected_gate_revision must be at least 1"})
	}
	if strings.TrimSpace(command.Resolution) != HumanGateResolutionRetry {
		errs = append(errs, ValidationError{Path: "resolution", Code: "unsupported_resolution", Message: "resolution must be retry"})
	}
	if len(errs) > 0 {
		return ResolveHumanGateResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return ResolveHumanGateResult{}, err
	}
	result, err := s.repository.ResolveHumanGate(ctx, resolveHumanGateParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, GateID: command.GateID,
		CommandID: command.CommandID, CorrelationID: command.CorrelationID, ActorID: command.ActorID,
		ExpectedRevision: command.ExpectedRevision, ExpectedTaskRevision: command.ExpectedTaskRevision,
		ExpectedGateRevision: command.ExpectedGateRevision, Resolution: HumanGateResolutionRetry,
		Reason: strings.TrimSpace(command.Reason),
	})
	if err != nil {
		return ResolveHumanGateResult{}, err
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
