package orchestration

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	DefaultProjectionActivityLimit = 50
	MaxActivityPageSize            = 100
)

type MissionProjection struct {
	Mission    MissionProjectionSummary `json:"mission"`
	Nodes      []TaskNodeProjection     `json:"nodes"`
	Team       []TeamMemberProjection   `json:"team"`
	Activities ActivityWindow           `json:"activities"`
}

type MissionProjectionSummary struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Status       MissionStatus    `json:"status"`
	CurrentPhase string           `json:"current_phase"`
	Progress     MissionProgress  `json:"progress"`
	Limits       PlanLimits       `json:"limits"`
	Budget       BudgetProjection `json:"budget"`
	Revision     int64            `json:"revision"`
	LastSequence int64            `json:"last_sequence"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

type BudgetProjection struct {
	Status               string     `json:"status"`
	Gate                 string     `json:"gate,omitempty"`
	MaxTokens            *int64     `json:"max_tokens,omitempty"`
	MaxCostUSDTicks      *int64     `json:"max_cost_usd_ticks,omitempty"`
	ConsumedTokens       int64      `json:"consumed_tokens"`
	ReservedTokens       int64      `json:"reserved_tokens"`
	ConsumedCostUSDTicks int64      `json:"consumed_cost_usd_ticks"`
	ReservedCostUSDTicks int64      `json:"reserved_cost_usd_ticks"`
	GrantTokens          int64      `json:"grant_tokens"`
	GrantCostUSDTicks    int64      `json:"grant_cost_usd_ticks"`
	ApprovedBy           string     `json:"approved_by,omitempty"`
	ApprovedAt           *time.Time `json:"approved_at,omitempty"`
}

type MissionProgress struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
	Percent   int `json:"percent"`
}

type TaskNodeProjection struct {
	ID                 string                   `json:"id"`
	Key                string                   `json:"key"`
	Title              string                   `json:"title"`
	Description        string                   `json:"description"`
	Role               Role                     `json:"role"`
	Status             TaskStatus               `json:"status"`
	DependencyIDs      []string                 `json:"dependency_ids"`
	AcceptanceCriteria json.RawMessage          `json:"acceptance_criteria"`
	ArtifactKinds      json.RawMessage          `json:"artifact_kinds"`
	BlockReason        string                   `json:"block_reason,omitempty"`
	ReworkCount        int32                    `json:"rework_count"`
	Revision           int64                    `json:"revision"`
	BudgetEstimate     BudgetEstimate           `json:"budget_estimate"`
	ActiveAssignment   *AssignmentProjection    `json:"active_assignment,omitempty"`
	LatestRun          *RunProjection           `json:"latest_run,omitempty"`
	LatestArtifact     *ArtifactProjection      `json:"latest_artifact,omitempty"`
	LatestVerdict      *ReviewVerdictProjection `json:"latest_verdict,omitempty"`
}

type AssignmentProjection struct {
	ID           string           `json:"id"`
	TaskNodeID   string           `json:"task_node_id"`
	Role         Role             `json:"role"`
	AgentID      string           `json:"agent_id"`
	RuntimeID    string           `json:"runtime_id"`
	Status       AssignmentStatus `json:"status"`
	Sequence     int32            `json:"sequence"`
	SupersedesID string           `json:"supersedes_id,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	EndedAt      *time.Time       `json:"ended_at,omitempty"`
}

type RunProjection struct {
	ID                 string          `json:"id"`
	TaskNodeID         string          `json:"task_node_id"`
	AssignmentID       string          `json:"assignment_id"`
	AgentTaskID        string          `json:"agent_task_id,omitempty"`
	Purpose            string          `json:"purpose"`
	Attempt            int32           `json:"attempt"`
	Status             RunStatus       `json:"status"`
	Input              json.RawMessage `json:"input"`
	RetryOfID          string          `json:"retry_of_id,omitempty"`
	FailureKind        string          `json:"failure_kind,omitempty"`
	FailureMessage     string          `json:"failure_message,omitempty"`
	DispatchDeadlineAt time.Time       `json:"dispatch_deadline_at"`
	TimeoutSeconds     int32           `json:"timeout_seconds"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
}

type ArtifactProjection struct {
	ID          string          `json:"id"`
	TaskNodeID  string          `json:"task_node_id"`
	RunID       string          `json:"run_id"`
	Kind        ArtifactKind    `json:"kind"`
	Version     int32           `json:"version"`
	URI         string          `json:"uri"`
	ContentHash string          `json:"content_hash,omitempty"`
	Summary     string          `json:"summary"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}

type ReviewVerdictProjection struct {
	ID               string          `json:"id"`
	TaskNodeID       string          `json:"task_node_id"`
	ReviewRunID      string          `json:"review_run_id"`
	ArtifactID       string          `json:"artifact_id"`
	Decision         ReviewDecision  `json:"decision"`
	Evidence         json.RawMessage `json:"evidence"`
	RequestedChanges json.RawMessage `json:"requested_changes"`
	CreatedAt        time.Time       `json:"created_at"`
}

type TeamMemberProjection struct {
	AgentID        string          `json:"agent_id"`
	AgentName      string          `json:"agent_name"`
	AvatarURL      string          `json:"avatar_url,omitempty"`
	Role           Role            `json:"role"`
	RuntimeID      string          `json:"runtime_id"`
	RuntimeName    string          `json:"runtime_name"`
	RuntimeStatus  string          `json:"runtime_status"`
	Provider       string          `json:"provider"`
	Model          string          `json:"model,omitempty"`
	Capabilities   json.RawMessage `json:"capabilities"`
	CurrentNodeIDs []string        `json:"current_node_ids"`
}

type ActivityProjection struct {
	ID             string          `json:"id"`
	TaskNodeID     string          `json:"task_node_id,omitempty"`
	RunID          string          `json:"run_id,omitempty"`
	Type           string          `json:"type"`
	ActorType      string          `json:"actor_type"`
	ActorID        string          `json:"actor_id,omitempty"`
	SubjectType    string          `json:"subject_type"`
	SubjectID      string          `json:"subject_id"`
	CausationID    string          `json:"causation_id"`
	CorrelationID  string          `json:"correlation_id"`
	PayloadVersion int32           `json:"payload_version"`
	Payload        json.RawMessage `json:"payload"`
	Sequence       int64           `json:"sequence"`
	OccurredAt     time.Time       `json:"occurred_at"`
}

type ActivityWindow struct {
	Items         []ActivityProjection `json:"items"`
	FirstSequence int64                `json:"first_sequence"`
	LastSequence  int64                `json:"last_sequence"`
	HasPrevious   bool                 `json:"has_previous"`
}

type ActivityPage struct {
	Items             []ActivityProjection `json:"items"`
	AfterSequence     int64                `json:"after_sequence"`
	NextAfterSequence int64                `json:"next_after_sequence"`
	LastSequence      int64                `json:"last_sequence"`
	HasMore           bool                 `json:"has_more"`
	ResetRequired     bool                 `json:"reset_required"`
}

type RunDetailProjection struct {
	MissionID  string                    `json:"mission_id"`
	Node       TaskNodeProjection        `json:"node"`
	Run        RunProjection             `json:"run"`
	Assignment AssignmentProjection      `json:"assignment"`
	Agent      *TeamMemberProjection     `json:"agent,omitempty"`
	Execution  *ExecutionProjection      `json:"execution,omitempty"`
	Messages   []TaskMessageProjection   `json:"messages"`
	Usage      []TaskUsageProjection     `json:"usage"`
	Artifacts  []ArtifactProjection      `json:"artifacts"`
	Reviews    []ReviewVerdictProjection `json:"reviews"`
	Lineage    RunLineageProjection      `json:"lineage"`
}

type ExecutionProjection struct {
	AgentTaskID string          `json:"agent_task_id"`
	Status      string          `json:"status"`
	SessionID   string          `json:"session_id,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

type TaskMessageProjection struct {
	Sequence  int32           `json:"sequence"`
	Type      string          `json:"type"`
	Tool      string          `json:"tool,omitempty"`
	Content   string          `json:"content,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    string          `json:"output,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type TaskUsageProjection struct {
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	CostUSDTicks     *int64    `json:"cost_usd_ticks,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type RunLineageProjection struct {
	Assignments []AssignmentProjection `json:"assignments"`
	Runs        []RunProjection        `json:"runs"`
}

type projectionFacts struct {
	mission      db.Mission
	budgetUsage  db.GetMissionBudgetUsageRow
	missionIssue db.Issue
	nodes        []db.TaskNode
	issues       map[string]db.Issue
	dependencies []db.IssueDependency
	assignments  []db.OrchestrationAssignment
	runs         []db.OrchestrationRun
	artifacts    []db.Artifact
	verdicts     []db.ReviewVerdict
	activities   []db.OrchestrationActivity
	agents       map[string]db.Agent
	runtimes     map[string]db.AgentRuntime
	tasks        map[string]db.AgentTaskQueue
}

func buildMissionProjection(facts projectionFacts) MissionProjection {
	lastSequence := facts.mission.NextActivitySequence - 1
	limits := PlanLimits{}
	_ = json.Unmarshal(facts.mission.Limits, &limits)

	assignmentsByNode := make(map[string][]db.OrchestrationAssignment)
	runsByNode := make(map[string][]db.OrchestrationRun)
	artifactsByNode := make(map[string][]db.Artifact)
	verdictByArtifact := make(map[string]db.ReviewVerdict)
	dependenciesByNode := make(map[string][]string)
	for _, item := range facts.assignments {
		key := uuidText(item.TaskNodeID)
		assignmentsByNode[key] = append(assignmentsByNode[key], item)
	}
	for _, item := range facts.runs {
		key := uuidText(item.TaskNodeID)
		runsByNode[key] = append(runsByNode[key], item)
	}
	for _, item := range facts.artifacts {
		key := uuidText(item.TaskNodeID)
		artifactsByNode[key] = append(artifactsByNode[key], item)
	}
	for _, item := range facts.verdicts {
		verdictByArtifact[uuidText(item.ArtifactID)] = item
	}
	for _, item := range facts.dependencies {
		key := uuidText(item.IssueID)
		dependenciesByNode[key] = append(dependenciesByNode[key], uuidText(item.DependsOnIssueID))
	}

	nodes := make([]TaskNodeProjection, 0, len(facts.nodes))
	completed := 0
	for _, node := range facts.nodes {
		id := uuidText(node.IssueID)
		issue := facts.issues[id]
		if TaskStatus(node.Status) == TaskStatusCompleted {
			completed++
		}
		projection := TaskNodeProjection{
			ID: id, Key: node.NodeKey, Title: issue.Title, Description: textOrEmpty(issue.Description),
			Role: Role(node.Role), Status: TaskStatus(node.Status), DependencyIDs: nonNilStrings(dependenciesByNode[id]),
			AcceptanceCriteria: validJSON(node.AcceptanceCriteria, `[]`), ArtifactKinds: validJSON(node.ArtifactKinds, `[]`),
			BlockReason: textOrEmpty(node.BlockReason), ReworkCount: node.ReworkCount, Revision: node.Revision,
			BudgetEstimate: budgetEstimateForNode(node),
		}
		if assignment, ok := selectActiveAssignment(node, assignmentsByNode[id]); ok {
			value := assignmentProjection(assignment)
			projection.ActiveAssignment = &value
		}
		if list := runsByNode[id]; len(list) > 0 {
			value := runProjection(list[len(list)-1], facts.tasks)
			projection.LatestRun = &value
		}
		if artifact, ok := latestArtifact(artifactsByNode[id]); ok {
			value := artifactProjection(artifact)
			projection.LatestArtifact = &value
			if verdict, ok := verdictByArtifact[uuidText(artifact.ID)]; ok {
				verdictValue := reviewVerdictProjection(verdict)
				projection.LatestVerdict = &verdictValue
			}
		}
		nodes = append(nodes, projection)
	}

	percent := 0
	if len(nodes) > 0 {
		percent = completed * 100 / len(nodes)
	}
	activities := make([]ActivityProjection, 0, len(facts.activities))
	for _, item := range facts.activities {
		activities = append(activities, activityProjection(item))
	}
	firstSequence := int64(0)
	if len(activities) > 0 {
		firstSequence = activities[0].Sequence
	}

	return MissionProjection{
		Mission: MissionProjectionSummary{
			ID: uuidText(facts.mission.IssueID), Title: facts.missionIssue.Title,
			Description: textOrEmpty(facts.missionIssue.Description), Status: MissionStatus(facts.mission.Status),
			CurrentPhase: deriveCurrentPhase(MissionStatus(facts.mission.Status), facts.nodes),
			Progress:     MissionProgress{Completed: completed, Total: len(nodes), Percent: percent},
			Limits:       limits, Budget: buildBudgetProjection(facts.mission, limits.Budget, facts.budgetUsage),
			Revision: facts.mission.Revision, LastSequence: lastSequence,
			CreatedAt: facts.mission.CreatedAt.Time, UpdatedAt: facts.mission.UpdatedAt.Time,
		},
		Nodes:      nodes,
		Team:       buildTeamProjection(facts),
		Activities: ActivityWindow{Items: activities, FirstSequence: firstSequence, LastSequence: lastSequence, HasPrevious: firstSequence > 1},
	}
}

func buildBudgetProjection(mission db.Mission, policy *BudgetPolicy, usage db.GetMissionBudgetUsageRow) BudgetProjection {
	projection := BudgetProjection{
		Status: "unlimited", ConsumedTokens: usage.ConsumedTokens, ReservedTokens: usage.ReservedTokens,
		ConsumedCostUSDTicks: usage.ConsumedCostUsdTicks, ReservedCostUSDTicks: usage.ReservedCostUsdTicks,
		GrantTokens: mission.BudgetGrantTokens, GrantCostUSDTicks: mission.BudgetGrantCostUsdTicks,
		ApprovedBy: uuidText(mission.BudgetApprovedBy), ApprovedAt: timePointer(mission.BudgetApprovedAt),
	}
	if policy == nil {
		return projection
	}
	projection.Status = "ok"
	projection.Gate = policy.Gate
	projection.MaxTokens = policy.MaxTokens
	projection.MaxCostUSDTicks = policy.MaxCostUSDTicks
	switch mission.BudgetGateStatus {
	case BudgetGateStatusApproved:
		projection.Status = "approved"
	case BudgetGateStatusApprovalRequired:
		projection.Status = "approval_required"
	case BudgetGateStatusExceeded:
		projection.Status = "budget_exceeded"
	}
	return projection
}

func latestArtifact(items []db.Artifact) (db.Artifact, bool) {
	if len(items) == 0 {
		return db.Artifact{}, false
	}
	latest := items[0]
	for _, item := range items[1:] {
		if item.CreatedAt.Time.After(latest.CreatedAt.Time) ||
			(item.CreatedAt.Time.Equal(latest.CreatedAt.Time) && uuidText(item.ID) > uuidText(latest.ID)) {
			latest = item
		}
	}
	return latest, true
}

func deriveCurrentPhase(status MissionStatus, nodes []db.TaskNode) string {
	if status == MissionStatusDraft || status == MissionStatusReady {
		return "planning"
	}
	for _, node := range nodes {
		if TaskStatus(node.Status) == TaskStatusReview {
			return "reviewing"
		}
	}
	for _, node := range nodes {
		if Role(node.Role) == RoleIntegrator && TaskStatus(node.Status) != TaskStatusPending && TaskStatus(node.Status) != TaskStatusReady {
			return "integrating"
		}
	}
	return "executing"
}

func selectActiveAssignment(node db.TaskNode, assignments []db.OrchestrationAssignment) (db.OrchestrationAssignment, bool) {
	wantedRole := Role(node.Role)
	if TaskStatus(node.Status) == TaskStatusReview {
		wantedRole = RoleReviewer
	}
	for index := len(assignments) - 1; index >= 0; index-- {
		item := assignments[index]
		if AssignmentStatus(item.Status) == AssignmentStatusActive && Role(item.Role) == wantedRole {
			return item, true
		}
	}
	return db.OrchestrationAssignment{}, false
}

func buildTeamProjection(facts projectionFacts) []TeamMemberProjection {
	team := make([]TeamMemberProjection, 0)
	indexes := make(map[string]int)
	for _, assignment := range facts.assignments {
		agentID := uuidText(assignment.AgentID)
		runtimeID := uuidText(assignment.RuntimeID)
		key := agentID + ":" + assignment.Role + ":" + runtimeID
		index, exists := indexes[key]
		if !exists {
			agent := facts.agents[agentID]
			runtime := facts.runtimes[runtimeID]
			team = append(team, TeamMemberProjection{
				AgentID: agentID, AgentName: agent.Name, AvatarURL: textOrEmpty(agent.AvatarUrl), Role: Role(assignment.Role),
				RuntimeID: runtimeID, RuntimeName: runtime.Name, RuntimeStatus: runtime.Status,
				Provider: runtime.Provider, Model: textOrEmpty(agent.Model), Capabilities: runtimeCapabilities(runtime.Metadata),
				CurrentNodeIDs: []string{},
			})
			index = len(team) - 1
			indexes[key] = index
		}
		if AssignmentStatus(assignment.Status) == AssignmentStatusActive {
			team[index].CurrentNodeIDs = appendUnique(team[index].CurrentNodeIDs, uuidText(assignment.TaskNodeID))
		}
	}
	return team
}

func assignmentProjection(item db.OrchestrationAssignment) AssignmentProjection {
	return AssignmentProjection{
		ID: uuidText(item.ID), TaskNodeID: uuidText(item.TaskNodeID), Role: Role(item.Role),
		AgentID: uuidText(item.AgentID), RuntimeID: uuidText(item.RuntimeID), Status: AssignmentStatus(item.Status),
		Sequence: item.Sequence, SupersedesID: uuidText(item.SupersedesID), CreatedAt: item.CreatedAt.Time,
		EndedAt: timePointer(item.EndedAt),
	}
}

func runProjection(item db.OrchestrationRun, tasks map[string]db.AgentTaskQueue) RunProjection {
	result := RunProjection{
		ID: uuidText(item.ID), TaskNodeID: uuidText(item.TaskNodeID), AssignmentID: uuidText(item.AssignmentID),
		Purpose: item.Purpose, Attempt: item.Attempt, Status: RunStatus(item.Status), Input: validJSON(item.Input, `{}`),
		RetryOfID: uuidText(item.RetryOfID), FailureKind: textOrEmpty(item.FailureKind), FailureMessage: textOrEmpty(item.FailureMessage),
		DispatchDeadlineAt: item.DispatchDeadlineAt.Time, TimeoutSeconds: item.TimeoutSeconds,
		StartedAt: timePointer(item.StartedAt), FinishedAt: timePointer(item.FinishedAt), CreatedAt: item.CreatedAt.Time,
	}
	if task, ok := tasks[result.ID]; ok {
		result.AgentTaskID = uuidText(task.ID)
	}
	return result
}

func artifactProjection(item db.Artifact) ArtifactProjection {
	return ArtifactProjection{
		ID: uuidText(item.ID), TaskNodeID: uuidText(item.TaskNodeID), RunID: uuidText(item.RunID),
		Kind: ArtifactKind(item.Kind), Version: item.Version, URI: item.Uri, ContentHash: textOrEmpty(item.ContentHash),
		Summary: item.Summary, Metadata: validJSON(item.Metadata, `{}`), CreatedAt: item.CreatedAt.Time,
	}
}

func reviewVerdictProjection(item db.ReviewVerdict) ReviewVerdictProjection {
	return ReviewVerdictProjection{
		ID: uuidText(item.ID), TaskNodeID: uuidText(item.TaskNodeID), ReviewRunID: uuidText(item.ReviewRunID),
		ArtifactID: uuidText(item.ArtifactID), Decision: ReviewDecision(item.Decision),
		Evidence: validJSON(item.Evidence, `{}`), RequestedChanges: validJSON(item.RequestedChanges, `[]`), CreatedAt: item.CreatedAt.Time,
	}
}

func activityProjection(item db.OrchestrationActivity) ActivityProjection {
	return ActivityProjection{
		ID: uuidText(item.ID), TaskNodeID: uuidText(item.TaskNodeID), RunID: uuidText(item.RunID), Type: item.Type,
		ActorType: item.ActorType, ActorID: uuidText(item.ActorID), SubjectType: item.SubjectType,
		SubjectID: uuidText(item.SubjectID), CausationID: uuidText(item.CausationID), CorrelationID: uuidText(item.CorrelationID),
		PayloadVersion: item.PayloadVersion, Payload: validJSON(item.Payload, `{}`), Sequence: item.Sequence, OccurredAt: item.OccurredAt.Time,
	}
}

func runtimeCapabilities(metadata []byte) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(metadata, &object) == nil {
		if value, ok := object["capabilities"]; ok && json.Valid(value) {
			return append(json.RawMessage(nil), value...)
		}
	}
	return json.RawMessage(`[]`)
}

func validJSON(value []byte, fallback string) json.RawMessage {
	if json.Valid(value) {
		return append(json.RawMessage(nil), value...)
	}
	return json.RawMessage(fallback)
}

func textOrEmpty(value pgtype.Text) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func appendUnique(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func sortProjectionFacts(facts *projectionFacts) {
	sort.SliceStable(facts.assignments, func(i, j int) bool {
		if facts.assignments[i].CreatedAt.Time.Equal(facts.assignments[j].CreatedAt.Time) {
			return uuidText(facts.assignments[i].ID) < uuidText(facts.assignments[j].ID)
		}
		return facts.assignments[i].CreatedAt.Time.Before(facts.assignments[j].CreatedAt.Time)
	})
	sort.SliceStable(facts.runs, func(i, j int) bool {
		if facts.runs[i].CreatedAt.Time.Equal(facts.runs[j].CreatedAt.Time) {
			return uuidText(facts.runs[i].ID) < uuidText(facts.runs[j].ID)
		}
		return facts.runs[i].CreatedAt.Time.Before(facts.runs[j].CreatedAt.Time)
	})
}
