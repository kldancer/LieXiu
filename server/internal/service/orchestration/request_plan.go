package orchestration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const activityPlanRequested = "mission.plan_requested"

var (
	ErrPlanningInProgress = errors.New("mission already has an active planning assignment")
	ErrNoPlanningAgent    = errors.New("no online agent is available for planning")
)

// RoutingUnavailableError preserves the deterministic, privacy-safe routing
// evidence for a command that could not select an eligible candidate. Planner
// callers can still match ErrNoPlanningAgent for backward compatibility.
type RoutingUnavailableError struct {
	Duty    Duty                   `json:"duty"`
	Routing RoutingSelectionResult `json:"routing"`
}

func (e *RoutingUnavailableError) Error() string {
	return fmt.Sprintf("no eligible routing candidate for %s", e.Duty)
}

func (e *RoutingUnavailableError) Unwrap() error {
	if e != nil && e.Duty == DutyPlanner {
		return ErrNoPlanningAgent
	}
	return nil
}

type RequestPlanParams struct {
	WorkspaceID       pgtype.UUID
	MissionID         pgtype.UUID
	CommandID         pgtype.UUID
	CorrelationID     pgtype.UUID
	ActorID           pgtype.UUID
	ExpectedRevision  int64
	Input             PlanProposalInput
	RolePolicyBinding RolePolicyBinding
	ObservedAt        time.Time
}

type RequestPlanResult struct {
	Mission             db.Mission
	Assignment          db.OrchestrationAssignment
	Run                 db.OrchestrationRun
	Activity            db.OrchestrationActivity
	Execution           EnqueueExecutionResult
	RolePolicySnapshots []RolePolicySnapshot
	Idempotent          bool
}

func (r *Repository) RequestPlan(ctx context.Context, params RequestPlanParams) (RequestPlanResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: repository is not configured")
	}
	if params.ObservedAt.IsZero() {
		return RequestPlanResult{}, fmt.Errorf("request plan: observed_at is required")
	}
	dedupeKey, err := commandDedupeKey(params.CommandID)
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: %w", err)
	}
	correlationID := correlationOrCommand(params.CorrelationID, params.CommandID)
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	activity, err := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey})
	if err == nil {
		if matchErr := ensureFrozenRolePolicyBindingsMatch(ctx, r.queries, params.WorkspaceID, params.MissionID, []RolePolicyBinding{params.RolePolicyBinding}); matchErr != nil {
			return RequestPlanResult{}, matchErr
		}
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return RequestPlanResult{}, rollbackErr
		}
		return r.loadRequestPlanResult(ctx, params.WorkspaceID, params.MissionID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RequestPlanResult{}, fmt.Errorf("request plan: check command: %w", err)
	}
	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: params.MissionID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: lock mission: %w", err)
	}
	// A concurrent copy of the same command can commit while this transaction
	// waits on the Mission lock. Recheck after the lock so it replays instead of
	// observing the advanced revision as a conflict.
	activity, err = qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey})
	if err == nil {
		if matchErr := ensureFrozenRolePolicyBindingsMatch(ctx, qtx, params.WorkspaceID, params.MissionID, []RolePolicyBinding{params.RolePolicyBinding}); matchErr != nil {
			return RequestPlanResult{}, matchErr
		}
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return RequestPlanResult{}, rollbackErr
		}
		return r.loadRequestPlanResult(ctx, params.WorkspaceID, params.MissionID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RequestPlanResult{}, fmt.Errorf("request plan: recheck command: %w", err)
	}
	if mission.Status != string(MissionStatusDraft) {
		return RequestPlanResult{}, ErrMissionNotDraft
	}
	if mission.Revision != params.ExpectedRevision {
		return RequestPlanResult{}, ErrRevisionConflict
	}
	if _, err := qtx.GetActivePlanningAssignment(ctx, db.GetActivePlanningAssignmentParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID}); err == nil {
		return RequestPlanResult{}, ErrPlanningInProgress
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RequestPlanResult{}, fmt.Errorf("request plan: check active assignment: %w", err)
	}
	snapshots, err := freezeRolePolicyBindings(ctx, qtx, params.WorkspaceID, params.MissionID, params.ActorID, []RolePolicyBinding{params.RolePolicyBinding})
	if err != nil {
		return RequestPlanResult{}, err
	}
	plannerPolicy := snapshots[0]
	routing, err := selectAndLockRoutingCandidate(ctx, qtx, params.WorkspaceID, mission.CreatedBy, plannerPolicy, "", "")
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: select routing candidate: %w", err)
	}
	if routing.Selected == nil {
		return RequestPlanResult{}, &RoutingUnavailableError{Duty: DutyPlanner, Routing: routing}
	}
	candidateAgentID, err := uuidFromText(routing.Selected.AgentID)
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: decode selected agent id: %w", err)
	}
	candidateRuntimeID, err := uuidFromText(routing.Selected.RuntimeID)
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: decode selected runtime id: %w", err)
	}
	limits, err := decodePlanLimits(mission.Limits)
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: decode limits: %w", err)
	}
	input, err := EncodePlanningRunSpec(PlanningRunSpec{SchemaVersion: PlanningRunSpecSchemaVersion, MissionID: uuidText(mission.IssueID), ProposalArtifactKind: ArtifactKindPlanProposal, ProposalSchemaVersion: PlanProposalSchemaVersion, Input: params.Input, Limits: limits})
	if err != nil {
		return RequestPlanResult{}, err
	}
	mailboxContext, mailboxRows, err := selectMailboxRunContext(
		ctx, qtx, params.WorkspaceID, params.MissionID, pgtype.UUID{}, candidateAgentID, params.ObservedAt.UTC(),
	)
	if err != nil {
		return RequestPlanResult{}, err
	}
	input, err = attachMailboxRunContext(input, mailboxContext)
	if err != nil {
		return RequestPlanResult{}, err
	}
	sequence, err := qtx.NextPlanningAssignmentSequence(ctx, db.NextPlanningAssignmentSequenceParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: next assignment sequence: %w", err)
	}
	assignment, err := qtx.CreateOrchestrationAssignment(ctx, db.CreateOrchestrationAssignmentParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, Role: string(DutyPlanner), AgentID: candidateAgentID, RuntimeID: candidateRuntimeID, Sequence: sequence, CreatedBy: params.ActorID})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: create assignment: %w", err)
	}
	run, err := qtx.CreateOrchestrationRun(ctx, db.CreateOrchestrationRunParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, AssignmentID: assignment.ID, Purpose: "plan", Attempt: 1, Input: input, DispatchDeadlineAt: timestamptz(params.ObservedAt.UTC().Add(defaultDispatchTimeout)), TimeoutSeconds: int32(plannerPolicy.Config.TimeoutSeconds)})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: create run: %w", err)
	}
	if _, err := consumeMailboxRunContext(ctx, qtx, mission, run, candidateAgentID, mailboxContext, mailboxRows, params.ObservedAt.UTC(), correlationID); err != nil {
		return RequestPlanResult{}, err
	}
	mission, err = qtx.BeginMissionPlanning(ctx, db.BeginMissionPlanningParams{IssueID: params.MissionID, WorkspaceID: params.WorkspaceID, ExpectedRevision: params.ExpectedRevision})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: advance revision: %w", err)
	}
	activity, err = createAutomaticActivity(ctx, qtx, mission, pgtype.UUID{}, run.ID, activityPlanRequested, "run", run.ID, params.CommandID, correlationID, dedupeKey, map[string]any{"assignment_id": uuidText(assignment.ID), "run_id": uuidText(run.ID), "role_policy_snapshot_hash": plannerPolicy.ContentHash, "routing": routing})
	if err != nil {
		if isActivityDedupeViolation(err) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return RequestPlanResult{}, fmt.Errorf("request plan: rollback command race: %w", rollbackErr)
			}
			replayed, replayErr := r.queries.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
				WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey,
			})
			if replayErr != nil {
				return RequestPlanResult{}, fmt.Errorf("request plan: load command race: %w", replayErr)
			}
			if matchErr := ensureFrozenRolePolicyBindingsMatch(ctx, r.queries, params.WorkspaceID, params.MissionID, []RolePolicyBinding{params.RolePolicyBinding}); matchErr != nil {
				return RequestPlanResult{}, matchErr
			}
			return r.loadRequestPlanResult(ctx, params.WorkspaceID, params.MissionID, replayed, true)
		}
		return RequestPlanResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RequestPlanResult{}, fmt.Errorf("request plan: commit: %w", err)
	}
	return RequestPlanResult{Mission: mission, Assignment: assignment, Run: run, Activity: activity, RolePolicySnapshots: snapshots}, nil
}

func (r *Repository) loadRequestPlanResult(ctx context.Context, workspaceID, missionID pgtype.UUID, activity db.OrchestrationActivity, idempotent bool) (RequestPlanResult, error) {
	if activity.Type != activityPlanRequested || activity.SubjectType != "run" || activity.MissionID != missionID {
		return RequestPlanResult{}, ErrCommandConflict
	}
	run, err := r.queries.GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{RunID: activity.SubjectID, WorkspaceID: workspaceID})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("load request plan run: %w", err)
	}
	assignment, err := r.queries.GetOrchestrationAssignmentInWorkspace(ctx, db.GetOrchestrationAssignmentInWorkspaceParams{AssignmentID: run.AssignmentID, WorkspaceID: workspaceID})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("load request plan assignment: %w", err)
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("load request plan mission: %w", err)
	}
	snapshotRows, err := r.queries.ListMissionRolePolicySnapshots(ctx, db.ListMissionRolePolicySnapshotsParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return RequestPlanResult{}, fmt.Errorf("load request plan role policies: %w", err)
	}
	snapshots, err := mapRolePolicySnapshots(snapshotRows)
	if err != nil {
		return RequestPlanResult{}, err
	}
	return RequestPlanResult{Mission: mission, Assignment: assignment, Run: run, Activity: activity, RolePolicySnapshots: snapshots, Idempotent: idempotent}, nil
}
