package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	activityPlanProposalEdited   = "plan_proposal.edited"
	activityPlanProposalRejected = "plan_proposal.rejected"
)

var ErrPlanProposalNotPending = errors.New("plan proposal is not the current pending version")

type EditPlanProposalCommand struct {
	WorkspaceID        pgtype.UUID
	MissionID          pgtype.UUID
	ProposalArtifactID pgtype.UUID
	CommandID          pgtype.UUID
	CorrelationID      pgtype.UUID
	ActorID            pgtype.UUID
	ExpectedRevision   int64
	Proposal           PlanProposal
}

type EditPlanProposalResult struct {
	Mission    db.Mission
	Artifact   db.Artifact
	Activity   db.OrchestrationActivity
	Idempotent bool
}

type RejectPlanProposalCommand struct {
	WorkspaceID        pgtype.UUID
	MissionID          pgtype.UUID
	ProposalArtifactID pgtype.UUID
	CommandID          pgtype.UUID
	CorrelationID      pgtype.UUID
	ActorID            pgtype.UUID
	ExpectedRevision   int64
	Reason             string
}

type RejectPlanProposalResult struct {
	Mission    db.Mission
	Artifact   db.Artifact
	Assignment db.OrchestrationAssignment
	Activity   db.OrchestrationActivity
	Idempotent bool
}

func (s *Service) EditPlanProposal(ctx context.Context, command EditPlanProposalCommand) (EditPlanProposalResult, error) {
	errs := validatePlanGateCommand(command.WorkspaceID, command.MissionID, command.ProposalArtifactID, command.CommandID, command.CorrelationID, command.ActorID, command.ExpectedRevision)
	if len(errs) == 0 {
		errs = append(errs, ValidatePlanProposal(command.Proposal, uuidText(command.MissionID), s.hardLimits)...)
	}
	if len(errs) > 0 {
		return EditPlanProposalResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return EditPlanProposalResult{}, err
	}
	return s.repository.EditPlanProposal(ctx, command)
}

func (s *Service) RejectPlanProposal(ctx context.Context, command RejectPlanProposalCommand) (RejectPlanProposalResult, error) {
	errs := validatePlanGateCommand(command.WorkspaceID, command.MissionID, command.ProposalArtifactID, command.CommandID, command.CorrelationID, command.ActorID, command.ExpectedRevision)
	if strings.TrimSpace(command.Reason) == "" {
		errs = append(errs, ValidationError{Path: "reason", Code: "missing_reason", Message: "rejection reason is required"})
	}
	if len(errs) > 0 {
		return RejectPlanProposalResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return RejectPlanProposalResult{}, err
	}
	command.Reason = strings.TrimSpace(command.Reason)
	return s.repository.RejectPlanProposal(ctx, command)
}

func (s *Service) ApprovePlanProposal(ctx context.Context, command SubmitPlanProposalCommand) (SubmitPlanResult, error) {
	return s.SubmitPlanProposal(ctx, command)
}

func validatePlanGateCommand(workspaceID, missionID, artifactID, commandID, correlationID, actorID pgtype.UUID, expectedRevision int64) []ValidationError {
	errs := validateCommandIdentity(workspaceID, missionID, commandID, correlationID, actorID, true)
	if !validUUID(artifactID) {
		errs = append(errs, ValidationError{Path: "proposal_artifact_id", Code: "invalid_uuid", Message: "proposal_artifact_id is required"})
	}
	if expectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	return errs
}

type planGateLocked struct {
	artifact   db.Artifact
	run        db.OrchestrationRun
	assignment db.OrchestrationAssignment
}

func lockPendingPlanProposal(ctx context.Context, qtx *db.Queries, workspaceID, missionID, artifactID pgtype.UUID) (planGateLocked, error) {
	artifact, err := qtx.GetArtifactInWorkspace(ctx, db.GetArtifactInWorkspaceParams{ArtifactID: artifactID, WorkspaceID: workspaceID})
	if err != nil {
		return planGateLocked{}, err
	}
	if artifact.MissionID != missionID || artifact.TaskNodeID.Valid || artifact.Kind != string(ArtifactKindPlanProposal) {
		return planGateLocked{}, ErrPlanProposalNotPending
	}
	artifacts, err := qtx.ListArtifactsByMission(ctx, db.ListArtifactsByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return planGateLocked{}, err
	}
	for _, candidate := range artifacts {
		if !candidate.TaskNodeID.Valid && candidate.Kind == string(ArtifactKindPlanProposal) && candidate.Version > artifact.Version {
			return planGateLocked{}, ErrPlanProposalNotPending
		}
	}
	run, err := qtx.GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{RunID: artifact.RunID, WorkspaceID: workspaceID})
	if err != nil {
		return planGateLocked{}, err
	}
	if run.MissionID != missionID || run.TaskNodeID.Valid || run.Purpose != "plan" || run.Status != string(RunStatusSucceeded) {
		return planGateLocked{}, ErrPlanProposalNotPending
	}
	assignment, err := qtx.GetOrchestrationAssignmentInWorkspace(ctx, db.GetOrchestrationAssignmentInWorkspaceParams{AssignmentID: run.AssignmentID, WorkspaceID: workspaceID})
	if err != nil {
		return planGateLocked{}, err
	}
	if assignment.MissionID != missionID || assignment.TaskNodeID.Valid || assignment.Role != string(DutyPlanner) || assignment.Status != string(AssignmentStatusActive) {
		return planGateLocked{}, ErrPlanProposalNotPending
	}
	return planGateLocked{artifact: artifact, run: run, assignment: assignment}, nil
}

func (r *Repository) EditPlanProposal(ctx context.Context, command EditPlanProposalCommand) (EditPlanProposalResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return EditPlanProposalResult{}, fmt.Errorf("edit plan proposal: repository is not configured")
	}
	dedupeKey, _ := commandDedupeKey(command.CommandID)
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	mission, activity, replayed, err := lockPlanGateMission(ctx, tx, qtx, command.WorkspaceID, command.MissionID, command.ExpectedRevision, dedupeKey)
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	if replayed {
		return r.loadEditPlanProposalResult(ctx, command, activity)
	}
	locked, err := lockPendingPlanProposal(ctx, qtx, command.WorkspaceID, command.MissionID, command.ProposalArtifactID)
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	base, baseErrs := DecodeAndValidatePlanProposal(locked.artifact.Metadata, uuidText(command.MissionID), command.Proposal.Limits)
	if len(baseErrs) > 0 {
		return EditPlanProposalResult{}, CommandValidationError{Errors: baseErrs}
	}
	if !reflect.DeepEqual(base.Input, command.Proposal.Input) || !reflect.DeepEqual(base.Limits, command.Proposal.Limits) {
		return EditPlanProposalResult{}, CommandValidationError{Errors: []ValidationError{{Path: "proposal", Code: "frozen_planning_input", Message: "mission_id, input, and limits cannot be edited"}}}
	}
	canonical, err := EncodePlanProposal(command.Proposal)
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	version, err := qtx.NextPlanProposalVersion(ctx, db.NextPlanProposalVersionParams{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID})
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	artifact, err := qtx.CreateArtifactRecord(ctx, db.CreateArtifactRecordParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, RunID: locked.run.ID,
		Kind: string(ArtifactKindPlanProposal), Version: version,
		Uri:         "owner-edit://" + uuidText(command.CommandID) + "/plan-proposal",
		ContentHash: textValue(planProposalContentHash(canonical)), Summary: command.Proposal.ProposalKey, Metadata: canonical,
	})
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	mission, err = qtx.BeginMissionPlanning(ctx, db.BeginMissionPlanningParams{IssueID: command.MissionID, WorkspaceID: command.WorkspaceID, ExpectedRevision: command.ExpectedRevision})
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	payload := mustActivityPayload(map[string]any{"base_artifact_id": uuidText(locked.artifact.ID), "artifact_id": uuidText(artifact.ID), "version": artifact.Version})
	activity, err = createPlanGateActivity(ctx, qtx, mission, artifact.ID, locked.run.ID, command.ActorID, command.CommandID, correlationOrCommand(command.CorrelationID, command.CommandID), dedupeKey, activityPlanProposalEdited, payload)
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EditPlanProposalResult{}, err
	}
	return EditPlanProposalResult{Mission: mission, Artifact: artifact, Activity: activity}, nil
}

func (r *Repository) RejectPlanProposal(ctx context.Context, command RejectPlanProposalCommand) (RejectPlanProposalResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return RejectPlanProposalResult{}, fmt.Errorf("reject plan proposal: repository is not configured")
	}
	dedupeKey, _ := commandDedupeKey(command.CommandID)
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	mission, activity, replayed, err := lockPlanGateMission(ctx, tx, qtx, command.WorkspaceID, command.MissionID, command.ExpectedRevision, dedupeKey)
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	if replayed {
		return r.loadRejectPlanProposalResult(ctx, command, activity)
	}
	locked, err := lockPendingPlanProposal(ctx, qtx, command.WorkspaceID, command.MissionID, command.ProposalArtifactID)
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	assignment, err := qtx.EndOrchestrationAssignment(ctx, db.EndOrchestrationAssignmentParams{
		TargetStatus: string(AssignmentStatusRevoked), AssignmentID: locked.assignment.ID,
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID,
	})
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	mission, err = qtx.BeginMissionPlanning(ctx, db.BeginMissionPlanningParams{IssueID: command.MissionID, WorkspaceID: command.WorkspaceID, ExpectedRevision: command.ExpectedRevision})
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	payload := mustActivityPayload(map[string]any{"artifact_id": uuidText(locked.artifact.ID), "reason": command.Reason})
	activity, err = createPlanGateActivity(ctx, qtx, mission, locked.artifact.ID, locked.run.ID, command.ActorID, command.CommandID, correlationOrCommand(command.CorrelationID, command.CommandID), dedupeKey, activityPlanProposalRejected, payload)
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RejectPlanProposalResult{}, err
	}
	return RejectPlanProposalResult{Mission: mission, Artifact: locked.artifact, Assignment: assignment, Activity: activity}, nil
}

func lockPlanGateMission(ctx context.Context, tx pgx.Tx, qtx *db.Queries, workspaceID, missionID pgtype.UUID, expectedRevision int64, dedupeKey string) (db.Mission, db.OrchestrationActivity, bool, error) {
	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return db.Mission{}, db.OrchestrationActivity{}, false, err
	}
	activity, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: workspaceID, DedupeKey: dedupeKey})
	if replayErr == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return db.Mission{}, db.OrchestrationActivity{}, false, rollbackErr
		}
		return mission, activity, true, nil
	}
	if !errors.Is(replayErr, pgx.ErrNoRows) {
		return db.Mission{}, db.OrchestrationActivity{}, false, replayErr
	}
	if mission.Status != string(MissionStatusDraft) {
		return db.Mission{}, db.OrchestrationActivity{}, false, ErrMissionNotDraft
	}
	if mission.Revision != expectedRevision {
		return db.Mission{}, db.OrchestrationActivity{}, false, ErrRevisionConflict
	}
	return mission, db.OrchestrationActivity{}, false, nil
}

func createPlanGateActivity(ctx context.Context, qtx *db.Queries, mission db.Mission, artifactID, runID, actorID, commandID, correlationID pgtype.UUID, dedupeKey, activityType string, payload []byte) (db.OrchestrationActivity, error) {
	sequence, err := allocateActivitySequence(ctx, qtx, mission.WorkspaceID, mission.IssueID)
	if err != nil {
		return db.OrchestrationActivity{}, err
	}
	return qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, RunID: runID,
		Type: activityType, ActorType: "user", ActorID: actorID, SubjectType: "artifact", SubjectID: artifactID,
		CausationID: commandID, CorrelationID: correlationID, PayloadVersion: 1, Payload: payload, DedupeKey: dedupeKey, Sequence: sequence,
	})
}

func mustActivityPayload(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}

func (r *Repository) loadEditPlanProposalResult(ctx context.Context, command EditPlanProposalCommand, activity db.OrchestrationActivity) (EditPlanProposalResult, error) {
	if activity.MissionID != command.MissionID || activity.Type != activityPlanProposalEdited {
		return EditPlanProposalResult{}, ErrCommandConflict
	}
	artifact, err := r.queries.GetArtifactInWorkspace(ctx, db.GetArtifactInWorkspaceParams{ArtifactID: activity.SubjectID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: command.MissionID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return EditPlanProposalResult{}, err
	}
	return EditPlanProposalResult{Mission: mission, Artifact: artifact, Activity: activity, Idempotent: true}, nil
}

func (r *Repository) loadRejectPlanProposalResult(ctx context.Context, command RejectPlanProposalCommand, activity db.OrchestrationActivity) (RejectPlanProposalResult, error) {
	if activity.MissionID != command.MissionID || activity.Type != activityPlanProposalRejected {
		return RejectPlanProposalResult{}, ErrCommandConflict
	}
	artifact, err := r.queries.GetArtifactInWorkspace(ctx, db.GetArtifactInWorkspaceParams{ArtifactID: activity.SubjectID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: command.MissionID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	run, err := r.queries.GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{RunID: artifact.RunID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	assignment, err := r.queries.GetOrchestrationAssignmentInWorkspace(ctx, db.GetOrchestrationAssignmentInWorkspaceParams{AssignmentID: run.AssignmentID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RejectPlanProposalResult{}, err
	}
	return RejectPlanProposalResult{Mission: mission, Artifact: artifact, Assignment: assignment, Activity: activity, Idempotent: true}, nil
}
