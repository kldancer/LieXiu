package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const activityBudgetApproved = "budget.approved"

var ErrBudgetApprovalNotRequired = errors.New("mission does not require budget approval")

type ApproveMissionBudgetParams struct {
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

type ApproveMissionBudgetResult struct {
	Mission    db.Mission
	Activity   db.OrchestrationActivity
	Advance    AdvanceMissionResult
	Idempotent bool
}

func (r *Repository) ApproveMissionBudget(ctx context.Context, params ApproveMissionBudgetParams) (ApproveMissionBudgetResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: repository is not configured")
	}
	dedupeKey, err := commandDedupeKey(params.CommandID)
	if err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: %w", err)
	}
	correlationID := correlationOrCommand(params.CorrelationID, params.CommandID)
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)

	activity, err := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey})
	if err == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: rollback replay: %w", rollbackErr)
		}
		return r.loadApproveMissionBudgetResult(ctx, params.WorkspaceID, params.MissionID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: check command: %w", err)
	}

	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: params.MissionID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: lock mission: %w", err)
	}
	if mission.Status != string(MissionStatusBlocked) || mission.BudgetGateStatus != BudgetGateStatusApprovalRequired || mission.Revision != params.ExpectedRevision {
		replayed, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey})
		if replayErr == nil {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: rollback concurrent replay: %w", rollbackErr)
			}
			return r.loadApproveMissionBudgetResult(ctx, params.WorkspaceID, params.MissionID, replayed, true)
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: check concurrent replay: %w", replayErr)
		}
		if mission.Status != string(MissionStatusBlocked) || mission.BudgetGateStatus != BudgetGateStatusApprovalRequired {
			return ApproveMissionBudgetResult{}, ErrBudgetApprovalNotRequired
		}
		return ApproveMissionBudgetResult{}, ErrRevisionConflict
	}

	mission, err = qtx.ApproveMissionBudgetRecord(ctx, db.ApproveMissionBudgetRecordParams{
		GrantTokens: params.GrantTokens, GrantCostUsdTicks: params.GrantCostUSDTicks,
		ApprovedBy: params.ActorID, MissionID: params.MissionID, WorkspaceID: params.WorkspaceID,
		ExpectedRevision: params.ExpectedRevision,
	})
	if err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: update mission: %w", err)
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: params.MissionID, WorkspaceID: params.WorkspaceID, Status: "in_progress"}); err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: update root issue: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"grant_tokens": params.GrantTokens, "grant_cost_usd_ticks": params.GrantCostUSDTicks,
		"reason": params.Reason, "revision": mission.Revision,
	})
	if err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: encode activity: %w", err)
	}
	sequence, err := allocateActivitySequence(ctx, qtx, params.WorkspaceID, params.MissionID)
	if err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: allocate activity sequence: %w", err)
	}
	activity, err = qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
		Type: activityBudgetApproved, ActorType: "user", ActorID: params.ActorID,
		SubjectType: activitySubjectMission, SubjectID: params.MissionID,
		CausationID: params.CommandID, CorrelationID: correlationID,
		PayloadVersion: 1, Payload: payload, DedupeKey: dedupeKey, Sequence: sequence,
	})
	if err != nil {
		if isActivityDedupeViolation(err) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: rollback command race: %w", rollbackErr)
			}
			return r.loadApproveMissionBudgetByDedupeKey(ctx, params.WorkspaceID, params.MissionID, dedupeKey)
		}
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: create activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("approve mission budget: commit: %w", err)
	}
	return ApproveMissionBudgetResult{Mission: mission, Activity: activity}, nil
}

func (r *Repository) loadApproveMissionBudgetByDedupeKey(ctx context.Context, workspaceID, missionID pgtype.UUID, dedupeKey string) (ApproveMissionBudgetResult, error) {
	activity, err := r.queries.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: workspaceID, DedupeKey: dedupeKey})
	if err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("load budget approval command result: %w", err)
	}
	return r.loadApproveMissionBudgetResult(ctx, workspaceID, missionID, activity, true)
}

func (r *Repository) loadApproveMissionBudgetResult(ctx context.Context, workspaceID, missionID pgtype.UUID, activity db.OrchestrationActivity, idempotent bool) (ApproveMissionBudgetResult, error) {
	if activity.Type != activityBudgetApproved || activity.SubjectType != activitySubjectMission || activity.MissionID != missionID {
		return ApproveMissionBudgetResult{}, ErrCommandConflict
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return ApproveMissionBudgetResult{}, fmt.Errorf("load approved mission budget result: %w", err)
	}
	return ApproveMissionBudgetResult{Mission: mission, Activity: activity, Idempotent: idempotent}, nil
}
