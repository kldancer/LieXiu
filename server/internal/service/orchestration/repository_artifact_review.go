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

const (
	activityArtifactCreated        = "artifact.created"
	activityReviewApproved         = "review.approved"
	activityReviewChangesRequested = "review.changes_requested"
	activityReviewRejected         = "review.rejected"
	activityTaskReworkRequested    = "task.rework_requested"
	activityTaskCompleted          = "task.completed"
)

var ErrReviewAlreadyRecorded = errors.New("review verdict was already recorded")

func (r *Repository) RecordArtifact(ctx context.Context, command RecordArtifactCommand) (RecordArtifactResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: repository is not configured")
	}
	if err := validateArtifactCommand(command); err != nil {
		return RecordArtifactResult{}, err
	}
	dedupeKey, _ := commandDedupeKey(command.CommandID)
	correlationID := correlationOrCommand(command.CorrelationID, command.CommandID)
	if existing, err := r.queries.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: command.WorkspaceID, DedupeKey: dedupeKey}); err == nil {
		return r.loadArtifactReplay(ctx, command, existing)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RecordArtifactResult{}, err
	}
	preflightRun, err := r.queries.GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{RunID: command.RunID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: load run: %w", err)
	}
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return RecordArtifactResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: command.MissionID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordArtifactResult{}, err
	}
	if existing, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: command.WorkspaceID, DedupeKey: dedupeKey}); replayErr == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return RecordArtifactResult{}, rollbackErr
		}
		return r.loadArtifactReplay(ctx, command, existing)
	} else if !errors.Is(replayErr, pgx.ErrNoRows) {
		return RecordArtifactResult{}, replayErr
	}
	if mission.IssueID != preflightRun.MissionID || MissionStatus(mission.Status) != MissionStatusRunning {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: mission is not running")
	}
	run, err := qtx.LockOrchestrationRunForReconcile(ctx, db.LockOrchestrationRunForReconcileParams{RunID: command.RunID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordArtifactResult{}, err
	}
	node, err := qtx.LockTaskNodeForReconcile(ctx, db.LockTaskNodeForReconcileParams{TaskNodeID: command.TaskNodeID, WorkspaceID: command.WorkspaceID, MissionID: command.MissionID})
	if err != nil {
		return RecordArtifactResult{}, err
	}
	if run.TaskNodeID != node.IssueID || run.Status != string(RunStatusSucceeded) || run.Purpose == "review" || TaskStatus(node.Status) != TaskStatusReview {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: run is not the current successful work run")
	}
	assignment, err := qtx.GetOrchestrationAssignmentInWorkspace(ctx, db.GetOrchestrationAssignmentInWorkspaceParams{AssignmentID: run.AssignmentID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordArtifactResult{}, err
	}
	if assignment.AgentID != command.ActorID {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: actor does not own the work assignment")
	}
	runs, err := qtx.ListOrchestrationRunsByMission(ctx, db.ListOrchestrationRunsByMissionParams{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID})
	if err != nil {
		return RecordArtifactResult{}, err
	}
	latest, ok := latestWorkRun(runs, node.IssueID)
	if !ok || latest.ID != run.ID {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: stale work run")
	}
	var allowed []ArtifactKind
	if err := json.Unmarshal(node.ArtifactKinds, &allowed); err != nil {
		return RecordArtifactResult{}, err
	}
	allowedKind := false
	for _, kind := range allowed {
		if kind == command.Kind {
			allowedKind = true
		}
	}
	if !allowedKind {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: kind %q is not allowed by the task", command.Kind)
	}
	version, err := qtx.NextArtifactVersion(ctx, db.NextArtifactVersionParams{WorkspaceID: command.WorkspaceID, TaskNodeID: node.IssueID, Kind: string(command.Kind)})
	if err != nil {
		return RecordArtifactResult{}, err
	}
	metadata := command.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	artifact, err := qtx.CreateArtifactRecord(ctx, db.CreateArtifactRecordParams{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, TaskNodeID: node.IssueID, RunID: run.ID, Kind: string(command.Kind), Version: version, Uri: strings.TrimSpace(command.URI), ContentHash: textValue(strings.TrimSpace(command.ContentHash)), Summary: strings.TrimSpace(command.Summary), Metadata: metadata})
	if err != nil {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: create: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"artifact_id": uuidText(artifact.ID), "run_id": uuidText(run.ID), "kind": artifact.Kind, "version": artifact.Version})
	sequence, err := allocateActivitySequence(ctx, qtx, mission.WorkspaceID, mission.IssueID)
	if err != nil {
		return RecordArtifactResult{}, err
	}
	activity, err := qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, TaskNodeID: node.IssueID, RunID: run.ID, Type: activityArtifactCreated, ActorType: "agent", ActorID: command.ActorID, SubjectType: "artifact", SubjectID: artifact.ID, CausationID: command.CommandID, CorrelationID: correlationID, PayloadVersion: 1, Payload: payload, DedupeKey: dedupeKey, Sequence: sequence})
	if err != nil {
		return RecordArtifactResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RecordArtifactResult{}, err
	}
	return RecordArtifactResult{Artifact: artifact, Activity: activity}, nil
}

func (r *Repository) loadArtifactReplay(ctx context.Context, command RecordArtifactCommand, activity db.OrchestrationActivity) (RecordArtifactResult, error) {
	if activity.Type != activityArtifactCreated || activity.RunID != command.RunID || activity.TaskNodeID != command.TaskNodeID {
		return RecordArtifactResult{}, ErrCommandConflict
	}
	var payload struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal(activity.Payload, &payload); err != nil {
		return RecordArtifactResult{}, err
	}
	id, err := uuidFromText(payload.ArtifactID)
	if err != nil {
		return RecordArtifactResult{}, err
	}
	artifact, err := r.queries.GetArtifactInWorkspace(ctx, db.GetArtifactInWorkspaceParams{ArtifactID: id, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordArtifactResult{}, err
	}
	return RecordArtifactResult{Artifact: artifact, Activity: activity, Idempotent: true}, nil
}

func (r *Repository) RecordReviewVerdict(ctx context.Context, command RecordReviewVerdictCommand) (RecordReviewVerdictResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return RecordReviewVerdictResult{}, fmt.Errorf("record review verdict: repository is not configured")
	}
	if err := validateReviewCommand(command); err != nil {
		return RecordReviewVerdictResult{}, err
	}
	dedupeKey, _ := commandDedupeKey(command.CommandID)
	correlationID := correlationOrCommand(command.CorrelationID, command.CommandID)
	if existing, err := r.queries.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: command.WorkspaceID, DedupeKey: dedupeKey}); err == nil {
		return r.loadReviewReplay(ctx, command, existing)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RecordReviewVerdictResult{}, err
	}
	preflightRun, err := r.queries.GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{RunID: command.ReviewRunID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: command.MissionID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	if existing, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: command.WorkspaceID, DedupeKey: dedupeKey}); replayErr == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return RecordReviewVerdictResult{}, rollbackErr
		}
		return r.loadReviewReplay(ctx, command, existing)
	} else if !errors.Is(replayErr, pgx.ErrNoRows) {
		return RecordReviewVerdictResult{}, replayErr
	}
	if preflightRun.MissionID != mission.IssueID || (MissionStatus(mission.Status) != MissionStatusRunning && MissionStatus(mission.Status) != MissionStatusBlocked) {
		return RecordReviewVerdictResult{}, fmt.Errorf("record review verdict: mission is not active")
	}
	run, err := qtx.LockOrchestrationRunForReconcile(ctx, db.LockOrchestrationRunForReconcileParams{RunID: command.ReviewRunID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	node, err := qtx.LockTaskNodeForReconcile(ctx, db.LockTaskNodeForReconcileParams{TaskNodeID: command.TaskNodeID, WorkspaceID: command.WorkspaceID, MissionID: command.MissionID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	artifact, err := qtx.GetArtifactInWorkspace(ctx, db.GetArtifactInWorkspaceParams{ArtifactID: command.ArtifactID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	assignment, err := qtx.GetOrchestrationAssignmentInWorkspace(ctx, db.GetOrchestrationAssignmentInWorkspaceParams{AssignmentID: run.AssignmentID, WorkspaceID: command.WorkspaceID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	if run.Purpose != "review" || run.Status != string(RunStatusSucceeded) || run.TaskNodeID != node.IssueID || TaskStatus(node.Status) != TaskStatusReview || artifact.TaskNodeID != node.IssueID || assignment.Role != string(RoleReviewer) || assignment.AgentID != command.ActorID {
		return RecordReviewVerdictResult{}, fmt.Errorf("record review verdict: review scope or actor is invalid")
	}
	var frozen struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal(run.Input, &frozen); err != nil || frozen.ArtifactID != uuidText(artifact.ID) {
		return RecordReviewVerdictResult{}, fmt.Errorf("record review verdict: artifact does not match the review run")
	}
	if _, err := qtx.GetReviewVerdictByRun(ctx, run.ID); err == nil {
		return RecordReviewVerdictResult{}, ErrReviewAlreadyRecorded
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RecordReviewVerdictResult{}, err
	}
	evidence := command.Evidence
	if len(evidence) == 0 {
		evidence = json.RawMessage(`{}`)
	}
	requestedChanges := command.RequestedChanges
	if requestedChanges == nil {
		requestedChanges = []string{}
	}
	requested, _ := json.Marshal(requestedChanges)
	verdict, err := qtx.CreateReviewVerdictRecord(ctx, db.CreateReviewVerdictRecordParams{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, TaskNodeID: node.IssueID, ReviewRunID: run.ID, ArtifactID: artifact.ID, Decision: string(command.Decision), Evidence: evidence, RequestedChanges: requested})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	limits, err := decodePlanLimits(mission.Limits)
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	target := TaskStatusCompleted
	taskActivity := activityTaskCompleted
	reviewActivity := activityReviewApproved
	if command.Decision == ReviewDecisionChangesRequested {
		reviewActivity = activityReviewChangesRequested
		if int(node.ReworkCount) < limits.MaxReworkCycles {
			target, taskActivity = TaskStatusRework, activityTaskReworkRequested
		} else {
			target, taskActivity = TaskStatusFailed, activityTaskFailed
		}
	}
	if command.Decision == ReviewDecisionRejected {
		target, taskActivity, reviewActivity = TaskStatusFailed, activityTaskFailed, activityReviewRejected
	}
	var updated db.TaskNode
	if target == TaskStatusRework {
		updated, err = qtx.TransitionTaskNodeForRework(ctx, db.TransitionTaskNodeForReworkParams{TargetStatus: target.String(), TaskNodeID: node.IssueID, WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, ExpectedReworkCount: node.ReworkCount})
	} else {
		updated, err = qtx.TransitionTaskNodeState(ctx, db.TransitionTaskNodeStateParams{TargetStatus: target.String(), TaskNodeID: node.IssueID, WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, ExpectedStatus: node.Status})
	}
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	issueStatus := "done"
	if target == TaskStatusRework {
		issueStatus = "todo"
	}
	if target == TaskStatusFailed {
		issueStatus = "blocked"
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: node.IssueID, WorkspaceID: command.WorkspaceID, Status: issueStatus}); err != nil {
		return RecordReviewVerdictResult{}, err
	}
	assignments, err := qtx.ListOrchestrationAssignmentsByMission(ctx, db.ListOrchestrationAssignmentsByMissionParams{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	for _, current := range assignments {
		if current.TaskNodeID != node.IssueID || current.Status != string(AssignmentStatusActive) {
			continue
		}
		endStatus := AssignmentStatusFulfilled
		if current.Role != string(RoleReviewer) && target != TaskStatusCompleted {
			endStatus = AssignmentStatusSuperseded
		}
		if _, err := qtx.EndOrchestrationAssignment(ctx, db.EndOrchestrationAssignmentParams{TargetStatus: string(endStatus), AssignmentID: current.ID, WorkspaceID: command.WorkspaceID, MissionID: command.MissionID}); err != nil {
			return RecordReviewVerdictResult{}, err
		}
	}
	payload, _ := json.Marshal(map[string]any{"verdict_id": uuidText(verdict.ID), "artifact_id": uuidText(artifact.ID), "decision": verdict.Decision})
	activities := make([]db.OrchestrationActivity, 0, 2)
	for index, spec := range []struct {
		typ, subject string
		id           pgtype.UUID
		key          string
	}{{reviewActivity, "review", verdict.ID, dedupeKey}, {taskActivity, "task_node", node.IssueID, dedupeKey + ":task"}} {
		sequence, seqErr := allocateActivitySequence(ctx, qtx, mission.WorkspaceID, mission.IssueID)
		if seqErr != nil {
			return RecordReviewVerdictResult{}, seqErr
		}
		activity, createErr := qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, TaskNodeID: node.IssueID, RunID: run.ID, Type: spec.typ, ActorType: "agent", ActorID: command.ActorID, SubjectType: spec.subject, SubjectID: spec.id, CausationID: command.CommandID, CorrelationID: correlationID, PayloadVersion: 1, Payload: payload, DedupeKey: spec.key, Sequence: sequence})
		if createErr != nil {
			return RecordReviewVerdictResult{}, createErr
		}
		activities = append(activities, activity)
		_ = index
	}
	if err := tx.Commit(ctx); err != nil {
		return RecordReviewVerdictResult{}, err
	}
	return RecordReviewVerdictResult{Verdict: verdict, TaskNode: updated, Activities: activities}, nil
}

func (r *Repository) loadReviewReplay(ctx context.Context, command RecordReviewVerdictCommand, activity db.OrchestrationActivity) (RecordReviewVerdictResult, error) {
	if (activity.Type != activityReviewApproved && activity.Type != activityReviewChangesRequested && activity.Type != activityReviewRejected) || activity.RunID != command.ReviewRunID || activity.TaskNodeID != command.TaskNodeID {
		return RecordReviewVerdictResult{}, ErrCommandConflict
	}
	verdict, err := r.queries.GetReviewVerdictByRun(ctx, command.ReviewRunID)
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	nodes, err := r.queries.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	node := findNode(nodes, command.TaskNodeID)
	if node == nil {
		return RecordReviewVerdictResult{}, pgx.ErrNoRows
	}
	activities, err := r.queries.ListOrchestrationActivitiesByCausation(ctx, db.ListOrchestrationActivitiesByCausationParams{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, CausationID: command.CommandID})
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	return RecordReviewVerdictResult{Verdict: verdict, TaskNode: *node, Activities: activities, Idempotent: true}, nil
}

func uuidFromText(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}
