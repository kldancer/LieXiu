package orchestration

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

var (
	ErrInvalidActivityCursor = errors.New("activity cursor must be non-negative")
	ErrInvalidActivityLimit  = errors.New("activity limit is out of range")
)

func (s *Service) GetMissionProjection(ctx context.Context, workspaceID, missionID pgtype.UUID) (MissionProjection, error) {
	if s == nil || s.queries == nil || s.repository == nil || !validUUID(workspaceID) || !validUUID(missionID) {
		return MissionProjection{}, fmt.Errorf("get mission projection: invalid scope")
	}
	facts, err := s.repository.loadProjectionFacts(ctx, workspaceID, missionID, DefaultProjectionActivityLimit)
	if err != nil {
		return MissionProjection{}, err
	}
	return buildMissionProjection(facts), nil
}

func (s *Service) ListMissionActivities(ctx context.Context, workspaceID, missionID pgtype.UUID, afterSequence int64, limit int32) (ActivityPage, error) {
	if s == nil || s.queries == nil || s.repository == nil || !validUUID(workspaceID) || !validUUID(missionID) {
		return ActivityPage{}, fmt.Errorf("list mission activities: invalid scope")
	}
	if afterSequence < 0 {
		return ActivityPage{}, ErrInvalidActivityCursor
	}
	if limit == 0 {
		limit = DefaultProjectionActivityLimit
	}
	if limit < 1 || limit > MaxActivityPageSize {
		return ActivityPage{}, ErrInvalidActivityLimit
	}
	mission, rows, err := s.repository.loadActivityPageFacts(ctx, workspaceID, missionID, afterSequence, limit+1)
	if err != nil {
		return ActivityPage{}, err
	}
	lastSequence := mission.NextActivitySequence - 1
	page := ActivityPage{
		Items: []ActivityProjection{}, AfterSequence: afterSequence, NextAfterSequence: afterSequence, LastSequence: lastSequence,
	}
	if afterSequence > lastSequence {
		page.ResetRequired = true
		return page, nil
	}
	expected := afterSequence + 1
	for _, row := range rows {
		if row.Sequence != expected {
			page.ResetRequired = true
			return page, nil
		}
		expected++
	}
	if len(rows) == 0 && afterSequence < lastSequence {
		page.ResetRequired = true
		return page, nil
	}
	if len(rows) > int(limit) {
		page.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, activityProjection(row))
	}
	if len(page.Items) > 0 {
		page.NextAfterSequence = page.Items[len(page.Items)-1].Sequence
	}
	return page, nil
}

func (s *Service) GetRunDetail(ctx context.Context, workspaceID, missionID, runID pgtype.UUID) (RunDetailProjection, error) {
	if s == nil || s.queries == nil || s.repository == nil || !validUUID(workspaceID) || !validUUID(missionID) || !validUUID(runID) {
		return RunDetailProjection{}, fmt.Errorf("get run detail: invalid scope")
	}
	facts, err := s.repository.loadProjectionFacts(ctx, workspaceID, missionID, 0)
	if err != nil {
		return RunDetailProjection{}, err
	}
	var selected db.OrchestrationRun
	found := false
	for _, run := range facts.runs {
		if run.ID == runID {
			selected = run
			found = true
			break
		}
	}
	if !found {
		return RunDetailProjection{}, pgx.ErrNoRows
	}
	projection := buildMissionProjection(facts)
	detail := RunDetailProjection{
		MissionID: uuidText(missionID), Messages: []TaskMessageProjection{}, Usage: []TaskUsageProjection{},
		Artifacts: []ArtifactProjection{}, Reviews: []ReviewVerdictProjection{},
		Lineage: RunLineageProjection{Assignments: []AssignmentProjection{}, Runs: []RunProjection{}},
	}
	nodeFound := false
	for _, node := range projection.Nodes {
		if node.ID == uuidText(selected.TaskNodeID) {
			detail.Node = node
			nodeFound = true
			break
		}
	}
	if !nodeFound {
		return RunDetailProjection{}, fmt.Errorf("get run detail: task node is missing")
	}
	detail.Run = runProjection(selected, facts.tasks)
	assignmentFound := false
	for _, assignment := range facts.assignments {
		if assignment.ID == selected.AssignmentID {
			detail.Assignment = assignmentProjection(assignment)
			assignmentFound = true
		}
		if assignment.TaskNodeID == selected.TaskNodeID {
			detail.Lineage.Assignments = append(detail.Lineage.Assignments, assignmentProjection(assignment))
		}
	}
	if !assignmentFound {
		return RunDetailProjection{}, fmt.Errorf("get run detail: assignment is missing")
	}
	for _, run := range facts.runs {
		if run.TaskNodeID == selected.TaskNodeID {
			detail.Lineage.Runs = append(detail.Lineage.Runs, runProjection(run, facts.tasks))
		}
	}
	for _, artifact := range facts.artifacts {
		if artifact.TaskNodeID == selected.TaskNodeID {
			detail.Artifacts = append(detail.Artifacts, artifactProjection(artifact))
		}
	}
	for _, verdict := range facts.verdicts {
		if verdict.TaskNodeID == selected.TaskNodeID {
			detail.Reviews = append(detail.Reviews, reviewVerdictProjection(verdict))
		}
	}
	for _, member := range projection.Team {
		if member.AgentID == detail.Assignment.AgentID && member.Role == detail.Assignment.Role && member.RuntimeID == detail.Assignment.RuntimeID {
			value := member
			detail.Agent = &value
			break
		}
	}
	if task, ok := facts.tasks[uuidText(runID)]; ok {
		detail.Execution = &ExecutionProjection{
			AgentTaskID: uuidText(task.ID), Status: task.Status, SessionID: textOrEmpty(task.SessionID),
			Result: validJSON(task.Result, `null`), Error: textOrEmpty(task.Error), CreatedAt: task.CreatedAt.Time,
			StartedAt: timePointer(task.StartedAt), CompletedAt: timePointer(task.CompletedAt),
		}
		messages, messageErr := s.queries.ListTaskMessages(ctx, task.ID)
		if messageErr != nil {
			return RunDetailProjection{}, fmt.Errorf("get run detail: load messages: %w", messageErr)
		}
		for _, message := range messages {
			detail.Messages = append(detail.Messages, TaskMessageProjection{
				Sequence: message.Seq, Type: message.Type, Tool: textOrEmpty(message.Tool), Content: textOrEmpty(message.Content),
				Input: validJSON(message.Input, `null`), Output: textOrEmpty(message.Output), CreatedAt: message.CreatedAt.Time,
			})
		}
		usage, usageErr := s.queries.GetTaskUsage(ctx, task.ID)
		if usageErr != nil {
			return RunDetailProjection{}, fmt.Errorf("get run detail: load usage: %w", usageErr)
		}
		for _, item := range usage {
			var costUSDTicks *int64
			if item.CostUsdTicks.Valid {
				value := item.CostUsdTicks.Int64
				costUSDTicks = &value
			}
			detail.Usage = append(detail.Usage, TaskUsageProjection{
				Provider: item.Provider, Model: item.Model, InputTokens: item.InputTokens, OutputTokens: item.OutputTokens,
				CacheReadTokens: item.CacheReadTokens, CacheWriteTokens: item.CacheWriteTokens,
				CostUSDTicks: costUSDTicks, CreatedAt: item.CreatedAt.Time,
			})
		}
	}
	return detail, nil
}
