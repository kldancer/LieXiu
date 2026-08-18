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

const (
	activityHumanGateRequired = "human_gate.required"
	activityHumanGateResolved = "human_gate.resolved"
)

type resolveHumanGateParams struct {
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

func createPendingHumanGate(
	ctx context.Context,
	q *db.Queries,
	mission db.Mission,
	node db.TaskNode,
	artifact db.Artifact,
	sourceRunID pgtype.UUID,
	kind HumanGateKind,
	reason string,
	contextValue any,
	causationID pgtype.UUID,
	correlationID pgtype.UUID,
) (db.OrchestrationHumanGate, db.OrchestrationActivity, error) {
	contextJSON, err := json.Marshal(contextValue)
	if err != nil {
		return db.OrchestrationHumanGate{}, db.OrchestrationActivity{}, fmt.Errorf("create human gate: encode context: %w", err)
	}
	gate, err := q.CreatePendingHumanGate(ctx, db.CreatePendingHumanGateParams{
		WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, TaskNodeID: node.IssueID,
		ArtifactID: artifact.ID, SourceRunID: sourceRunID, Kind: string(kind), Reason: reason, Context: contextJSON,
	})
	if err != nil {
		return db.OrchestrationHumanGate{}, db.OrchestrationActivity{}, fmt.Errorf("create human gate: persist: %w", err)
	}
	activity, err := createAutomaticActivity(
		ctx, q, mission, node.IssueID, sourceRunID, activityHumanGateRequired,
		"task_node", node.IssueID, causationID, correlationID,
		"human-gate:"+uuidText(gate.ID)+":required",
		map[string]any{"gate_id": uuidText(gate.ID), "kind": gate.Kind, "reason": gate.Reason, "artifact_id": uuidText(artifact.ID)},
	)
	if err != nil {
		return db.OrchestrationHumanGate{}, db.OrchestrationActivity{}, err
	}
	return gate, activity, nil
}

func (r *Repository) ResolveHumanGate(ctx context.Context, params resolveHumanGateParams) (ResolveHumanGateResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: repository is not configured")
	}
	dedupeKey, err := commandDedupeKey(params.CommandID)
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: %w", err)
	}
	correlationID := correlationOrCommand(params.CorrelationID, params.CommandID)
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)

	activity, err := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey})
	if err == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: rollback replay: %w", rollbackErr)
		}
		return r.loadResolveHumanGateResult(ctx, params.WorkspaceID, params.MissionID, params.GateID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: check command: %w", err)
	}

	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: params.MissionID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: lock mission: %w", err)
	}
	if replayed, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey}); replayErr == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: rollback concurrent replay: %w", rollbackErr)
		}
		return r.loadResolveHumanGateResult(ctx, params.WorkspaceID, params.MissionID, params.GateID, replayed, true)
	} else if !errors.Is(replayErr, pgx.ErrNoRows) {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: recheck command: %w", replayErr)
	}
	if MissionStatus(mission.Status) != MissionStatusRunning && MissionStatus(mission.Status) != MissionStatusBlocked {
		return ResolveHumanGateResult{}, ErrMissionNotRetryable
	}
	if mission.Revision != params.ExpectedRevision {
		return ResolveHumanGateResult{}, ErrRevisionConflict
	}
	gate, err := qtx.LockPendingHumanGate(ctx, db.LockPendingHumanGateParams{GateID: params.GateID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolveHumanGateResult{}, ErrHumanGateNotPending
	}
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: lock gate: %w", err)
	}
	if gate.Revision != params.ExpectedGateRevision {
		return ResolveHumanGateResult{}, ErrHumanGateRevisionConflict
	}
	node, err := qtx.LockTaskNodeForReconcile(ctx, db.LockTaskNodeForReconcileParams{TaskNodeID: gate.TaskNodeID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: lock task node: %w", err)
	}
	if TaskStatus(node.Status) != TaskStatusBlocked {
		return ResolveHumanGateResult{}, ErrTaskNodeNotRetryable
	}
	if node.Revision != params.ExpectedTaskRevision {
		return ResolveHumanGateResult{}, ErrTaskRevisionConflict
	}

	gate, err = qtx.ResolvePendingHumanGate(ctx, db.ResolvePendingHumanGateParams{
		ResolvedBy: params.ActorID, Resolution: textValue(params.Resolution), ResolutionReason: textValue(params.Reason),
		GateID: params.GateID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedRevision: params.ExpectedGateRevision,
	})
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: update gate: %w", err)
	}
	mission, err = qtx.ResumeMissionForTaskRetry(ctx, db.ResumeMissionForTaskRetryParams{MissionID: params.MissionID, WorkspaceID: params.WorkspaceID, ExpectedRevision: params.ExpectedRevision})
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: resume mission: %w", err)
	}
	issueStatus := "todo"
	switch HumanGateKind(gate.Kind) {
	case HumanGateReviewerUnavailable:
		node, err = qtx.RestoreTaskNodeForReviewerGate(ctx, db.RestoreTaskNodeForReviewerGateParams{TaskNodeID: node.IssueID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedRevision: params.ExpectedTaskRevision})
		issueStatus = "in_review"
	case HumanGateReworkLimitExceeded:
		node, err = qtx.RestoreTaskNodeForReworkGate(ctx, db.RestoreTaskNodeForReworkGateParams{TaskNodeID: node.IssueID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedRevision: params.ExpectedTaskRevision})
	default:
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: unsupported gate kind %q", gate.Kind)
	}
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: restore task node: %w", err)
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: params.MissionID, WorkspaceID: params.WorkspaceID, Status: "in_progress"}); err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: update mission issue: %w", err)
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: node.IssueID, WorkspaceID: params.WorkspaceID, Status: issueStatus}); err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: update task issue: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"gate_id": uuidText(gate.ID), "resolution": params.Resolution, "reason": params.Reason, "gate_revision": gate.Revision})
	sequence, err := allocateActivitySequence(ctx, qtx, params.WorkspaceID, params.MissionID)
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: allocate activity sequence: %w", err)
	}
	activity, err = qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: node.IssueID,
		Type: activityHumanGateResolved, ActorType: "user", ActorID: params.ActorID,
		SubjectType: "task_node", SubjectID: node.IssueID, CausationID: params.CommandID, CorrelationID: correlationID,
		PayloadVersion: 1, Payload: payload, DedupeKey: dedupeKey, Sequence: sequence,
	})
	if err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: create activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ResolveHumanGateResult{}, fmt.Errorf("resolve human gate: commit: %w", err)
	}
	return ResolveHumanGateResult{Mission: mission, TaskNode: node, Gate: gate, Activity: activity}, nil
}

func (r *Repository) loadResolveHumanGateResult(ctx context.Context, workspaceID, missionID, gateID pgtype.UUID, activity db.OrchestrationActivity, idempotent bool) (ResolveHumanGateResult, error) {
	if activity.Type != activityHumanGateResolved || activity.MissionID != missionID {
		return ResolveHumanGateResult{}, ErrCommandConflict
	}
	var payload struct {
		GateID string `json:"gate_id"`
	}
	if err := json.Unmarshal(activity.Payload, &payload); err != nil || payload.GateID != uuidText(gateID) {
		return ResolveHumanGateResult{}, ErrCommandConflict
	}
	gate, err := r.queries.GetHumanGateInWorkspace(ctx, db.GetHumanGateInWorkspaceParams{GateID: gateID, WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return ResolveHumanGateResult{}, err
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return ResolveHumanGateResult{}, err
	}
	node, err := r.queries.GetTaskNodeInMission(ctx, db.GetTaskNodeInMissionParams{TaskNodeID: gate.TaskNodeID, WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return ResolveHumanGateResult{}, err
	}
	return ResolveHumanGateResult{Mission: mission, TaskNode: node, Gate: gate, Activity: activity, Idempotent: idempotent}, nil
}
