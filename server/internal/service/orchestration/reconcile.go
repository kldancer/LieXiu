package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	defaultRunReconcileBatchSize = 64
	defaultRunReconcileInterval  = 2 * time.Second
	maxRunFailureMessageBytes    = 2048
)

type ReconcileCursor struct {
	CreatedAt pgtype.Timestamptz
	RunID     pgtype.UUID
}

type ReconcilableRun struct {
	WorkspaceID pgtype.UUID
	RunID       pgtype.UUID
	CreatedAt   pgtype.Timestamptz
}

type ReconcileRunParams struct {
	WorkspaceID pgtype.UUID
	RunID       pgtype.UUID
	ObservedAt  time.Time
}

type ReconcileRunResult struct {
	Run                   db.OrchestrationRun
	TaskNode              db.TaskNode
	Activities            []db.OrchestrationActivity
	PlanProposalArtifact  *db.Artifact
	Artifact              *db.Artifact
	ReviewVerdict         *db.ReviewVerdict
	Changed               bool
	EnqueueExecution      bool
	EnqueueActorID        pgtype.UUID
	CancelExecution       bool
	CancelAgentTaskID     pgtype.UUID
	CancelExecutionReason string
}

type RunReconcileStore interface {
	ListReconcilableRuns(context.Context, ReconcileCursor, int) ([]ReconcilableRun, error)
	ReconcileRun(context.Context, ReconcileRunParams) (ReconcileRunResult, error)
}

type ExpiredMailboxMessage struct {
	WorkspaceID pgtype.UUID
	MissionID   pgtype.UUID
	MessageID   pgtype.UUID
	Revision    int64
}

type ExpireMailboxMessageParams struct {
	WorkspaceID pgtype.UUID
	MissionID   pgtype.UUID
	MessageID   pgtype.UUID
	CommandID   pgtype.UUID
	Revision    int64
	ObservedAt  time.Time
}

// MailboxExpiryStore is optional so the Run reconciler remains usable with
// repository-only test stores. The production Repository implements it and
// shares the same bounded startup/periodic recovery loop.
type MailboxExpiryStore interface {
	ListExpiredMailboxMessages(context.Context, time.Time, int) ([]ExpiredMailboxMessage, error)
	ExpireMailboxMessage(context.Context, ExpireMailboxMessageParams) error
}

type RunReconcilerOptions struct {
	BatchSize int
	Interval  time.Duration
	Logger    *slog.Logger
	Now       func() time.Time
	// AfterReconcile advances deterministic orchestration after a committed
	// Run/TaskNode change. It is optional so repository-only tests stay small.
	AfterReconcile func(context.Context, ReconcileRunResult) error
}

type RunReconciler struct {
	store     RunReconcileStore
	execution ExecutionGateway
	batchSize int
	interval  time.Duration
	logger    *slog.Logger
	now       func() time.Time
	after     func(context.Context, ReconcileRunResult) error
	cursor    ReconcileCursor
}

func NewRunReconciler(store RunReconcileStore, execution ExecutionGateway, options RunReconcilerOptions) *RunReconciler {
	batchSize := options.BatchSize
	if batchSize <= 0 {
		batchSize = defaultRunReconcileBatchSize
	}
	interval := options.Interval
	if interval <= 0 {
		interval = defaultRunReconcileInterval
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &RunReconciler{
		store: store, execution: execution, batchSize: batchSize,
		interval: interval, logger: logger, now: now, after: options.AfterReconcile,
	}
}

// Run performs recovery immediately at startup and then continues with a
// bounded periodic scan. A database cursor prevents old active runs from
// permanently starving newer rows.
func (r *RunReconciler) Run(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := r.ReconcileBatch(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Warn("orchestration run reconciliation failed", "error", err)
			}
			timer.Reset(r.interval)
		}
	}
}

func (r *RunReconciler) ReconcileBatch(ctx context.Context) (int, error) {
	if r == nil || r.store == nil {
		return 0, fmt.Errorf("run reconciler is not configured")
	}
	processed, expiryErr := r.expireMailboxBatch(ctx)
	runs, err := r.store.ListReconcilableRuns(ctx, r.cursor, r.batchSize)
	if err != nil {
		return processed, errors.Join(expiryErr, fmt.Errorf("list reconcilable runs: %w", err))
	}
	if len(runs) == 0 {
		r.cursor = ReconcileCursor{}
		return processed, expiryErr
	}

	var batchErrors []error
	for _, candidate := range runs {
		r.cursor = ReconcileCursor{CreatedAt: candidate.CreatedAt, RunID: candidate.RunID}
		result, reconcileErr := r.store.ReconcileRun(ctx, ReconcileRunParams{
			WorkspaceID: candidate.WorkspaceID,
			RunID:       candidate.RunID,
			ObservedAt:  r.now().UTC(),
		})
		if reconcileErr != nil {
			batchErrors = append(batchErrors, fmt.Errorf("reconcile run %s: %w", uuidText(candidate.RunID), reconcileErr))
			continue
		}
		if result.EnqueueExecution {
			if r.execution == nil {
				batchErrors = append(batchErrors, fmt.Errorf("enqueue run %s execution: execution gateway is not configured", uuidText(candidate.RunID)))
				continue
			}
			if _, enqueueErr := r.execution.Enqueue(ctx, EnqueueExecutionRequest{
				WorkspaceID: candidate.WorkspaceID, RunID: candidate.RunID, ActorID: result.EnqueueActorID,
			}); enqueueErr != nil {
				batchErrors = append(batchErrors, fmt.Errorf("enqueue run %s execution: %w", uuidText(candidate.RunID), enqueueErr))
			}
			continue
		}
		if !result.CancelExecution {
			if result.Changed && r.after != nil {
				if advanceErr := r.after(ctx, result); advanceErr != nil {
					batchErrors = append(batchErrors, fmt.Errorf("advance after run %s: %w", uuidText(candidate.RunID), advanceErr))
				}
			}
			continue
		}
		if r.execution == nil {
			batchErrors = append(batchErrors, fmt.Errorf("cancel run %s execution: execution gateway is not configured", uuidText(candidate.RunID)))
			continue
		}
		if _, cancelErr := r.execution.Cancel(ctx, CancelExecutionRequest{
			AgentTaskID: result.CancelAgentTaskID,
			Reason:      result.CancelExecutionReason,
		}); cancelErr != nil {
			batchErrors = append(batchErrors, fmt.Errorf("cancel run %s execution: %w", uuidText(candidate.RunID), cancelErr))
			continue
		}
		if r.after != nil {
			if advanceErr := r.after(ctx, result); advanceErr != nil {
				batchErrors = append(batchErrors, fmt.Errorf("advance after run %s: %w", uuidText(candidate.RunID), advanceErr))
			}
		}
	}
	if len(runs) < r.batchSize {
		r.cursor = ReconcileCursor{}
	}
	return processed + len(runs), errors.Join(append(batchErrors, expiryErr)...)
}

func (r *RunReconciler) expireMailboxBatch(ctx context.Context) (int, error) {
	store, ok := r.store.(MailboxExpiryStore)
	if !ok {
		return 0, nil
	}
	observedAt := r.now().UTC().Truncate(time.Microsecond)
	messages, err := store.ListExpiredMailboxMessages(ctx, observedAt, r.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list expired mailbox messages: %w", err)
	}
	var batchErrors []error
	for _, message := range messages {
		if err := store.ExpireMailboxMessage(ctx, ExpireMailboxMessageParams{
			WorkspaceID: message.WorkspaceID,
			MissionID:   message.MissionID,
			MessageID:   message.MessageID,
			CommandID:   derivedMailboxExpiryCommandID(message.MessageID, message.Revision),
			Revision:    message.Revision,
			ObservedAt:  observedAt,
		}); err != nil && !errors.Is(err, ErrMailboxStatusConflict) {
			batchErrors = append(batchErrors, fmt.Errorf("expire mailbox message %s: %w", uuidText(message.MessageID), err))
		}
	}
	return len(messages), errors.Join(batchErrors...)
}

func derivedMailboxExpiryCommandID(messageID pgtype.UUID, revision int64) pgtype.UUID {
	derived := uuid.NewSHA1(uuid.UUID(messageID.Bytes), []byte(fmt.Sprintf("mailbox-expiry:%d", revision)))
	return pgtype.UUID{Bytes: [16]byte(derived), Valid: true}
}

type runObservation struct {
	status          RunStatus
	failureKind     pgtype.Text
	failureMessage  pgtype.Text
	startedAt       pgtype.Timestamptz
	finishedAt      pgtype.Timestamptz
	taskStatus      TaskStatus
	taskStatusValid bool
	blockReason     pgtype.Text
	cancelExecution bool
}

func normalizeRunObservation(mission db.Mission, run db.OrchestrationRun, node db.TaskNode, task *db.AgentTaskQueue, observedAt time.Time) (runObservation, error) {
	if observedAt.IsZero() {
		return runObservation{}, fmt.Errorf("observed_at is required")
	}
	observation := runObservation{status: RunStatus(run.Status), startedAt: run.StartedAt, finishedAt: run.FinishedAt}
	planning := run.Purpose == "plan" && !run.TaskNodeID.Valid
	if isTerminalRunStatus(RunStatus(run.Status)) {
		if task != nil && isActiveAgentTaskStatus(task.Status) && (run.Status == string(RunStatusFailed) || run.Status == string(RunStatusCancelled)) {
			observation.cancelExecution = true
		}
		return observation, nil
	}
	missionStatus := MissionStatus(mission.Status)
	if missionStatus == MissionStatusCancelled || missionStatus == MissionStatusFailed || (!planning && TaskStatus(node.Status) == TaskStatusCancelled) {
		observation.status = RunStatusCancelled
		observation.finishedAt = timestamptz(observedAt)
		if !planning && !isTerminalTaskStatus(TaskStatus(node.Status)) {
			observation.taskStatus = TaskStatusCancelled
			observation.taskStatusValid = true
		}
		observation.cancelExecution = task != nil && isActiveAgentTaskStatus(task.Status)
		return observation, nil
	}
	if task == nil {
		if !observedAt.Before(run.DispatchDeadlineAt.Time) {
			observation.status = RunStatusFailed
			observation.failureKind = textValue("dispatch_timeout")
			observation.failureMessage = textValue("execution was not enqueued before the dispatch deadline")
			observation.finishedAt = timestamptz(observedAt)
		}
		return observation, nil
	}

	if task.Status != "failed" && task.Status != "cancelled" && timedOut(run, *task, observedAt) {
		observation.status = RunStatusFailed
		observation.failureKind = textValue("timeout")
		observation.failureMessage = textValue("execution exceeded the run timeout")
		observation.startedAt = firstTimestamp(run.StartedAt, task.StartedAt)
		observation.finishedAt = task.CompletedAt
		if !observation.finishedAt.Valid {
			observation.finishedAt = timestamptz(observedAt)
		}
		if !planning && TaskStatus(node.Status) == TaskStatusRunning {
			observation.taskStatus = TaskStatusAssigned
			observation.taskStatusValid = true
		}
		observation.cancelExecution = isActiveAgentTaskStatus(task.Status)
		return observation, nil
	}

	switch task.Status {
	case "queued", "deferred":
		if !observedAt.Before(run.DispatchDeadlineAt.Time) {
			observation.status = RunStatusFailed
			observation.failureKind = textValue("dispatch_timeout")
			observation.failureMessage = textValue("execution was not claimed before the dispatch deadline")
			observation.finishedAt = timestamptz(observedAt)
			observation.cancelExecution = true
		}
	case "dispatched", "waiting_local_directory":
		observation.status = laterActiveRunStatus(RunStatus(run.Status), RunStatusDispatched)
	case "running":
		observation.status = RunStatusRunning
		observation.startedAt = firstTimestamp(run.StartedAt, task.StartedAt)
		if !observation.startedAt.Valid {
			observation.startedAt = timestamptz(observedAt)
		}
		if !planning && TaskStatus(node.Status) == TaskStatusAssigned {
			observation.taskStatus = TaskStatusRunning
			observation.taskStatusValid = true
		}
	case "completed":
		observation.status = RunStatusSucceeded
		observation.startedAt = firstTimestamp(run.StartedAt, task.StartedAt)
		observation.finishedAt = firstTimestamp(task.CompletedAt, timestamptz(observedAt))
		if !planning && !isTerminalTaskStatus(TaskStatus(node.Status)) {
			observation.taskStatus = TaskStatusReview
			observation.taskStatusValid = true
		}
	case "failed":
		observation.status = RunStatusFailed
		observation.startedAt = firstTimestamp(run.StartedAt, task.StartedAt)
		observation.finishedAt = firstTimestamp(task.CompletedAt, timestamptz(observedAt))
		observation.failureKind = textValue(normalizeFailureKind(task.FailureReason.String))
		observation.failureMessage = textValue(normalizeFailureMessage(task.Error.String, observation.failureKind.String))
		if !planning && TaskStatus(node.Status) == TaskStatusRunning {
			observation.taskStatus = TaskStatusAssigned
			observation.taskStatusValid = true
		}
	case "cancelled":
		observation.status = RunStatusCancelled
		observation.startedAt = firstTimestamp(run.StartedAt, task.StartedAt)
		observation.finishedAt = firstTimestamp(task.CompletedAt, timestamptz(observedAt))
		if !planning && !isTerminalTaskStatus(TaskStatus(node.Status)) {
			observation.taskStatus = TaskStatusCancelled
			observation.taskStatusValid = true
		}
	default:
		return runObservation{}, fmt.Errorf("unsupported agent task status %q", task.Status)
	}
	return observation, nil
}

func timedOut(run db.OrchestrationRun, task db.AgentTaskQueue, observedAt time.Time) bool {
	startedAt := firstTimestamp(run.StartedAt, task.StartedAt)
	if !startedAt.Valid && (task.Status == "dispatched" || task.Status == "waiting_local_directory") {
		startedAt = task.DispatchedAt
	}
	if !startedAt.Valid || run.TimeoutSeconds <= 0 {
		return false
	}
	deadline := startedAt.Time.Add(time.Duration(run.TimeoutSeconds) * time.Second)
	factAt := observedAt
	if task.CompletedAt.Valid {
		factAt = task.CompletedAt.Time
	}
	return !factAt.Before(deadline)
}

func normalizeFailureKind(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case value == "runtime_offline", value == "runtime_recovery", strings.Contains(value, "runtime offline"):
		return "runtime_offline"
	case value == "queued_expired", strings.Contains(value, "dispatch") && strings.Contains(value, "timeout"):
		return "dispatch_timeout"
	case strings.Contains(value, "timeout"), strings.Contains(value, "timed_out"):
		return "timeout"
	case value == "agent_error.provider_network", value == "provider_network":
		return "provider_network"
	case value == "skill_bundle_unavailable":
		return "skill_bundle_unavailable"
	case strings.Contains(value, "protocol"):
		return "protocol_error"
	case strings.Contains(value, "worktree"), strings.Contains(value, "work_dir"), strings.Contains(value, "working directory"):
		return "worktree_error"
	default:
		return "agent_error"
	}
}

func normalizeFailureMessage(raw, fallback string) string {
	message := strings.TrimSpace(raw)
	if message == "" {
		message = fallback
	}
	if len(message) <= maxRunFailureMessageBytes {
		return message
	}
	message = message[:maxRunFailureMessageBytes]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message
}

func laterActiveRunStatus(current, observed RunStatus) RunStatus {
	rank := map[RunStatus]int{RunStatusQueued: 0, RunStatusDispatched: 1, RunStatusRunning: 2}
	if rank[current] > rank[observed] {
		return current
	}
	return observed
}

func isTerminalRunStatus(status RunStatus) bool {
	return status == RunStatusSucceeded || status == RunStatusFailed || status == RunStatusCancelled
}

func isTerminalTaskStatus(status TaskStatus) bool {
	return status == TaskStatusCompleted || status == TaskStatusFailed || status == TaskStatusCancelled
}

func isActiveAgentTaskStatus(status string) bool {
	switch status {
	case "queued", "deferred", "dispatched", "waiting_local_directory", "running":
		return true
	default:
		return false
	}
}

func firstTimestamp(values ...pgtype.Timestamptz) pgtype.Timestamptz {
	for _, value := range values {
		if value.Valid {
			return value
		}
	}
	return pgtype.Timestamptz{}
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func textValue(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}
