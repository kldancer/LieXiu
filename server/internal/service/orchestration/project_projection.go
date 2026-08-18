package orchestration

import (
	"sort"
	"strings"
	"time"

	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

type ProjectCommandCenterProjection struct {
	Project     ProjectProjection          `json:"project"`
	GeneratedAt time.Time                  `json:"generated_at"`
	Truncated   bool                       `json:"truncated"`
	Missions    []MissionPortfolioSummary  `json:"missions"`
	Attention   []ProjectAttention         `json:"attention"`
	Capacity    ProjectCapacity            `json:"capacity"`
	Totals      ProjectCommandCenterTotals `json:"totals"`
}

type ProjectProjection struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

type MissionPortfolioSummary struct {
	ID                   string           `json:"id"`
	Title                string           `json:"title"`
	Status               MissionStatus    `json:"status"`
	CurrentPhase         string           `json:"current_phase"`
	Progress             MissionProgress  `json:"progress"`
	Budget               BudgetProjection `json:"budget"`
	Revision             int64            `json:"revision"`
	LastSequence         int64            `json:"last_sequence"`
	UpdatedAt            time.Time        `json:"updated_at"`
	PendingHumanGates    int              `json:"pending_human_gates"`
	PendingReviews       int              `json:"pending_reviews"`
	PendingPlanProposals int              `json:"pending_plan_proposals"`
	OfflineAgents        int              `json:"offline_agents"`
	ActiveRuns           int              `json:"active_runs"`
	QueuedRuns           int              `json:"queued_runs"`
}

type ProjectAttention struct {
	ID              string        `json:"id"`
	MissionID       string        `json:"mission_id"`
	Kind            string        `json:"kind"`
	Severity        string        `json:"severity"`
	SubjectType     string        `json:"subject_type"`
	SubjectID       string        `json:"subject_id"`
	TaskNodeID      string        `json:"task_node_id,omitempty"`
	RunID           string        `json:"run_id,omitempty"`
	ArtifactID      string        `json:"artifact_id,omitempty"`
	GateID          string        `json:"gate_id,omitempty"`
	MissionRevision int64         `json:"mission_revision"`
	TaskRevision    int64         `json:"task_revision,omitempty"`
	GateRevision    int64         `json:"gate_revision,omitempty"`
	Actions         []OwnerAction `json:"actions"`
}

type OwnerAction struct {
	Kind               string `json:"kind"`
	Enabled            bool   `json:"enabled"`
	RequiredPermission string `json:"required_permission"`
	Risk               string `json:"risk"`
	ReasonCode         string `json:"reason_code"`
}

type ProjectCapacity struct {
	Agents   []CapacityAgent   `json:"agents"`
	Runtimes []CapacityRuntime `json:"runtimes"`
}

type CapacityAgent struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	Duties           []Duty   `json:"duties"`
	ActiveMissionIDs []string `json:"active_mission_ids"`
	ActiveRuns       int      `json:"active_runs"`
	QueuedRuns       int      `json:"queued_runs"`
}

type CapacityRuntime struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	Duties           []Duty   `json:"duties"`
	ActiveMissionIDs []string `json:"active_mission_ids"`
	ActiveRuns       int      `json:"active_runs"`
	QueuedRuns       int      `json:"queued_runs"`
}

type ProjectCommandCenterTotals struct {
	MissionCount         int   `json:"mission_count"`
	ActiveMissions       int   `json:"active_missions"`
	BlockedMissions      int   `json:"blocked_missions"`
	CompletedMissions    int   `json:"completed_missions"`
	AttentionCount       int   `json:"attention_count"`
	ActiveRuns           int   `json:"active_runs"`
	QueuedRuns           int   `json:"queued_runs"`
	OfflineAgents        int   `json:"offline_agents"`
	PendingHumanGates    int   `json:"pending_human_gates"`
	PendingReviews       int   `json:"pending_reviews"`
	ConsumedTokens       int64 `json:"consumed_tokens"`
	ReservedTokens       int64 `json:"reserved_tokens"`
	ConsumedCostUSDTicks int64 `json:"consumed_cost_usd_ticks"`
	ReservedCostUSDTicks int64 `json:"reserved_cost_usd_ticks"`
}

// BuildProjectCommandCenterProjection is a pure projection over already-built
// MissionProjections. It does not inspect payloads or perform database work.
func BuildProjectCommandCenterProjection(project db.Project, missions []MissionProjection, generatedAt time.Time, truncated bool) ProjectCommandCenterProjection {
	result := ProjectCommandCenterProjection{
		Project:     ProjectProjection{ID: uuidText(project.ID), Title: project.Title, Status: project.Status, UpdatedAt: project.UpdatedAt.Time},
		GeneratedAt: generatedAt,
		Truncated:   truncated,
		Missions:    make([]MissionPortfolioSummary, 0, len(missions)),
		Attention:   []ProjectAttention{},
	}
	capacity := newCapacityBuilder()
	for _, mission := range missions {
		result.Missions = append(result.Missions, missionSummary(mission))
		result.Attention = append(result.Attention, missionAttention(mission, generatedAt)...)
		capacity.addMission(mission)
	}
	result.Capacity = capacity.build()
	sort.Slice(result.Missions, func(i, j int) bool { return result.Missions[i].ID < result.Missions[j].ID })
	sort.SliceStable(result.Attention, func(i, j int) bool {
		return severityRank(result.Attention[i].Severity) < severityRank(result.Attention[j].Severity) ||
			(severityRank(result.Attention[i].Severity) == severityRank(result.Attention[j].Severity) && result.Attention[i].ID < result.Attention[j].ID)
	})
	result.Totals = totals(result)
	return result
}

func missionSummary(projection MissionProjection) MissionPortfolioSummary {
	summary := MissionPortfolioSummary{ID: projection.Mission.ID, Title: projection.Mission.Title, Status: projection.Mission.Status, CurrentPhase: projection.Mission.CurrentPhase, Progress: projection.Mission.Progress, Budget: projection.Mission.Budget, Revision: projection.Mission.Revision, LastSequence: projection.Mission.LastSequence, UpdatedAt: projection.Mission.UpdatedAt}
	for _, gate := range projection.HumanGates {
		if pending(gate.Status) {
			summary.PendingHumanGates++
		}
	}
	for _, node := range projection.Nodes {
		if node.Status == TaskStatusReview {
			summary.PendingReviews++
		}
		if node.LatestRun != nil {
			if activeRun(node.LatestRun.Status) {
				summary.ActiveRuns++
			}
			if node.LatestRun.Status == RunStatusQueued {
				summary.QueuedRuns++
			}
		}
	}
	for _, member := range projection.Team {
		if !online(member.RuntimeStatus) {
			summary.OfflineAgents++
		}
	}
	for _, proposal := range projection.Planning.Proposals {
		if proposal.Decision == "pending" {
			summary.PendingPlanProposals++
		}
	}
	return summary
}

func missionAttention(projection MissionProjection, asOf time.Time) []ProjectAttention {
	mission := projection.Mission
	result := []ProjectAttention{}
	add := func(kind, severity, subjectType, subjectID string, node *TaskNodeProjection, gate *HumanGateProjection) {
		item := ProjectAttention{ID: attentionID(kind, mission.ID, subjectID), MissionID: mission.ID, Kind: kind, Severity: severity, SubjectType: subjectType, SubjectID: subjectID, MissionRevision: mission.Revision, Actions: actionsFor(kind, mission.Status, node)}
		if node != nil {
			item.TaskNodeID, item.TaskRevision = node.ID, node.Revision
			if node.LatestRun != nil {
				item.RunID = node.LatestRun.ID
			}
			if node.LatestArtifact != nil {
				item.ArtifactID = node.LatestArtifact.ID
			}
		}
		if subjectType == "run" {
			item.RunID = subjectID
		}
		if subjectType == "artifact" {
			item.ArtifactID = subjectID
		}
		if gate != nil {
			item.GateID, item.GateRevision = gate.ID, gate.Revision
			item.TaskNodeID, item.RunID, item.ArtifactID = gate.TaskNodeID, gate.SourceRunID, gate.ArtifactID
		}
		result = append(result, item)
	}
	if mission.Status == MissionStatusBlocked {
		add("mission_blocked", "critical", "mission", mission.ID, nil, nil)
	}
	if mission.Budget.Status == "approval_required" || mission.Budget.Gate == "approval_required" {
		add("budget_approval", "high", "mission", mission.ID, nil, nil)
	}
	if mission.Budget.Status == "budget_exceeded" {
		add("budget_exceeded", "critical", "mission", mission.ID, nil, nil)
	}
	for _, node := range projection.Nodes {
		if node.Status == TaskStatusReview {
			add("review_pending", "attention", "task_node", node.ID, &node, nil)
		}
		if node.LatestRun == nil {
			continue
		}
		run := node.LatestRun
		if run.Status == RunStatusFailed {
			add("run_failed", "critical", "run", run.ID, &node, nil)
		}
		if (run.Status == RunStatusQueued || run.Status == RunStatusDispatched) && !run.DispatchDeadlineAt.IsZero() && asOf.After(run.DispatchDeadlineAt) {
			add("dispatch_timeout", "high", "run", run.ID, &node, nil)
		}
		if run.Status == RunStatusRunning && run.StartedAt != nil && run.TimeoutSeconds > 0 && asOf.After(run.StartedAt.Add(time.Duration(run.TimeoutSeconds)*time.Second)) {
			add("run_timeout", "high", "run", run.ID, &node, nil)
		}
	}
	for _, proposal := range projection.Planning.Proposals {
		if proposal.Decision == "pending" {
			add("plan_proposal_pending", "attention", "artifact", proposal.ID, nil, nil)
		}
	}
	for _, gate := range projection.HumanGates {
		if pending(gate.Status) {
			var gatedNode *TaskNodeProjection
			for index := range projection.Nodes {
				if projection.Nodes[index].ID == gate.TaskNodeID {
					gatedNode = &projection.Nodes[index]
					break
				}
			}
			add("human_gate", "high", "human_gate", gate.ID, gatedNode, &gate)
		}
	}
	offlineRuntimeIDs := map[string]bool{}
	for _, member := range projection.Team {
		if !online(member.RuntimeStatus) && !offlineRuntimeIDs[member.RuntimeID] {
			add("runtime_offline", "critical", "runtime", member.RuntimeID, nil, nil)
			offlineRuntimeIDs[member.RuntimeID] = true
		}
	}
	return result
}

type capacityBuilder struct {
	agents   map[string]*CapacityAgent
	runtimes map[string]*CapacityRuntime
}

func newCapacityBuilder() capacityBuilder {
	return capacityBuilder{agents: map[string]*CapacityAgent{}, runtimes: map[string]*CapacityRuntime{}}
}
func (b *capacityBuilder) addMission(projection MissionProjection) {
	for _, member := range projection.Team {
		agent := b.agents[member.AgentID]
		if agent == nil {
			agent = &CapacityAgent{ID: member.AgentID, Name: member.AgentName, Status: member.RuntimeStatus, Duties: []Duty{}, ActiveMissionIDs: []string{}}
			b.agents[member.AgentID] = agent
		}
		runtime := b.runtimes[member.RuntimeID]
		if runtime == nil {
			runtime = &CapacityRuntime{ID: member.RuntimeID, Name: member.RuntimeName, Status: member.RuntimeStatus, Duties: []Duty{}, ActiveMissionIDs: []string{}}
			b.runtimes[member.RuntimeID] = runtime
		}
		addDuty(&agent.Duties, member.Duty)
		addDuty(&runtime.Duties, member.Duty)
		if activeMissionStatus(projection.Mission.Status) && !contains(agent.ActiveMissionIDs, projection.Mission.ID) {
			agent.ActiveMissionIDs = append(agent.ActiveMissionIDs, projection.Mission.ID)
		}
		if activeMissionStatus(projection.Mission.Status) && !contains(runtime.ActiveMissionIDs, projection.Mission.ID) {
			runtime.ActiveMissionIDs = append(runtime.ActiveMissionIDs, projection.Mission.ID)
		}
		for _, node := range projection.Nodes {
			if !containsValue(member.CurrentNodeIDs, node.ID) || node.LatestRun == nil {
				continue
			}
			if node.LatestRun.Status == RunStatusQueued {
				agent.QueuedRuns++
				runtime.QueuedRuns++
			}
			if activeRun(node.LatestRun.Status) {
				agent.ActiveRuns++
				runtime.ActiveRuns++
			}
		}
	}
}
func (b capacityBuilder) build() ProjectCapacity {
	result := ProjectCapacity{Agents: []CapacityAgent{}, Runtimes: []CapacityRuntime{}}
	for _, item := range b.agents {
		sort.Strings(item.ActiveMissionIDs)
		sort.Slice(item.Duties, func(i, j int) bool { return item.Duties[i] < item.Duties[j] })
		result.Agents = append(result.Agents, *item)
	}
	for _, item := range b.runtimes {
		sort.Strings(item.ActiveMissionIDs)
		sort.Slice(item.Duties, func(i, j int) bool { return item.Duties[i] < item.Duties[j] })
		result.Runtimes = append(result.Runtimes, *item)
	}
	sort.Slice(result.Agents, func(i, j int) bool { return result.Agents[i].ID < result.Agents[j].ID })
	sort.Slice(result.Runtimes, func(i, j int) bool { return result.Runtimes[i].ID < result.Runtimes[j].ID })
	return result
}

func totals(projection ProjectCommandCenterProjection) ProjectCommandCenterTotals {
	result := ProjectCommandCenterTotals{MissionCount: len(projection.Missions), AttentionCount: len(projection.Attention)}
	for _, mission := range projection.Missions {
		switch mission.Status {
		case MissionStatusCompleted:
			result.CompletedMissions++
		case MissionStatusBlocked:
			result.BlockedMissions++
		case MissionStatusDraft, MissionStatusReady, MissionStatusRunning:
			result.ActiveMissions++
		}
		result.ActiveRuns += mission.ActiveRuns
		result.QueuedRuns += mission.QueuedRuns
		result.PendingHumanGates += mission.PendingHumanGates
		result.PendingReviews += mission.PendingReviews
		result.ConsumedTokens += mission.Budget.ConsumedTokens
		result.ReservedTokens += mission.Budget.ReservedTokens
		result.ConsumedCostUSDTicks += mission.Budget.ConsumedCostUSDTicks
		result.ReservedCostUSDTicks += mission.Budget.ReservedCostUSDTicks
	}
	for _, agent := range projection.Capacity.Agents {
		if !online(agent.Status) {
			result.OfflineAgents++
		}
	}
	return result
}
func activeRun(status RunStatus) bool {
	return status == RunStatusDispatched || status == RunStatusRunning
}
func activeMissionStatus(status MissionStatus) bool {
	return status == MissionStatusDraft || status == MissionStatusReady || status == MissionStatusRunning || status == MissionStatusBlocked
}
func online(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "online" || status == "ready" || status == "running" || status == "active" || status == "connected"
}
func pending(status string) bool {
	return status != "" && status != "resolved" && status != "closed" && status != "cancelled" && status != "completed"
}
func addDuty(duties *[]Duty, duty Duty) {
	for _, item := range *duties {
		if item == duty {
			return
		}
	}
	*duties = append(*duties, duty)
}
func containsValue(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
func attentionID(kind, missionID, subjectID string) string {
	return kind + ":" + missionID + ":" + subjectID
}
func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "high":
		return 1
	case "attention":
		return 2
	default:
		return 3
	}
}

func actionsFor(kind string, missionStatus MissionStatus, node *TaskNodeProjection) []OwnerAction {
	actions := []OwnerAction{{Kind: "inspect", Enabled: true, RequiredPermission: "project:read", Risk: "low", ReasonCode: "projection_only"}}
	switch kind {
	case "budget_approval":
		actions = append(actions, OwnerAction{Kind: "approve_budget", Enabled: true, RequiredPermission: "mission:command", Risk: "high", ReasonCode: "owner_confirmation_required"})
	case "plan_proposal_pending":
		actions = append(actions,
			OwnerAction{Kind: "approve_plan", Enabled: true, RequiredPermission: "mission:command", Risk: "high", ReasonCode: "owner_confirmation_required"},
			OwnerAction{Kind: "reject_plan", Enabled: true, RequiredPermission: "mission:command", Risk: "high", ReasonCode: "owner_confirmation_required"},
		)
	case "run_failed", "dispatch_timeout", "run_timeout":
		actions = append(actions, OwnerAction{Kind: "retry_task", Enabled: true, RequiredPermission: "mission:command", Risk: "medium", ReasonCode: "owner_confirmation_required"})
	case "human_gate":
		actions = append(actions, OwnerAction{Kind: "resolve_human_gate", Enabled: true, RequiredPermission: "mission:command", Risk: "high", ReasonCode: "owner_confirmation_required"})
	}
	if node != nil {
		actions = append(actions, OwnerAction{Kind: "reassign_task", Enabled: false, RequiredPermission: "mission:command", Risk: "high", ReasonCode: "orchestration_reassign_not_available"})
	}
	if missionStatus != MissionStatusCompleted && missionStatus != MissionStatusCancelled && kind == "mission_blocked" {
		actions = append(actions, OwnerAction{Kind: "cancel_mission", Enabled: true, RequiredPermission: "mission:command", Risk: "high", ReasonCode: "owner_confirmation_required"})
	}
	return actions
}
