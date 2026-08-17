package orchestration

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func (r *Repository) loadProjectionFacts(ctx context.Context, workspaceID, missionID pgtype.UUID, activityLimit int32) (projectionFacts, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: repository is not configured")
	}
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: begin read transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: configure read transaction: %w", err)
	}
	queries := r.queries.WithTx(tx)
	facts := projectionFacts{
		issues: make(map[string]db.Issue), agents: make(map[string]db.Agent),
		runtimes: make(map[string]db.AgentRuntime), tasks: make(map[string]db.AgentTaskQueue),
	}
	facts.mission, err = queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return projectionFacts{}, err
	}
	facts.budgetUsage, err = queries.GetMissionBudgetUsage(ctx, db.GetMissionBudgetUsageParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: load budget usage: %w", err)
	}
	facts.missionIssue, err = queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: load mission issue: %w", err)
	}
	facts.nodes, err = queries.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: load task nodes: %w", err)
	}
	for _, node := range facts.nodes {
		issue, issueErr := queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: node.IssueID, WorkspaceID: workspaceID})
		if issueErr != nil {
			return projectionFacts{}, fmt.Errorf("load mission projection: load task issue: %w", issueErr)
		}
		facts.issues[uuidText(node.IssueID)] = issue
	}
	facts.dependencies, err = queries.ListOrchestrationIssueDependencies(ctx, db.ListOrchestrationIssueDependenciesParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: load dependencies: %w", err)
	}
	facts.assignments, err = queries.ListOrchestrationAssignmentsByMission(ctx, db.ListOrchestrationAssignmentsByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: load assignments: %w", err)
	}
	facts.runs, err = queries.ListOrchestrationRunsByMission(ctx, db.ListOrchestrationRunsByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: load runs: %w", err)
	}
	facts.artifacts, err = queries.ListArtifactsByMission(ctx, db.ListArtifactsByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: load artifacts: %w", err)
	}
	facts.verdicts, err = queries.ListReviewVerdictsByMission(ctx, db.ListReviewVerdictsByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: load review verdicts: %w", err)
	}
	if activityLimit > 0 {
		facts.activities, err = queries.ListRecentOrchestrationActivities(ctx, db.ListRecentOrchestrationActivitiesParams{
			WorkspaceID: workspaceID, MissionID: missionID, PageSize: activityLimit,
		})
		if err != nil {
			return projectionFacts{}, fmt.Errorf("load mission projection: load activities: %w", err)
		}
	}

	for _, assignment := range facts.assignments {
		agentKey := uuidText(assignment.AgentID)
		if _, exists := facts.agents[agentKey]; !exists {
			agent, agentErr := queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: assignment.AgentID, WorkspaceID: workspaceID})
			if agentErr == nil {
				facts.agents[agentKey] = agent
			} else if !errors.Is(agentErr, pgx.ErrNoRows) {
				return projectionFacts{}, fmt.Errorf("load mission projection: load agent: %w", agentErr)
			}
		}
		runtimeKey := uuidText(assignment.RuntimeID)
		if _, exists := facts.runtimes[runtimeKey]; !exists {
			runtime, runtimeErr := queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: assignment.RuntimeID, WorkspaceID: workspaceID})
			if runtimeErr == nil {
				facts.runtimes[runtimeKey] = runtime
			} else if !errors.Is(runtimeErr, pgx.ErrNoRows) {
				return projectionFacts{}, fmt.Errorf("load mission projection: load runtime: %w", runtimeErr)
			}
		}
	}
	for _, run := range facts.runs {
		task, taskErr := queries.GetOrchestrationRunExecutionInWorkspace(ctx, db.GetOrchestrationRunExecutionInWorkspaceParams{
			RunID: run.ID, WorkspaceID: workspaceID,
		})
		if taskErr == nil {
			facts.tasks[uuidText(run.ID)] = task
		} else if !errors.Is(taskErr, pgx.ErrNoRows) {
			return projectionFacts{}, fmt.Errorf("load mission projection: load run execution: %w", taskErr)
		}
	}
	sortProjectionFacts(&facts)
	if err := tx.Commit(ctx); err != nil {
		return projectionFacts{}, fmt.Errorf("load mission projection: commit read transaction: %w", err)
	}
	return facts, nil
}

func (r *Repository) loadActivityPageFacts(ctx context.Context, workspaceID, missionID pgtype.UUID, afterSequence int64, pageSize int32) (db.Mission, []db.OrchestrationActivity, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return db.Mission{}, nil, fmt.Errorf("load mission activities: repository is not configured")
	}
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return db.Mission{}, nil, fmt.Errorf("load mission activities: begin read transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
		return db.Mission{}, nil, fmt.Errorf("load mission activities: configure read transaction: %w", err)
	}
	queries := r.queries.WithTx(tx)
	mission, err := queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return db.Mission{}, nil, err
	}
	rows, err := queries.ListOrchestrationActivitiesAfterSequence(ctx, db.ListOrchestrationActivitiesAfterSequenceParams{
		WorkspaceID: workspaceID, MissionID: missionID, AfterSequence: afterSequence, PageSize: pageSize,
	})
	if err != nil {
		return db.Mission{}, nil, fmt.Errorf("load mission activities: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Mission{}, nil, fmt.Errorf("load mission activities: commit read transaction: %w", err)
	}
	return mission, rows, nil
}
