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

func (r *Repository) loadReconciledArtifact(ctx context.Context, workspaceID, missionID, runID pgtype.UUID) (db.Artifact, error) {
	artifacts, err := r.queries.ListArtifactsByMission(ctx, db.ListArtifactsByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return db.Artifact{}, err
	}
	for _, artifact := range artifacts {
		if artifact.RunID == runID {
			return artifact, nil
		}
	}
	return db.Artifact{}, pgx.ErrNoRows
}

func artifactKindAllowedByNode(node db.TaskNode, kind ArtifactKind) (bool, error) {
	var allowed []ArtifactKind
	if err := json.Unmarshal(node.ArtifactKinds, &allowed); err != nil {
		return false, fmt.Errorf("decode artifact kinds: %w", err)
	}
	for _, candidate := range allowed {
		if candidate == kind {
			return true, nil
		}
	}
	return false, nil
}

func (r *Repository) recordReconciledArtifactInTx(ctx context.Context, qtx *db.Queries, mission db.Mission, run db.OrchestrationRun, node db.TaskNode, task db.AgentTaskQueue, commandID, correlationID pgtype.UUID, receipt executionArtifactReceipt) (db.Artifact, []db.OrchestrationActivity, error) {
	if run.Purpose == "review" || run.TaskNodeID != node.IssueID || TaskStatus(node.Status) != TaskStatusReview {
		return db.Artifact{}, nil, fmt.Errorf("reconcile run: completed work is not in review state")
	}
	assignment, err := qtx.GetOrchestrationAssignmentInWorkspace(ctx, db.GetOrchestrationAssignmentInWorkspaceParams{AssignmentID: run.AssignmentID, WorkspaceID: mission.WorkspaceID})
	if err != nil {
		return db.Artifact{}, nil, err
	}
	if assignment.AgentID != task.AgentID {
		return db.Artifact{}, nil, fmt.Errorf("reconcile run: completed task does not own work assignment")
	}
	runs, err := qtx.ListOrchestrationRunsByMission(ctx, db.ListOrchestrationRunsByMissionParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID})
	if err != nil {
		return db.Artifact{}, nil, err
	}
	latest, ok := latestWorkRun(runs, node.IssueID)
	if !ok || latest.ID != run.ID {
		return db.Artifact{}, nil, fmt.Errorf("reconcile run: stale work run")
	}
	allowedKind, err := artifactKindAllowedByNode(node, receipt.Artifact.Kind)
	if err != nil {
		return db.Artifact{}, nil, fmt.Errorf("reconcile run: %w", err)
	}
	if !allowedKind {
		return db.Artifact{}, nil, fmt.Errorf("reconcile run: artifact kind %q is not allowed by the task", receipt.Artifact.Kind)
	}
	metadata, err := json.Marshal(receipt.Artifact.Metadata)
	if err != nil {
		return db.Artifact{}, nil, err
	}
	version, err := qtx.NextArtifactVersion(ctx, db.NextArtifactVersionParams{WorkspaceID: mission.WorkspaceID, TaskNodeID: node.IssueID, Kind: string(receipt.Artifact.Kind)})
	if err != nil {
		return db.Artifact{}, nil, err
	}
	artifact, err := qtx.CreateArtifactRecord(ctx, db.CreateArtifactRecordParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, TaskNodeID: node.IssueID, RunID: run.ID, Kind: string(receipt.Artifact.Kind), Version: version, Uri: strings.TrimSpace(receipt.Artifact.URI), ContentHash: textValue(strings.TrimSpace(receipt.Artifact.ContentHash)), Summary: strings.TrimSpace(receipt.Artifact.Summary), Metadata: metadata})
	if err != nil {
		return db.Artifact{}, nil, fmt.Errorf("reconcile run: create artifact: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"artifact_id": uuidText(artifact.ID), "run_id": uuidText(run.ID), "kind": artifact.Kind, "version": artifact.Version})
	if err != nil {
		return db.Artifact{}, nil, err
	}
	sequence, err := allocateActivitySequence(ctx, qtx, mission.WorkspaceID, mission.IssueID)
	if err != nil {
		return db.Artifact{}, nil, err
	}
	activity, err := qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, TaskNodeID: node.IssueID, RunID: run.ID, Type: activityArtifactCreated, ActorType: "agent", ActorID: task.AgentID, SubjectType: "artifact", SubjectID: artifact.ID, CausationID: commandID, CorrelationID: correlationID, PayloadVersion: 1, Payload: payload, DedupeKey: "command:" + uuidText(commandID), Sequence: sequence})
	if err != nil {
		return db.Artifact{}, nil, err
	}
	return artifact, []db.OrchestrationActivity{activity}, nil
}

type reconciledReviewResult struct {
	Verdict    db.ReviewVerdict
	TaskNode   db.TaskNode
	Activities []db.OrchestrationActivity
}

func (r *Repository) recordReconciledReviewInTx(ctx context.Context, qtx *db.Queries, mission db.Mission, run db.OrchestrationRun, node db.TaskNode, task db.AgentTaskQueue, commandID, correlationID, actorID, requestedArtifactID pgtype.UUID, receipt executionReviewReceipt) (reconciledReviewResult, error) {
	var result reconciledReviewResult
	if run.Purpose != "review" || run.Status != string(RunStatusSucceeded) || run.TaskNodeID != node.IssueID || TaskStatus(node.Status) != TaskStatusReview {
		return result, fmt.Errorf("reconcile run: review scope is invalid")
	}
	assignment, err := qtx.GetOrchestrationAssignmentInWorkspace(ctx, db.GetOrchestrationAssignmentInWorkspaceParams{AssignmentID: run.AssignmentID, WorkspaceID: mission.WorkspaceID})
	if err != nil {
		return result, err
	}
	if assignment.Role != DutyReviewer.String() || assignment.AgentID != actorID {
		return result, fmt.Errorf("reconcile run: completed task does not own review assignment")
	}
	var frozen struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal(run.Input, &frozen); err != nil || frozen.ArtifactID == "" {
		return result, fmt.Errorf("reconcile run: review run input is invalid")
	}
	frozenArtifactID, err := uuidFromText(frozen.ArtifactID)
	if err != nil {
		return result, fmt.Errorf("reconcile run: review artifact id is invalid")
	}
	artifactID := frozenArtifactID
	if requestedArtifactID.Valid {
		if requestedArtifactID != frozenArtifactID {
			return result, fmt.Errorf("reconcile run: artifact does not match the review run")
		}
		artifactID = requestedArtifactID
	}
	artifact, err := qtx.GetArtifactInWorkspace(ctx, db.GetArtifactInWorkspaceParams{ArtifactID: artifactID, WorkspaceID: mission.WorkspaceID})
	if err != nil {
		return result, err
	}
	if artifact.TaskNodeID != node.IssueID {
		return result, fmt.Errorf("reconcile run: review artifact does not match task node")
	}
	if _, err := qtx.GetReviewVerdictByRun(ctx, run.ID); err == nil {
		return result, ErrReviewAlreadyRecorded
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	evidence, err := json.Marshal(receipt.Evidence)
	if err != nil {
		return result, err
	}
	requested, err := json.Marshal(receipt.RequestedChanges)
	if err != nil {
		return result, err
	}
	verdict, err := qtx.CreateReviewVerdictRecord(ctx, db.CreateReviewVerdictRecordParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, TaskNodeID: node.IssueID, ReviewRunID: run.ID, ArtifactID: artifact.ID, Decision: string(receipt.Decision), Evidence: evidence, RequestedChanges: requested})
	if err != nil {
		return result, err
	}
	limits, err := decodePlanLimits(mission.Limits)
	if err != nil {
		return result, err
	}
	target, taskActivity, reviewActivity := TaskStatusCompleted, activityTaskCompleted, activityReviewApproved
	if receipt.Decision == ReviewDecisionChangesRequested {
		reviewActivity = activityReviewChangesRequested
		if int(node.ReworkCount) < limits.MaxReworkCycles {
			target, taskActivity = TaskStatusRework, activityTaskReworkRequested
		} else {
			target, taskActivity = TaskStatusBlocked, activityTaskBlocked
		}
	}
	if receipt.Decision == ReviewDecisionRejected {
		target, taskActivity, reviewActivity = TaskStatusFailed, activityTaskFailed, activityReviewRejected
	}
	var updated db.TaskNode
	if target == TaskStatusRework {
		updated, err = qtx.TransitionTaskNodeForRework(ctx, db.TransitionTaskNodeForReworkParams{TargetStatus: target.String(), TaskNodeID: node.IssueID, WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, ExpectedReworkCount: node.ReworkCount})
	} else {
		updated, err = qtx.TransitionTaskNodeState(ctx, db.TransitionTaskNodeStateParams{TargetStatus: target.String(), TaskNodeID: node.IssueID, WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, ExpectedStatus: node.Status})
	}
	if err != nil {
		return result, err
	}
	issueStatus := "done"
	if target == TaskStatusRework {
		issueStatus = "todo"
	}
	if target == TaskStatusFailed || target == TaskStatusBlocked {
		issueStatus = "blocked"
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: node.IssueID, WorkspaceID: mission.WorkspaceID, Status: issueStatus}); err != nil {
		return result, err
	}
	var gateActivity *db.OrchestrationActivity
	var gate *db.OrchestrationHumanGate
	if receipt.Decision == ReviewDecisionChangesRequested && target == TaskStatusBlocked {
		createdGate, createdActivity, gateErr := createPendingHumanGate(ctx, qtx, mission, updated, artifact, run.ID, HumanGateReworkLimitExceeded, "review rework limit is exhausted", map[string]any{"verdict_id": uuidText(verdict.ID), "rework_count": node.ReworkCount, "max_rework_cycles": limits.MaxReworkCycles}, commandID, correlationID)
		if gateErr != nil {
			return result, gateErr
		}
		gate, gateActivity = &createdGate, &createdActivity
	}
	assignments, err := qtx.ListOrchestrationAssignmentsByMission(ctx, db.ListOrchestrationAssignmentsByMissionParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID})
	if err != nil {
		return result, err
	}
	for _, current := range assignments {
		if current.TaskNodeID != node.IssueID || current.Status != string(AssignmentStatusActive) {
			continue
		}
		endStatus := AssignmentStatusFulfilled
		if current.Role != DutyReviewer.String() && target != TaskStatusCompleted {
			endStatus = AssignmentStatusSuperseded
		}
		if _, err := qtx.EndOrchestrationAssignment(ctx, db.EndOrchestrationAssignmentParams{TargetStatus: string(endStatus), AssignmentID: current.ID, WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID}); err != nil {
			return result, err
		}
	}
	payloadValue := map[string]any{"verdict_id": uuidText(verdict.ID), "artifact_id": uuidText(artifact.ID), "decision": verdict.Decision}
	if gate != nil {
		payloadValue["human_gate_id"] = uuidText(gate.ID)
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return result, err
	}
	activities := make([]db.OrchestrationActivity, 0, 3)
	if gateActivity != nil {
		activities = append(activities, *gateActivity)
	}
	for _, spec := range []struct {
		typ, subject string
		id           pgtype.UUID
		key          string
	}{{reviewActivity, "review", verdict.ID, "command:" + uuidText(commandID)}, {taskActivity, "task_node", node.IssueID, "command:" + uuidText(commandID) + ":task"}} {
		sequence, err := allocateActivitySequence(ctx, qtx, mission.WorkspaceID, mission.IssueID)
		if err != nil {
			return result, err
		}
		activity, err := qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, TaskNodeID: node.IssueID, RunID: run.ID, Type: spec.typ, ActorType: "agent", ActorID: actorID, SubjectType: spec.subject, SubjectID: spec.id, CausationID: commandID, CorrelationID: correlationID, PayloadVersion: 1, Payload: payload, DedupeKey: spec.key, Sequence: sequence})
		if err != nil {
			return result, err
		}
		activities = append(activities, activity)
	}
	result.Verdict, result.TaskNode, result.Activities = verdict, updated, activities
	return result, nil
}
