package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	activityRunStarted          = "run.started"
	activityRunSucceeded        = "run.succeeded"
	activityRunFailed           = "run.failed"
	activityRunCancelled        = "run.cancelled"
	activityTaskAssigned        = "task.assigned"
	activityTaskStarted         = "task.started"
	activityTaskReviewRequested = "task.review_requested"
	activityTaskBlocked         = "task.blocked"
)

func (r *Repository) ListReconcilableRuns(ctx context.Context, cursor ReconcileCursor, limit int) ([]ReconcilableRun, error) {
	if r == nil || r.queries == nil {
		return nil, fmt.Errorf("list reconcilable runs: repository is not configured")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list reconcilable runs: limit must be positive")
	}
	if !cursor.CreatedAt.Valid {
		cursor.CreatedAt = timestamptz(time.Unix(0, 0))
	}
	if !cursor.RunID.Valid {
		cursor.RunID = pgtype.UUID{Valid: true}
	}
	rows, err := r.queries.ListReconcilableOrchestrationRunsAfter(ctx, db.ListReconcilableOrchestrationRunsAfterParams{
		AfterCreatedAt: cursor.CreatedAt,
		AfterRunID:     cursor.RunID,
		BatchSize:      int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list reconcilable runs: %w", err)
	}
	result := make([]ReconcilableRun, 0, len(rows))
	for _, row := range rows {
		result = append(result, ReconcilableRun{WorkspaceID: row.WorkspaceID, RunID: row.ID, CreatedAt: row.CreatedAt})
	}
	return result, nil
}

func (r *Repository) ReconcileRun(ctx context.Context, params ReconcileRunParams) (ReconcileRunResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: repository is not configured")
	}
	if !validUUID(params.WorkspaceID) || !validUUID(params.RunID) || params.ObservedAt.IsZero() {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: workspace_id, run_id, and observed_at are required")
	}
	preflightRun, err := r.queries.GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{
		RunID: params.RunID, WorkspaceID: params.WorkspaceID,
	})
	if err != nil {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: load run: %w", err)
	}

	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{
		IssueID: preflightRun.MissionID, WorkspaceID: params.WorkspaceID,
	})
	if err != nil {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: lock mission: %w", err)
	}
	run, err := qtx.LockOrchestrationRunForReconcile(ctx, db.LockOrchestrationRunForReconcileParams{
		RunID: params.RunID, WorkspaceID: params.WorkspaceID,
	})
	if err != nil {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: lock run: %w", err)
	}
	node, err := qtx.LockTaskNodeForReconcile(ctx, db.LockTaskNodeForReconcileParams{
		TaskNodeID: run.TaskNodeID, WorkspaceID: params.WorkspaceID, MissionID: run.MissionID,
	})
	if err != nil {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: lock task node: %w", err)
	}
	task, err := qtx.LockAgentTaskByOrchestrationRun(ctx, db.LockAgentTaskByOrchestrationRunParams{
		RunID: params.RunID, WorkspaceID: params.WorkspaceID,
	})
	var taskPtr *db.AgentTaskQueue
	if err == nil {
		taskPtr = &task
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: lock execution task: %w", err)
	}

	observation, err := normalizeRunObservation(mission, run, node, taskPtr, params.ObservedAt.UTC())
	if err != nil {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: normalize observation: %w", err)
	}
	result := ReconcileRunResult{Run: run, TaskNode: node}
	if taskPtr != nil && observation.cancelExecution {
		result.CancelExecution = true
		result.CancelAgentTaskID = taskPtr.ID
		result.CancelExecutionReason = cancellationReason(run, observation)
	}

	runChanged := observation.status != RunStatus(run.Status)
	taskChanged := observation.taskStatusValid && observation.taskStatus != TaskStatus(node.Status)
	if !runChanged && !taskChanged {
		return result, nil
	}
	if runChanged {
		if err := validateRunTransition(RunStatus(run.Status), observation.status); err != nil {
			return ReconcileRunResult{}, fmt.Errorf("reconcile run: %w", err)
		}
		updated, updateErr := qtx.UpdateOrchestrationRunFromReconcile(ctx, db.UpdateOrchestrationRunFromReconcileParams{
			TargetStatus: observation.status.String(), FailureKind: observation.failureKind,
			FailureMessage: observation.failureMessage, StartedAt: observation.startedAt,
			FinishedAt: observation.finishedAt, RunID: run.ID, WorkspaceID: run.WorkspaceID,
			ExpectedStatus: run.Status,
		})
		if updateErr != nil {
			return ReconcileRunResult{}, fmt.Errorf("reconcile run: update run: %w", updateErr)
		}
		run = updated
		result.Run = updated
	}
	if taskChanged {
		if err := validateTaskTransition(TaskStatus(node.Status), observation.taskStatus); err != nil {
			return ReconcileRunResult{}, fmt.Errorf("reconcile run: %w", err)
		}
		updated, updateErr := qtx.UpdateTaskNodeFromReconcile(ctx, db.UpdateTaskNodeFromReconcileParams{
			TargetStatus: observation.taskStatus.String(), BlockReason: observation.blockReason,
			TaskNodeID: node.IssueID, WorkspaceID: node.WorkspaceID, MissionID: node.MissionID,
			ExpectedStatus: node.Status,
		})
		if updateErr != nil {
			return ReconcileRunResult{}, fmt.Errorf("reconcile run: update task node: %w", updateErr)
		}
		node = updated
		result.TaskNode = updated
		if issueStatus := compatibilityIssueStatus(observation.taskStatus); issueStatus != "" {
			if _, updateErr := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
				ID: node.IssueID, Status: issueStatus, WorkspaceID: node.WorkspaceID,
			}); updateErr != nil {
				return ReconcileRunResult{}, fmt.Errorf("reconcile run: update task issue compatibility status: %w", updateErr)
			}
		}
		if observation.taskStatus == TaskStatusCancelled {
			if _, revokeErr := qtx.RevokeAssignmentFromReconcile(ctx, db.RevokeAssignmentFromReconcileParams{
				AssignmentID: run.AssignmentID, WorkspaceID: run.WorkspaceID,
				MissionID: run.MissionID, TaskNodeID: run.TaskNodeID,
			}); revokeErr != nil && !errors.Is(revokeErr, pgx.ErrNoRows) {
				return ReconcileRunResult{}, fmt.Errorf("reconcile run: revoke assignment: %w", revokeErr)
			}
		}
	}

	activities, err := r.createReconcileActivities(ctx, qtx, mission, run, node, taskPtr, runChanged, taskChanged, observation)
	if err != nil {
		return ReconcileRunResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReconcileRunResult{}, fmt.Errorf("reconcile run: commit: %w", err)
	}
	result.Changed = true
	result.Activities = activities
	return result, nil
}

func (r *Repository) createReconcileActivities(
	ctx context.Context,
	qtx *db.Queries,
	mission db.Mission,
	run db.OrchestrationRun,
	node db.TaskNode,
	task *db.AgentTaskQueue,
	runChanged bool,
	taskChanged bool,
	observation runObservation,
) ([]db.OrchestrationActivity, error) {
	type pendingActivity struct {
		typeName    string
		subjectType string
		subjectID   pgtype.UUID
		dedupePart  string
	}
	var pending []pendingActivity
	if runChanged {
		if activityType := runActivityType(observation.status); activityType != "" {
			pending = append(pending, pendingActivity{activityType, "run", run.ID, "run:" + observation.status.String()})
		}
	}
	if taskChanged {
		if activityType := taskActivityType(observation.taskStatus); activityType != "" {
			pending = append(pending, pendingActivity{activityType, "task_node", node.IssueID, "task:" + observation.taskStatus.String()})
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}

	causationID := run.ID
	actorType := "orchestrator"
	var actorID pgtype.UUID
	if task != nil {
		causationID = task.ID
		actorType = "runtime"
		actorID = task.RuntimeID
	}
	payload, err := json.Marshal(struct {
		RunStatus   RunStatus  `json:"run_status"`
		TaskStatus  TaskStatus `json:"task_status,omitempty"`
		FailureKind string     `json:"failure_kind,omitempty"`
		AgentTaskID string     `json:"agent_task_id,omitempty"`
	}{
		RunStatus: observation.status, TaskStatus: observation.taskStatus,
		FailureKind: observation.failureKind.String,
		AgentTaskID: func() string {
			if task == nil {
				return ""
			}
			return uuidText(task.ID)
		}(),
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile run: encode activity payload: %w", err)
	}
	activities := make([]db.OrchestrationActivity, 0, len(pending))
	for _, item := range pending {
		sequence, sequenceErr := allocateActivitySequence(ctx, qtx, mission.WorkspaceID, mission.IssueID)
		if sequenceErr != nil {
			return nil, fmt.Errorf("reconcile run: allocate activity sequence: %w", sequenceErr)
		}
		activity, createErr := qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
			WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID,
			TaskNodeID: node.IssueID, RunID: run.ID, Type: item.typeName,
			ActorType: actorType, ActorID: actorID, SubjectType: item.subjectType,
			SubjectID: item.subjectID, CausationID: causationID, CorrelationID: run.ID,
			PayloadVersion: 1, Payload: payload,
			DedupeKey: "reconcile:" + uuidText(run.ID) + ":" + item.dedupePart,
			Sequence:  sequence,
		})
		if createErr != nil {
			return nil, fmt.Errorf("reconcile run: create %s activity: %w", item.typeName, createErr)
		}
		activities = append(activities, activity)
	}
	return activities, nil
}

func runActivityType(status RunStatus) string {
	switch status {
	case RunStatusRunning:
		return activityRunStarted
	case RunStatusSucceeded:
		return activityRunSucceeded
	case RunStatusFailed:
		return activityRunFailed
	case RunStatusCancelled:
		return activityRunCancelled
	default:
		return ""
	}
}

func taskActivityType(status TaskStatus) string {
	switch status {
	case TaskStatusAssigned:
		return activityTaskAssigned
	case TaskStatusRunning:
		return activityTaskStarted
	case TaskStatusReview:
		return activityTaskReviewRequested
	case TaskStatusBlocked:
		return activityTaskBlocked
	case TaskStatusCancelled:
		return activityTaskCancelled
	default:
		return ""
	}
}

func compatibilityIssueStatus(status TaskStatus) string {
	switch status {
	case TaskStatusAssigned:
		return "todo"
	case TaskStatusRunning:
		return "in_progress"
	case TaskStatusReview:
		return "in_review"
	case TaskStatusBlocked:
		return "todo"
	case TaskStatusCancelled:
		return "cancelled"
	default:
		return ""
	}
}

func cancellationReason(run db.OrchestrationRun, observation runObservation) string {
	reason := "orchestration run " + observation.status.String()
	if observation.failureKind.Valid {
		reason += ": " + observation.failureKind.String
	} else if run.FailureKind.Valid {
		reason += ": " + run.FailureKind.String
	}
	return reason
}

func (s RunStatus) String() string { return string(s) }

func (s TaskStatus) String() string { return string(s) }

var _ RunReconcileStore = (*Repository)(nil)
