package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	activityMissionStarted   = "mission.started"
	activityMissionCancelled = "mission.cancelled"
	activityTaskReady        = "task.ready"
	activityTaskCancelled    = "task.cancelled"
)

var (
	ErrMissionNotStartable    = errors.New("mission is not ready to start")
	ErrMissionNotCancellable  = errors.New("mission cannot be cancelled")
	ErrMissionHasNoTasks      = errors.New("mission has no task nodes")
	ErrMissionHasNoReadyTasks = errors.New("mission has no ready task nodes")
)

type StartMissionParams struct {
	WorkspaceID        pgtype.UUID
	MissionID          pgtype.UUID
	CommandID          pgtype.UUID
	CorrelationID      pgtype.UUID
	ActorID            pgtype.UUID
	ExpectedRevision   int64
	RolePolicyBindings []RolePolicyBinding
}

type StartMissionResult struct {
	Mission             db.Mission
	TaskNodes           []db.TaskNode
	Activities          []db.OrchestrationActivity
	RolePolicySnapshots []RolePolicySnapshot
	Idempotent          bool
}

type CancelMissionParams struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ActorID          pgtype.UUID
	ExpectedRevision int64
	Reason           string
}

type CancelMissionResult struct {
	Mission    db.Mission
	TaskNodes  []db.TaskNode
	ActiveRuns []db.OrchestrationRun
	Activities []db.OrchestrationActivity
	Idempotent bool
}

func (r *Repository) StartMission(ctx context.Context, params StartMissionParams) (StartMissionResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return StartMissionResult{}, fmt.Errorf("start mission: repository is not configured")
	}
	dedupeKey, err := commandDedupeKey(params.CommandID)
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: %w", err)
	}
	correlationID := correlationOrCommand(params.CorrelationID, params.CommandID)

	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)

	activity, err := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey,
	})
	if err == nil {
		if matchErr := ensureFrozenRolePolicyBindingsMatch(ctx, r.queries, params.WorkspaceID, params.MissionID, params.RolePolicyBindings); matchErr != nil {
			return StartMissionResult{}, matchErr
		}
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return StartMissionResult{}, fmt.Errorf("start mission: rollback idempotent transaction: %w", rollbackErr)
		}
		return r.loadStartMissionResult(ctx, params.WorkspaceID, params.MissionID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StartMissionResult{}, fmt.Errorf("start mission: check command: %w", err)
	}

	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{
		IssueID: params.MissionID, WorkspaceID: params.WorkspaceID,
	})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: lock mission: %w", err)
	}
	if mission.Status != string(MissionStatusReady) || mission.Revision != params.ExpectedRevision {
		replayed, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
			WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey,
		})
		if replayErr == nil {
			if matchErr := ensureFrozenRolePolicyBindingsMatch(ctx, qtx, params.WorkspaceID, params.MissionID, params.RolePolicyBindings); matchErr != nil {
				return StartMissionResult{}, matchErr
			}
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return StartMissionResult{}, fmt.Errorf("start mission: rollback concurrent replay: %w", rollbackErr)
			}
			return r.loadStartMissionResult(ctx, params.WorkspaceID, params.MissionID, replayed, true)
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return StartMissionResult{}, fmt.Errorf("start mission: check concurrent replay: %w", replayErr)
		}
		if mission.Status != string(MissionStatusReady) {
			return StartMissionResult{}, ErrMissionNotStartable
		}
		return StartMissionResult{}, ErrRevisionConflict
	}

	taskNodes, err := qtx.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
	})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: list task nodes: %w", err)
	}
	if len(taskNodes) == 0 {
		return StartMissionResult{}, ErrMissionHasNoTasks
	}
	dependencies, err := qtx.ListOrchestrationIssueDependencies(ctx, db.ListOrchestrationIssueDependenciesParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
	})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: list dependencies: %w", err)
	}
	limits, err := decodePlanLimits(mission.Limits)
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: %w", err)
	}
	readyKeys, err := initialReadyNodeKeys(taskNodes, dependencies, limits.MaxParallelRuns)
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: compute ready nodes: %w", err)
	}
	if len(readyKeys) == 0 {
		return StartMissionResult{}, ErrMissionHasNoReadyTasks
	}
	snapshots, err := freezeRolePolicyBindings(ctx, qtx, params.WorkspaceID, params.MissionID, params.ActorID, params.RolePolicyBindings)
	if err != nil {
		return StartMissionResult{}, err
	}

	mission, err = qtx.StartMissionRecord(ctx, db.StartMissionRecordParams{
		IssueID: params.MissionID, WorkspaceID: params.WorkspaceID,
		ExpectedRevision: params.ExpectedRevision,
	})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: update mission: %w", err)
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: params.MissionID, Status: "in_progress", WorkspaceID: params.WorkspaceID,
	}); err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: update root issue compatibility status: %w", err)
	}

	sequence, err := allocateActivitySequence(ctx, qtx, params.WorkspaceID, params.MissionID)
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: %w", err)
	}
	snapshotHashes := make(map[string]string, len(snapshots))
	for _, snapshot := range snapshots {
		snapshotHashes[snapshot.Duty.String()] = snapshot.ContentHash
	}
	startPayload, err := json.Marshal(struct {
		RolePolicySnapshotHashes map[string]string `json:"role_policy_snapshot_hashes"`
	}{RolePolicySnapshotHashes: snapshotHashes})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: encode policy snapshot hashes: %w", err)
	}
	activity, err = qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
		Type: activityMissionStarted, ActorType: "user", ActorID: params.ActorID,
		SubjectType: activitySubjectMission, SubjectID: params.MissionID,
		CausationID: params.CommandID, CorrelationID: correlationID,
		PayloadVersion: 1, Payload: startPayload, DedupeKey: dedupeKey, Sequence: sequence,
	})
	if err != nil {
		if isActivityDedupeViolation(err) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return StartMissionResult{}, fmt.Errorf("start mission: rollback command race: %w", rollbackErr)
			}
			return r.loadStartMissionByDedupeKey(ctx, params.WorkspaceID, params.MissionID, dedupeKey)
		}
		return StartMissionResult{}, fmt.Errorf("start mission: create activity: %w", err)
	}
	activities := []db.OrchestrationActivity{activity}
	nodesByKey := make(map[string]db.TaskNode, len(taskNodes))
	for _, node := range taskNodes {
		nodesByKey[node.NodeKey] = node
	}
	for _, key := range readyKeys {
		node := nodesByKey[key]
		readyNode, updateErr := qtx.MarkTaskNodeReady(ctx, db.MarkTaskNodeReadyParams{
			IssueID: node.IssueID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
			ExpectedRevision: node.Revision,
		})
		if updateErr != nil {
			return StartMissionResult{}, fmt.Errorf("start mission: mark task %q ready: %w", key, updateErr)
		}
		nodesByKey[key] = readyNode
		sequence, updateErr = allocateActivitySequence(ctx, qtx, params.WorkspaceID, params.MissionID)
		if updateErr != nil {
			return StartMissionResult{}, fmt.Errorf("start mission: allocate task activity sequence: %w", updateErr)
		}
		payload, marshalErr := json.Marshal(struct {
			NodeKey string `json:"node_key"`
		}{NodeKey: key})
		if marshalErr != nil {
			return StartMissionResult{}, fmt.Errorf("start mission: encode task activity payload: %w", marshalErr)
		}
		taskActivity, createErr := qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
			WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: readyNode.IssueID,
			Type: activityTaskReady, ActorType: "orchestrator",
			SubjectType: "task_node", SubjectID: readyNode.IssueID,
			CausationID: params.CommandID, CorrelationID: correlationID,
			PayloadVersion: 1, Payload: payload,
			DedupeKey: dedupeKey + ":task:" + key + ":ready", Sequence: sequence,
		})
		if createErr != nil {
			return StartMissionResult{}, fmt.Errorf("start mission: create task %q activity: %w", key, createErr)
		}
		activities = append(activities, taskActivity)
	}
	for index := range taskNodes {
		taskNodes[index] = nodesByKey[taskNodes[index].NodeKey]
	}
	if err := tx.Commit(ctx); err != nil {
		return StartMissionResult{}, fmt.Errorf("start mission: commit: %w", err)
	}
	return StartMissionResult{Mission: mission, TaskNodes: taskNodes, Activities: activities, RolePolicySnapshots: snapshots}, nil
}

func (r *Repository) CancelMission(ctx context.Context, params CancelMissionParams) (CancelMissionResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: repository is not configured")
	}
	dedupeKey, err := commandDedupeKey(params.CommandID)
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: %w", err)
	}
	correlationID := correlationOrCommand(params.CorrelationID, params.CommandID)

	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)

	activity, err := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey,
	})
	if err == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return CancelMissionResult{}, fmt.Errorf("cancel mission: rollback idempotent transaction: %w", rollbackErr)
		}
		return r.loadCancelMissionResult(ctx, params.WorkspaceID, params.MissionID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: check command: %w", err)
	}

	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{
		IssueID: params.MissionID, WorkspaceID: params.WorkspaceID,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: lock mission: %w", err)
	}
	if !missionCanCancel(MissionStatus(mission.Status)) || mission.Revision != params.ExpectedRevision {
		replayed, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
			WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey,
		})
		if replayErr == nil {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return CancelMissionResult{}, fmt.Errorf("cancel mission: rollback concurrent replay: %w", rollbackErr)
			}
			return r.loadCancelMissionResult(ctx, params.WorkspaceID, params.MissionID, replayed, true)
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return CancelMissionResult{}, fmt.Errorf("cancel mission: check concurrent replay: %w", replayErr)
		}
		if !missionCanCancel(MissionStatus(mission.Status)) {
			return CancelMissionResult{}, ErrMissionNotCancellable
		}
		return CancelMissionResult{}, ErrRevisionConflict
	}

	mission, err = qtx.CancelMissionRecord(ctx, db.CancelMissionRecordParams{
		IssueID: params.MissionID, WorkspaceID: params.WorkspaceID,
		ExpectedRevision: params.ExpectedRevision,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: update mission: %w", err)
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: params.MissionID, Status: "cancelled", WorkspaceID: params.WorkspaceID,
	}); err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: update root issue compatibility status: %w", err)
	}
	cancelledNodes, err := qtx.CancelMissionTaskNodes(ctx, db.CancelMissionTaskNodesParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: cancel task nodes: %w", err)
	}
	if _, err := qtx.RevokeMissionAssignments(ctx, db.RevokeMissionAssignmentsParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
	}); err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: revoke assignments: %w", err)
	}
	activeRuns, err := qtx.ListActiveMissionRuns(ctx, db.ListActiveMissionRunsParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: list active runs: %w", err)
	}

	sequence, err := allocateActivitySequence(ctx, qtx, params.WorkspaceID, params.MissionID)
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: %w", err)
	}
	payload, err := json.Marshal(struct {
		Reason string `json:"reason,omitempty"`
	}{Reason: params.Reason})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: encode activity payload: %w", err)
	}
	activity, err = qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
		Type: activityMissionCancelled, ActorType: "user", ActorID: params.ActorID,
		SubjectType: activitySubjectMission, SubjectID: params.MissionID,
		CausationID: params.CommandID, CorrelationID: correlationID,
		PayloadVersion: 1, Payload: payload, DedupeKey: dedupeKey, Sequence: sequence,
	})
	if err != nil {
		if isActivityDedupeViolation(err) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return CancelMissionResult{}, fmt.Errorf("cancel mission: rollback command race: %w", rollbackErr)
			}
			return r.loadCancelMissionByDedupeKey(ctx, params.WorkspaceID, params.MissionID, dedupeKey)
		}
		return CancelMissionResult{}, fmt.Errorf("cancel mission: create activity: %w", err)
	}
	activities := []db.OrchestrationActivity{activity}
	sort.Slice(cancelledNodes, func(i, j int) bool { return cancelledNodes[i].NodeKey < cancelledNodes[j].NodeKey })
	for _, node := range cancelledNodes {
		sequence, err = allocateActivitySequence(ctx, qtx, params.WorkspaceID, params.MissionID)
		if err != nil {
			return CancelMissionResult{}, fmt.Errorf("cancel mission: allocate task activity sequence: %w", err)
		}
		taskPayload, marshalErr := json.Marshal(struct {
			NodeKey string `json:"node_key"`
		}{NodeKey: node.NodeKey})
		if marshalErr != nil {
			return CancelMissionResult{}, fmt.Errorf("cancel mission: encode task activity payload: %w", marshalErr)
		}
		taskActivity, createErr := qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
			WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: node.IssueID,
			Type: activityTaskCancelled, ActorType: "orchestrator",
			SubjectType: "task_node", SubjectID: node.IssueID,
			CausationID: params.CommandID, CorrelationID: correlationID,
			PayloadVersion: 1, Payload: taskPayload,
			DedupeKey: dedupeKey + ":task:" + node.NodeKey + ":cancelled", Sequence: sequence,
		})
		if createErr != nil {
			return CancelMissionResult{}, fmt.Errorf("cancel mission: create task %q activity: %w", node.NodeKey, createErr)
		}
		activities = append(activities, taskActivity)
	}
	taskNodes, err := qtx.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: reload task nodes: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CancelMissionResult{}, fmt.Errorf("cancel mission: commit: %w", err)
	}
	return CancelMissionResult{
		Mission: mission, TaskNodes: taskNodes, ActiveRuns: activeRuns, Activities: activities,
	}, nil
}

func initialReadyNodeKeys(taskNodes []db.TaskNode, dependencies []db.IssueDependency, maxParallelRuns int) ([]string, error) {
	keysByID := make(map[pgtype.UUID]string, len(taskNodes))
	dependencyKeys := make(map[pgtype.UUID][]string, len(taskNodes))
	for _, node := range taskNodes {
		keysByID[node.IssueID] = node.NodeKey
	}
	for _, dependency := range dependencies {
		key, ok := keysByID[dependency.DependsOnIssueID]
		if !ok {
			return nil, fmt.Errorf("dependency predecessor %s is outside the mission", uuidText(dependency.DependsOnIssueID))
		}
		dependencyKeys[dependency.IssueID] = append(dependencyKeys[dependency.IssueID], key)
	}
	snapshots := make([]NodeSnapshot, 0, len(taskNodes))
	for index, node := range taskNodes {
		snapshots = append(snapshots, NodeSnapshot{
			Key: node.NodeKey, Status: TaskStatus(node.Status), Priority: int(node.Priority),
			CreatedOrder: index, DependencyKeys: dependencyKeys[node.IssueID],
		})
	}
	return ReadyNodeKeys(MissionStatusRunning, snapshots, 0, maxParallelRuns)
}

func decodePlanLimits(data []byte) (PlanLimits, error) {
	var limits PlanLimits
	if err := json.Unmarshal(data, &limits); err != nil {
		return PlanLimits{}, fmt.Errorf("decode mission limits: %w", err)
	}
	if limits.MaxParallelRuns < 1 {
		return PlanLimits{}, fmt.Errorf("mission max_parallel_runs must be at least 1")
	}
	return limits, nil
}

func missionCanCancel(status MissionStatus) bool {
	switch status {
	case MissionStatusDraft, MissionStatusReady, MissionStatusRunning, MissionStatusBlocked:
		return true
	default:
		return false
	}
}

func (r *Repository) loadStartMissionByDedupeKey(ctx context.Context, workspaceID, missionID pgtype.UUID, dedupeKey string) (StartMissionResult, error) {
	activity, err := r.queries.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: workspaceID, DedupeKey: dedupeKey,
	})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("load start mission command result: %w", err)
	}
	return r.loadStartMissionResult(ctx, workspaceID, missionID, activity, true)
}

func (r *Repository) loadStartMissionResult(ctx context.Context, workspaceID, missionID pgtype.UUID, activity db.OrchestrationActivity, idempotent bool) (StartMissionResult, error) {
	if activity.Type != activityMissionStarted || activity.SubjectType != activitySubjectMission || activity.MissionID != missionID {
		return StartMissionResult{}, ErrCommandConflict
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{
		IssueID: missionID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("load started mission command result: %w", err)
	}
	taskNodes, err := r.queries.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{
		WorkspaceID: workspaceID, MissionID: missionID,
	})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("load started task nodes command result: %w", err)
	}
	activities, err := r.queries.ListOrchestrationActivitiesByCausation(ctx, db.ListOrchestrationActivitiesByCausationParams{
		WorkspaceID: workspaceID, MissionID: missionID, CausationID: activity.CausationID,
	})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("load start activities command result: %w", err)
	}
	snapshotRows, err := r.queries.ListMissionRolePolicySnapshots(ctx, db.ListMissionRolePolicySnapshotsParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return StartMissionResult{}, fmt.Errorf("load start role policies command result: %w", err)
	}
	snapshots, err := mapRolePolicySnapshots(snapshotRows)
	if err != nil {
		return StartMissionResult{}, err
	}
	return StartMissionResult{Mission: mission, TaskNodes: taskNodes, Activities: activities, RolePolicySnapshots: snapshots, Idempotent: idempotent}, nil
}

func (r *Repository) loadCancelMissionByDedupeKey(ctx context.Context, workspaceID, missionID pgtype.UUID, dedupeKey string) (CancelMissionResult, error) {
	activity, err := r.queries.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: workspaceID, DedupeKey: dedupeKey,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("load cancel mission command result: %w", err)
	}
	return r.loadCancelMissionResult(ctx, workspaceID, missionID, activity, true)
}

func (r *Repository) loadCancelMissionResult(ctx context.Context, workspaceID, missionID pgtype.UUID, activity db.OrchestrationActivity, idempotent bool) (CancelMissionResult, error) {
	if activity.Type != activityMissionCancelled || activity.SubjectType != activitySubjectMission || activity.MissionID != missionID {
		return CancelMissionResult{}, ErrCommandConflict
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{
		IssueID: missionID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("load cancelled mission command result: %w", err)
	}
	taskNodes, err := r.queries.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{
		WorkspaceID: workspaceID, MissionID: missionID,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("load cancelled task nodes command result: %w", err)
	}
	activeRuns, err := r.queries.ListActiveMissionRuns(ctx, db.ListActiveMissionRunsParams{
		WorkspaceID: workspaceID, MissionID: missionID,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("load cancel active runs command result: %w", err)
	}
	activities, err := r.queries.ListOrchestrationActivitiesByCausation(ctx, db.ListOrchestrationActivitiesByCausationParams{
		WorkspaceID: workspaceID, MissionID: missionID, CausationID: activity.CausationID,
	})
	if err != nil {
		return CancelMissionResult{}, fmt.Errorf("load cancel activities command result: %w", err)
	}
	return CancelMissionResult{
		Mission: mission, TaskNodes: taskNodes, ActiveRuns: activeRuns,
		Activities: activities, Idempotent: idempotent,
	}, nil
}
