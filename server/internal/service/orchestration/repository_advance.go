package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	activityMissionBlocked   = "mission.blocked"
	activityMissionCompleted = "mission.completed"
	activityMissionFailed    = "mission.failed"
	activityBudgetExceeded   = "budget.exceeded"
	activityBudgetApproval   = "budget.approval_required"
	activityRunQueued        = "run.queued"
	activityTaskFailed       = "task.failed"
)

func (r *Repository) AdvanceMission(ctx context.Context, params AdvanceMissionParams) (AdvanceMissionResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: repository is not configured")
	}
	if params.ObservedAt.IsZero() || params.DispatchWindow <= 0 || params.RunTimeout <= 0 {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: time budgets are required")
	}
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: params.MissionID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: lock mission: %w", err)
	}
	result := AdvanceMissionResult{Mission: mission, ActorID: mission.CreatedBy}
	if MissionStatus(mission.Status) != MissionStatusRunning && MissionStatus(mission.Status) != MissionStatusBlocked {
		return result, nil
	}
	limits, err := decodePlanLimits(mission.Limits)
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: %w", err)
	}
	nodes, err := qtx.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list task nodes: %w", err)
	}
	if mission.Status == string(MissionStatusBlocked) &&
		(mission.BudgetGateStatus == BudgetGateStatusApprovalRequired || mission.BudgetGateStatus == BudgetGateStatusExceeded) {
		queued, queuedErr := qtx.ListQueuedOrchestrationRunsWithoutTask(ctx, db.ListQueuedOrchestrationRunsWithoutTaskParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
		if queuedErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: list queued runs while budget blocked: %w", queuedErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: commit budget-blocked snapshot: %w", commitErr)
		}
		result.TaskNodes = nodes
		result.RunsToEnqueue = queued
		return result, nil
	}
	usageRow, err := qtx.GetMissionBudgetUsage(ctx, db.GetMissionBudgetUsageParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: load budget usage: %w", err)
	}
	budgetUsage := BudgetUsage{
		ConsumedTokens: usageRow.ConsumedTokens, ReservedTokens: usageRow.ReservedTokens,
		ConsumedCostUSDTicks: usageRow.ConsumedCostUsdTicks, ReservedCostUSDTicks: usageRow.ReservedCostUsdTicks,
	}
	budgetAllowance := BudgetAllowance{GrantTokens: mission.BudgetGrantTokens, GrantCostUSDTicks: mission.BudgetGrantCostUsdTicks}
	dependencies, err := qtx.ListOrchestrationIssueDependencies(ctx, db.ListOrchestrationIssueDependenciesParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list dependencies: %w", err)
	}
	assignments, err := qtx.ListOrchestrationAssignmentsByMission(ctx, db.ListOrchestrationAssignmentsByMissionParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list assignments: %w", err)
	}
	runs, err := qtx.ListOrchestrationRunsByMission(ctx, db.ListOrchestrationRunsByMissionParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list runs: %w", err)
	}
	artifacts, err := qtx.ListArtifactsByMission(ctx, db.ListArtifactsByMissionParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list artifacts: %w", err)
	}
	verdicts, err := qtx.ListReviewVerdictsByMission(ctx, db.ListReviewVerdictsByMissionParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list review verdicts: %w", err)
	}
	activities := []db.OrchestrationActivity{}
	changed := false
	updateNode := func(updated db.TaskNode) {
		for index := range nodes {
			if nodes[index].IssueID == updated.IssueID {
				nodes[index] = updated
				return
			}
		}
	}

	// A failed Run is a technical fact. Retry it on the same Assignment until
	// the mission snapshot budget is exhausted.
	for _, assignment := range assignments {
		if assignment.Status != string(AssignmentStatusActive) {
			continue
		}
		latest, ok := latestRunForAssignment(runs, assignment.ID)
		if !ok || latest.Status != string(RunStatusFailed) {
			continue
		}
		policy := EvaluateFailurePolicy(latest.FailureKind.String, int(latest.Attempt), limits.MaxTaskAttempts)
		if policy.RetrySameAssignment {
			node := findNode(nodes, assignment.TaskNodeID)
			if node == nil {
				return AdvanceMissionResult{}, fmt.Errorf("advance mission: retry task node is missing")
			}
			estimate := budgetEstimateForNode(*node)
			decision := EvaluateBudgetAdmission(limits.Budget, budgetUsage, budgetAllowance, estimate)
			if !decision.Allowed {
				return finishBudgetBlockedAdvance(ctx, tx, qtx, mission, nodes, result, activities, changed, params, decision)
			}
			retry, createErr := qtx.CreateOrchestrationRun(ctx, db.CreateOrchestrationRunParams{
				WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: assignment.TaskNodeID,
				AssignmentID: assignment.ID, Purpose: latest.Purpose, Attempt: latest.Attempt + 1,
				Input: latest.Input, RetryOfID: latest.ID,
				DispatchDeadlineAt: timestamptz(params.ObservedAt.Add(params.DispatchWindow)),
				TimeoutSeconds:     int32(params.RunTimeout / time.Second),
			})
			if createErr != nil {
				return AdvanceMissionResult{}, fmt.Errorf("advance mission: create retry run: %w", createErr)
			}
			runs = append(runs, retry)
			budgetUsage = AddBudgetReservation(budgetUsage, estimate)
			result.CreatedRuns = append(result.CreatedRuns, retry)
			activity, activityErr := createAutomaticActivity(ctx, qtx, mission, retry.TaskNodeID, retry.ID, activityRunQueued, "run", retry.ID, retry.ID, params.CorrelationID, "run:"+uuidText(retry.ID)+":queued", map[string]any{"attempt": retry.Attempt, "retry_of_id": uuidText(latest.ID)})
			if activityErr != nil {
				return AdvanceMissionResult{}, activityErr
			}
			activities = append(activities, activity)
			changed = true
			continue
		}
		node := findNode(nodes, assignment.TaskNodeID)
		if node == nil || isTerminalTaskStatus(TaskStatus(node.Status)) {
			continue
		}
		target := policy.ExhaustedTaskStatus
		blockReason := pgtype.Text{}
		if target == TaskStatusBlocked {
			blockReason = textValue(latest.FailureMessage.String)
		}
		updated, updateErr := qtx.TransitionTaskNodeState(ctx, db.TransitionTaskNodeStateParams{
			TargetStatus: target.String(), BlockReason: blockReason, TaskNodeID: node.IssueID,
			WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedStatus: node.Status,
		})
		if updateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: exhaust task attempts: %w", updateErr)
		}
		updateNode(updated)
		if _, updateErr := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: updated.IssueID, WorkspaceID: params.WorkspaceID, Status: "blocked"}); updateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: update exhausted task issue: %w", updateErr)
		}
		if _, endErr := qtx.EndOrchestrationAssignment(ctx, db.EndOrchestrationAssignmentParams{TargetStatus: string(AssignmentStatusRevoked), AssignmentID: assignment.ID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID}); endErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: revoke exhausted assignment: %w", endErr)
		}
		activityType := activityTaskFailed
		if target == TaskStatusBlocked {
			activityType = activityTaskBlocked
		}
		activity, activityErr := createAutomaticActivity(ctx, qtx, mission, updated.IssueID, pgtype.UUID{}, activityType, "task_node", updated.IssueID, latest.ID, params.CorrelationID, "task:"+uuidText(updated.IssueID)+":"+target.String()+":attempts", map[string]any{"failure_kind": latest.FailureKind.String})
		if activityErr != nil {
			return AdvanceMissionResult{}, activityErr
		}
		activities = append(activities, activity)
		changed = true
	}

	depsByNode := make(map[pgtype.UUID][]pgtype.UUID)
	for _, dependency := range dependencies {
		depsByNode[dependency.IssueID] = append(depsByNode[dependency.IssueID], dependency.DependsOnIssueID)
	}
	for index := range nodes {
		node := nodes[index]
		if TaskStatus(node.Status) != TaskStatusPending && TaskStatus(node.Status) != TaskStatusRework {
			continue
		}
		allCompleted := true
		var failedDependency pgtype.UUID
		for _, dependencyID := range depsByNode[node.IssueID] {
			dependency := findNode(nodes, dependencyID)
			if dependency == nil || TaskStatus(dependency.Status) != TaskStatusCompleted {
				allCompleted = false
			}
			if dependency != nil && (TaskStatus(dependency.Status) == TaskStatusFailed || TaskStatus(dependency.Status) == TaskStatusBlocked || TaskStatus(dependency.Status) == TaskStatusCancelled) {
				failedDependency = dependencyID
				break
			}
		}
		target := TaskStatusReady
		blockReason := pgtype.Text{}
		activityType := activityTaskReady
		if failedDependency.Valid {
			target = TaskStatusBlocked
			blockReason = textValue("dependency " + uuidText(failedDependency) + " is not completable")
			activityType = activityTaskBlocked
		} else if !allCompleted {
			continue
		}
		updated, updateErr := qtx.TransitionTaskNodeState(ctx, db.TransitionTaskNodeStateParams{
			TargetStatus: target.String(), BlockReason: blockReason, TaskNodeID: node.IssueID,
			WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedStatus: node.Status,
		})
		if updateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: propagate task readiness: %w", updateErr)
		}
		nodes[index] = updated
		issueStatus := "todo"
		if target == TaskStatusBlocked {
			issueStatus = "blocked"
		}
		if _, updateErr := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: updated.IssueID, WorkspaceID: params.WorkspaceID, Status: issueStatus}); updateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: update ready issue: %w", updateErr)
		}
		activity, activityErr := createAutomaticActivity(ctx, qtx, mission, updated.IssueID, pgtype.UUID{}, activityType, "task_node", updated.IssueID, updated.IssueID, params.CorrelationID, fmt.Sprintf("task:%s:%s:%d", uuidText(updated.IssueID), target, updated.ReworkCount), map[string]any{"node_key": updated.NodeKey})
		if activityErr != nil {
			return AdvanceMissionResult{}, activityErr
		}
		activities = append(activities, activity)
		changed = true
	}

	policyRows, err := qtx.ListMissionRolePolicySnapshots(ctx, db.ListMissionRolePolicySnapshotsParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list role policy snapshots: %w", err)
	}
	policies, err := mapRolePolicySnapshots(policyRows)
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: decode role policy snapshots: %w", err)
	}
	policyByDuty := make(map[Duty]RolePolicySnapshot, len(policies))
	for _, policy := range policies {
		policyByDuty[policy.Duty] = policy
	}

	activeRuns := countActiveRuns(runs)
	for index := range nodes {
		if activeRuns >= limits.MaxParallelRuns {
			break
		}
		node := nodes[index]
		if TaskStatus(node.Status) != TaskStatusReady {
			continue
		}
		duty := Duty(node.Role)
		purpose := "execute"
		if duty == DutyIntegrator {
			purpose = "integrate"
		}
		policy, ok := policyByDuty[duty]
		if !ok {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: frozen %s role policy snapshot is missing", duty)
		}
		estimate := budgetEstimateForNode(node)
		decision := EvaluateBudgetAdmission(limits.Budget, budgetUsage, budgetAllowance, estimate)
		if !decision.Allowed {
			return finishBudgetBlockedAdvance(ctx, tx, qtx, mission, nodes, result, activities, changed, params, decision)
		}
		routing, candidateErr := selectAndLockRoutingCandidate(ctx, qtx, params.WorkspaceID, mission.CreatedBy, policy, "", "")
		if candidateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: select %s routing candidate: %w", duty, candidateErr)
		}
		if routing.Selected == nil {
			reason := "no eligible routing candidate for " + duty.String()
			updated, updateErr := qtx.TransitionTaskNodeState(ctx, db.TransitionTaskNodeStateParams{TargetStatus: string(TaskStatusBlocked), BlockReason: textValue(reason), TaskNodeID: node.IssueID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedStatus: node.Status})
			if updateErr != nil {
				return AdvanceMissionResult{}, fmt.Errorf("advance mission: block task without %s candidate: %w", duty, updateErr)
			}
			nodes[index] = updated
			if _, updateErr := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: updated.IssueID, WorkspaceID: params.WorkspaceID, Status: "blocked"}); updateErr != nil {
				return AdvanceMissionResult{}, fmt.Errorf("advance mission: update routing-blocked issue: %w", updateErr)
			}
			activity, activityErr := createAutomaticActivity(ctx, qtx, mission, updated.IssueID, pgtype.UUID{}, activityTaskBlocked, "task_node", updated.IssueID, updated.IssueID, params.CorrelationID, fmt.Sprintf("task:%s:blocked:routing:%s:%d", uuidText(updated.IssueID), duty, updated.ReworkCount), map[string]any{"reason": reason, "duty": duty, "routing": routing})
			if activityErr != nil {
				return AdvanceMissionResult{}, activityErr
			}
			activities = append(activities, activity)
			changed = true
			continue
		}
		candidateAgentID, candidateErr := uuidFromText(routing.Selected.AgentID)
		if candidateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: decode selected %s agent id: %w", duty, candidateErr)
		}
		candidateRuntimeID, candidateErr := uuidFromText(routing.Selected.RuntimeID)
		if candidateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: decode selected %s runtime id: %w", duty, candidateErr)
		}
		sequence, supersedes := nextAssignmentSequence(assignments, node.IssueID, duty.String())
		assignment, createErr := qtx.CreateOrchestrationAssignment(ctx, db.CreateOrchestrationAssignmentParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: node.IssueID, Role: duty.String(), AgentID: candidateAgentID, RuntimeID: candidateRuntimeID, Sequence: sequence, SupersedesID: supersedes, CreatedBy: mission.CreatedBy})
		if createErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: create work assignment: %w", createErr)
		}
		assignments = append(assignments, assignment)
		input, inputErr := workRunInput(node, depsByNode[node.IssueID], artifacts, verdicts)
		if inputErr != nil {
			return AdvanceMissionResult{}, inputErr
		}
		mailboxContext, mailboxRows, mailboxErr := selectMailboxRunContext(
			ctx, qtx, params.WorkspaceID, params.MissionID, node.IssueID, candidateAgentID, params.ObservedAt,
		)
		if mailboxErr != nil {
			return AdvanceMissionResult{}, mailboxErr
		}
		input, inputErr = attachMailboxRunContext(input, mailboxContext)
		if inputErr != nil {
			return AdvanceMissionResult{}, inputErr
		}
		run, createErr := qtx.CreateOrchestrationRun(ctx, db.CreateOrchestrationRunParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: node.IssueID, AssignmentID: assignment.ID, Purpose: purpose, Attempt: 1, Input: input, DispatchDeadlineAt: timestamptz(params.ObservedAt.Add(params.DispatchWindow)), TimeoutSeconds: int32(policy.Config.TimeoutSeconds)})
		if createErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: create work run: %w", createErr)
		}
		mailboxActivities, mailboxErr := consumeMailboxRunContext(
			ctx, qtx, mission, run, candidateAgentID, mailboxContext, mailboxRows, params.ObservedAt, params.CorrelationID,
		)
		if mailboxErr != nil {
			return AdvanceMissionResult{}, mailboxErr
		}
		activities = append(activities, mailboxActivities...)
		runs = append(runs, run)
		budgetUsage = AddBudgetReservation(budgetUsage, estimate)
		result.CreatedRuns = append(result.CreatedRuns, run)
		updated, updateErr := qtx.TransitionTaskNodeState(ctx, db.TransitionTaskNodeStateParams{TargetStatus: string(TaskStatusAssigned), TaskNodeID: node.IssueID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedStatus: node.Status})
		if updateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: assign task node: %w", updateErr)
		}
		nodes[index] = updated
		for _, spec := range []struct {
			typ, subject string
			subjectID    pgtype.UUID
			key          string
		}{{activityTaskAssigned, "task_node", node.IssueID, "task:" + uuidText(node.IssueID) + ":assigned:" + fmt.Sprint(sequence)}, {activityRunQueued, "run", run.ID, "run:" + uuidText(run.ID) + ":queued"}} {
			payload := map[string]any{"duty": duty, "agent_id": routing.Selected.AgentID}
			if spec.typ == activityTaskAssigned {
				payload["routing"] = routing
			}
			activity, activityErr := createAutomaticActivity(ctx, qtx, mission, node.IssueID, run.ID, spec.typ, spec.subject, spec.subjectID, run.ID, params.CorrelationID, spec.key, payload)
			if activityErr != nil {
				return AdvanceMissionResult{}, activityErr
			}
			activities = append(activities, activity)
		}
		activeRuns++
		changed = true
	}

	// A reviewer Run is created only for an Artifact produced by the latest
	// successful work Run. Old artifacts can never be silently re-reviewed.
	for _, node := range nodes {
		if activeRuns >= limits.MaxParallelRuns || TaskStatus(node.Status) != TaskStatusReview {
			continue
		}
		workRun, ok := latestWorkRun(runs, node.IssueID)
		if !ok || workRun.Status != string(RunStatusSucceeded) {
			continue
		}
		artifact, ok := latestArtifactForRun(artifacts, workRun.ID)
		if !ok || hasActiveAssignment(assignments, node.IssueID, DutyReviewer.String()) {
			continue
		}
		workAssignment := findAssignment(assignments, workRun.AssignmentID)
		producerAgentID := ""
		if workAssignment != nil {
			producerAgentID = uuidText(workAssignment.AgentID)
		}
		policy, ok := policyByDuty[DutyReviewer]
		if !ok {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: frozen %s role policy snapshot is missing", DutyReviewer)
		}
		estimate := budgetEstimateForNode(node)
		decision := EvaluateBudgetAdmission(limits.Budget, budgetUsage, budgetAllowance, estimate)
		if !decision.Allowed {
			return finishBudgetBlockedAdvance(ctx, tx, qtx, mission, nodes, result, activities, changed, params, decision)
		}
		routing, candidateErr := selectAndLockRoutingCandidate(ctx, qtx, params.WorkspaceID, mission.CreatedBy, policy, producerAgentID, "")
		if candidateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: select reviewer routing candidate: %w", candidateErr)
		}
		if routing.Selected == nil {
			reason := "independent reviewer is unavailable"
			updated, updateErr := qtx.TransitionTaskNodeState(ctx, db.TransitionTaskNodeStateParams{TargetStatus: string(TaskStatusBlocked), BlockReason: textValue(reason), TaskNodeID: node.IssueID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedStatus: node.Status})
			if updateErr != nil {
				return AdvanceMissionResult{}, fmt.Errorf("advance mission: block task without reviewer candidate: %w", updateErr)
			}
			updateNode(updated)
			if _, updateErr := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: updated.IssueID, WorkspaceID: params.WorkspaceID, Status: "blocked"}); updateErr != nil {
				return AdvanceMissionResult{}, fmt.Errorf("advance mission: update reviewer-routing-blocked issue: %w", updateErr)
			}
			gate, gateActivity, gateErr := createPendingHumanGate(
				ctx, qtx, mission, updated, artifact, workRun.ID, HumanGateReviewerUnavailable,
				reason, map[string]any{"duty": DutyReviewer, "routing": routing}, workRun.ID, params.CorrelationID,
			)
			if gateErr != nil {
				return AdvanceMissionResult{}, fmt.Errorf("advance mission: create reviewer human gate: %w", gateErr)
			}
			activities = append(activities, gateActivity)
			activity, activityErr := createAutomaticActivity(ctx, qtx, mission, updated.IssueID, pgtype.UUID{}, activityTaskBlocked, "task_node", updated.IssueID, workRun.ID, params.CorrelationID, fmt.Sprintf("task:%s:blocked:routing:%s:gate:%s", uuidText(updated.IssueID), DutyReviewer, uuidText(gate.ID)), map[string]any{"reason": reason, "duty": DutyReviewer, "human_gate_id": uuidText(gate.ID), "routing": routing})
			if activityErr != nil {
				return AdvanceMissionResult{}, activityErr
			}
			activities = append(activities, activity)
			changed = true
			continue
		}
		candidateAgentID, candidateErr := uuidFromText(routing.Selected.AgentID)
		if candidateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: decode selected reviewer agent id: %w", candidateErr)
		}
		candidateRuntimeID, candidateErr := uuidFromText(routing.Selected.RuntimeID)
		if candidateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: decode selected reviewer runtime id: %w", candidateErr)
		}
		sequence, supersedes := nextAssignmentSequence(assignments, node.IssueID, DutyReviewer.String())
		assignment, createErr := qtx.CreateOrchestrationAssignment(ctx, db.CreateOrchestrationAssignmentParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: node.IssueID, Role: DutyReviewer.String(), AgentID: candidateAgentID, RuntimeID: candidateRuntimeID, Sequence: sequence, SupersedesID: supersedes, CreatedBy: mission.CreatedBy})
		if createErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: create reviewer assignment: %w", createErr)
		}
		assignments = append(assignments, assignment)
		input, _ := json.Marshal(map[string]any{"artifact_id": uuidText(artifact.ID), "artifact_kind": artifact.Kind, "artifact_uri": artifact.Uri, "acceptance_criteria": json.RawMessage(node.AcceptanceCriteria)})
		mailboxContext, mailboxRows, mailboxErr := selectMailboxRunContext(
			ctx, qtx, params.WorkspaceID, params.MissionID, node.IssueID, candidateAgentID, params.ObservedAt,
		)
		if mailboxErr != nil {
			return AdvanceMissionResult{}, mailboxErr
		}
		input, mailboxErr = attachMailboxRunContext(input, mailboxContext)
		if mailboxErr != nil {
			return AdvanceMissionResult{}, mailboxErr
		}
		run, createErr := qtx.CreateOrchestrationRun(ctx, db.CreateOrchestrationRunParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: node.IssueID, AssignmentID: assignment.ID, Purpose: "review", Attempt: 1, Input: input, DispatchDeadlineAt: timestamptz(params.ObservedAt.Add(params.DispatchWindow)), TimeoutSeconds: int32(policy.Config.TimeoutSeconds)})
		if createErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: create review run: %w", createErr)
		}
		mailboxActivities, mailboxErr := consumeMailboxRunContext(
			ctx, qtx, mission, run, candidateAgentID, mailboxContext, mailboxRows, params.ObservedAt, params.CorrelationID,
		)
		if mailboxErr != nil {
			return AdvanceMissionResult{}, mailboxErr
		}
		activities = append(activities, mailboxActivities...)
		runs = append(runs, run)
		budgetUsage = AddBudgetReservation(budgetUsage, estimate)
		result.CreatedRuns = append(result.CreatedRuns, run)
		activity, activityErr := createAutomaticActivity(ctx, qtx, mission, node.IssueID, run.ID, activityRunQueued, "run", run.ID, run.ID, params.CorrelationID, "run:"+uuidText(run.ID)+":queued", map[string]any{"purpose": "review", "artifact_id": uuidText(artifact.ID), "routing": routing})
		if activityErr != nil {
			return AdvanceMissionResult{}, activityErr
		}
		activities = append(activities, activity)
		activeRuns++
		changed = true
	}

	targetMission := MissionStatus(mission.Status)
	if allNodesHaveStatus(nodes, TaskStatusCompleted) {
		targetMission = MissionStatusCompleted
	} else if anyNodeHasStatus(nodes, TaskStatusFailed) {
		targetMission = MissionStatusFailed
	} else if activeRuns == 0 && anyNodeHasStatus(nodes, TaskStatusBlocked) && !anyNodeHasStatus(nodes, TaskStatusReady, TaskStatusPending, TaskStatusRework, TaskStatusReview) {
		targetMission = MissionStatusBlocked
	} else if changed && targetMission == MissionStatusBlocked {
		targetMission = MissionStatusRunning
	}
	if targetMission != MissionStatus(mission.Status) {
		if err := validateMissionTransition(MissionStatus(mission.Status), targetMission); err != nil {
			return AdvanceMissionResult{}, err
		}
		updated, updateErr := qtx.TransitionMissionState(ctx, db.TransitionMissionStateParams{TargetStatus: targetMission.String(), MissionID: mission.IssueID, WorkspaceID: mission.WorkspaceID, ExpectedStatus: mission.Status})
		if updateErr != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: transition mission: %w", updateErr)
		}
		mission = updated
		result.Mission = updated
		issueStatus := "in_progress"
		activityType := ""
		switch targetMission {
		case MissionStatusCompleted:
			issueStatus, activityType = "done", activityMissionCompleted
		case MissionStatusFailed:
			issueStatus, activityType = "blocked", activityMissionFailed
		case MissionStatusBlocked:
			issueStatus, activityType = "blocked", activityMissionBlocked
		}
		if _, updateErr := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: mission.IssueID, WorkspaceID: mission.WorkspaceID, Status: issueStatus}); updateErr != nil {
			return AdvanceMissionResult{}, updateErr
		}
		if activityType != "" {
			activity, activityErr := createAutomaticActivity(ctx, qtx, mission, pgtype.UUID{}, pgtype.UUID{}, activityType, "mission", mission.IssueID, mission.IssueID, params.CorrelationID, fmt.Sprintf("mission:%s:%s:revision:%d", uuidText(mission.IssueID), targetMission, mission.Revision), map[string]any{})
			if activityErr != nil {
				return AdvanceMissionResult{}, activityErr
			}
			activities = append(activities, activity)
		}
		changed = true
	}
	queued, err := qtx.ListQueuedOrchestrationRunsWithoutTask(ctx, db.ListQueuedOrchestrationRunsWithoutTaskParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list queued runs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: commit: %w", err)
	}
	result.Mission = mission
	result.TaskNodes = nodes
	result.Activities = activities
	result.RunsToEnqueue = queued
	result.Changed = changed
	return result, nil
}

func budgetEstimateForNode(node db.TaskNode) BudgetEstimate {
	return BudgetEstimate{Tokens: node.BudgetEstimateTokens, CostUSDTicks: node.BudgetEstimateCostUsdTicks}
}

func finishBudgetBlockedAdvance(
	ctx context.Context,
	tx pgx.Tx,
	qtx *db.Queries,
	mission db.Mission,
	nodes []db.TaskNode,
	result AdvanceMissionResult,
	activities []db.OrchestrationActivity,
	changed bool,
	params AdvanceMissionParams,
	decision BudgetDecision,
) (AdvanceMissionResult, error) {
	if mission.Status != string(MissionStatusBlocked) || mission.BudgetGateStatus != decision.Status {
		updated, err := qtx.SetMissionBudgetGate(ctx, db.SetMissionBudgetGateParams{
			BudgetGateStatus: decision.Status, MissionID: mission.IssueID, WorkspaceID: mission.WorkspaceID,
		})
		if err != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: set budget gate: %w", err)
		}
		mission = updated
		if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: mission.IssueID, WorkspaceID: mission.WorkspaceID, Status: "blocked"}); err != nil {
			return AdvanceMissionResult{}, fmt.Errorf("advance mission: update budget-blocked issue: %w", err)
		}
		activityType := activityBudgetExceeded
		if decision.Status == BudgetGateStatusApprovalRequired {
			activityType = activityBudgetApproval
		}
		dedupeKey := fmt.Sprintf("mission:%s:budget:%s:%s:%d:%d", uuidText(mission.IssueID), decision.Status, decision.Dimension, decision.Limit, decision.Effective)
		activity, err := createAutomaticActivity(ctx, qtx, mission, pgtype.UUID{}, pgtype.UUID{}, activityType, activitySubjectMission, mission.IssueID, mission.IssueID, params.CorrelationID, dedupeKey, decision)
		if err != nil {
			return AdvanceMissionResult{}, err
		}
		activities = append(activities, activity)
		changed = true
	}
	queued, err := qtx.ListQueuedOrchestrationRunsWithoutTask(ctx, db.ListQueuedOrchestrationRunsWithoutTaskParams{WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: list queued runs after budget gate: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AdvanceMissionResult{}, fmt.Errorf("advance mission: commit budget gate: %w", err)
	}
	result.Mission = mission
	result.TaskNodes = nodes
	result.Activities = activities
	result.RunsToEnqueue = queued
	result.Changed = changed
	return result, nil
}

func createAutomaticActivity(ctx context.Context, q *db.Queries, mission db.Mission, taskNodeID, runID pgtype.UUID, typ, subjectType string, subjectID, causationID, correlationID pgtype.UUID, dedupeKey string, payloadValue any) (db.OrchestrationActivity, error) {
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return db.OrchestrationActivity{}, err
	}
	sequence, err := allocateActivitySequence(ctx, q, mission.WorkspaceID, mission.IssueID)
	if err != nil {
		return db.OrchestrationActivity{}, err
	}
	activity, err := q.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID, TaskNodeID: taskNodeID, RunID: runID, Type: typ, ActorType: "orchestrator", SubjectType: subjectType, SubjectID: subjectID, CausationID: causationID, CorrelationID: correlationID, PayloadVersion: 1, Payload: payload, DedupeKey: dedupeKey, Sequence: sequence})
	if err != nil {
		return db.OrchestrationActivity{}, fmt.Errorf("advance mission: create %s activity: %w", typ, err)
	}
	return activity, nil
}

func latestRunForAssignment(runs []db.OrchestrationRun, id pgtype.UUID) (db.OrchestrationRun, bool) {
	var result db.OrchestrationRun
	ok := false
	for _, run := range runs {
		if run.AssignmentID == id && (!ok || run.Attempt > result.Attempt) {
			result, ok = run, true
		}
	}
	return result, ok
}
func latestWorkRun(runs []db.OrchestrationRun, nodeID pgtype.UUID) (db.OrchestrationRun, bool) {
	var result db.OrchestrationRun
	ok := false
	for _, run := range runs {
		if run.TaskNodeID == nodeID && run.Purpose != "review" {
			result, ok = run, true
		}
	}
	return result, ok
}
func latestArtifactForRun(artifacts []db.Artifact, runID pgtype.UUID) (db.Artifact, bool) {
	var result db.Artifact
	ok := false
	for _, artifact := range artifacts {
		if artifact.RunID == runID {
			result, ok = artifact, true
		}
	}
	return result, ok
}
func findNode(nodes []db.TaskNode, id pgtype.UUID) *db.TaskNode {
	for index := range nodes {
		if nodes[index].IssueID == id {
			return &nodes[index]
		}
	}
	return nil
}
func findAssignment(items []db.OrchestrationAssignment, id pgtype.UUID) *db.OrchestrationAssignment {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}
func hasActiveAssignment(items []db.OrchestrationAssignment, nodeID pgtype.UUID, role string) bool {
	for _, item := range items {
		if item.TaskNodeID == nodeID && item.Role == role && item.Status == string(AssignmentStatusActive) {
			return true
		}
	}
	return false
}
func nextAssignmentSequence(items []db.OrchestrationAssignment, nodeID pgtype.UUID, role string) (int32, pgtype.UUID) {
	var max int32
	var previous pgtype.UUID
	for _, item := range items {
		if item.TaskNodeID == nodeID && item.Role == role && item.Sequence >= max {
			max, previous = item.Sequence, item.ID
		}
	}
	return max + 1, previous
}
func countActiveRuns(runs []db.OrchestrationRun) int {
	count := 0
	for _, run := range runs {
		if run.Status == "queued" || run.Status == "dispatched" || run.Status == "running" {
			count++
		}
	}
	return count
}
func allNodesHaveStatus(nodes []db.TaskNode, status TaskStatus) bool {
	if len(nodes) == 0 {
		return false
	}
	for _, node := range nodes {
		if TaskStatus(node.Status) != status {
			return false
		}
	}
	return true
}
func anyNodeHasStatus(nodes []db.TaskNode, statuses ...TaskStatus) bool {
	allowed := map[TaskStatus]bool{}
	for _, status := range statuses {
		allowed[status] = true
	}
	for _, node := range nodes {
		if allowed[TaskStatus(node.Status)] {
			return true
		}
	}
	return false
}
func (s MissionStatus) String() string { return string(s) }

func workRunInput(node db.TaskNode, dependencyIDs []pgtype.UUID, artifacts []db.Artifact, verdicts []db.ReviewVerdict) ([]byte, error) {
	approved := map[pgtype.UUID]bool{}
	for _, verdict := range verdicts {
		if verdict.Decision == "approved" {
			approved[verdict.ArtifactID] = true
		}
	}
	dependencySet := map[pgtype.UUID]bool{}
	for _, id := range dependencyIDs {
		dependencySet[id] = true
	}
	inputs := []map[string]any{}
	for _, artifact := range artifacts {
		if dependencySet[artifact.TaskNodeID] && approved[artifact.ID] {
			inputs = append(inputs, map[string]any{"artifact_id": uuidText(artifact.ID), "kind": artifact.Kind, "uri": artifact.Uri})
		}
	}
	sort.Slice(inputs, func(i, j int) bool {
		return fmt.Sprint(inputs[i]["artifact_id"]) < fmt.Sprint(inputs[j]["artifact_id"])
	})
	payload, err := json.Marshal(map[string]any{"node_key": node.NodeKey, "acceptance_criteria": json.RawMessage(node.AcceptanceCriteria), "dependency_artifacts": inputs})
	if err != nil {
		return nil, fmt.Errorf("advance mission: encode work input: %w", err)
	}
	return payload, nil
}
