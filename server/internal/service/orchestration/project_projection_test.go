package orchestration

import (
	"encoding/json"
	"testing"
	"time"

	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestBuildProjectCommandCenterProjectionSummarizesPortfolioAndAttention(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	first := newTestUUID()
	project := db.Project{ID: first, Title: "Project", Status: "in_progress", UpdatedAt: timestamptz(now)}
	result := BuildProjectCommandCenterProjection(project, []MissionProjection{
		projectMission("mission-a", now, true),
		projectMission("mission-b", now.Add(time.Minute), false),
	}, now, true)

	if result.Project.ID != uuidText(first) || !result.Truncated || len(result.Missions) != 2 {
		t.Fatalf("unexpected project envelope: %#v", result)
	}
	if result.Missions[0].ID != "mission-a" || result.Missions[0].PendingHumanGates != 1 || result.Missions[0].PendingReviews != 1 || result.Missions[0].PendingPlanProposals != 1 {
		t.Fatalf("unexpected mission summary: %#v", result.Missions[0])
	}
	wantKinds := []string{"mission_blocked", "budget_exceeded", "run_failed", "runtime_offline", "human_gate", "dispatch_timeout", "run_timeout", "budget_approval", "review_pending", "plan_proposal_pending"}
	gotKinds := make([]string, 0, len(result.Attention))
	for _, item := range result.Attention {
		gotKinds = append(gotKinds, item.Kind)
	}
	if len(gotKinds) < len(wantKinds) {
		t.Fatalf("attention=%v, want at least %v", gotKinds, wantKinds)
	}
	for _, want := range wantKinds {
		if !containsValue(gotKinds, want) {
			t.Fatalf("attention=%v missing %q", gotKinds, want)
		}
	}
	if result.Attention[0].Severity != "critical" {
		t.Fatalf("attention not severity sorted: %#v", result.Attention)
	}
	for _, item := range result.Attention {
		for _, action := range item.Actions {
			if action.Kind == "reassign_task" && (action.Enabled || action.ReasonCode != "orchestration_reassign_not_available") {
				t.Fatalf("unsafe reassign action: %#v", action)
			}
		}
	}
	if len(result.Capacity.Agents) != 2 || len(result.Capacity.Runtimes) != 2 {
		t.Fatalf("unexpected capacity: %#v", result.Capacity)
	}
	if result.Totals.MissionCount != 2 || result.Totals.AttentionCount != len(result.Attention) {
		t.Fatalf("unexpected totals: %#v", result.Totals)
	}
	if result.Totals.OfflineAgents != 2 {
		t.Fatalf("offline agents must be counted from unique capacity entries: %#v", result.Totals)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatal(err)
	}
	if string(mustJSON(result)) == "" || string(mustJSON(result)) == "secret failure message" {
		t.Fatal("invalid json")
	}
}

func TestBuildProjectCommandCenterProjectionIsDeterministicAndDoesNotCopyPayload(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	project := db.Project{ID: newTestUUID(), Title: "Project", UpdatedAt: timestamptz(now)}
	a := projectMission("mission-z", now, true)
	b := projectMission("mission-a", now, false)
	one := BuildProjectCommandCenterProjection(project, []MissionProjection{a, b}, now, false)
	two := BuildProjectCommandCenterProjection(project, []MissionProjection{b, a}, now, false)
	left, _ := json.Marshal(one)
	right, _ := json.Marshal(two)
	if string(left) != string(right) {
		t.Fatalf("projection is not deterministic:\n%s\n%s", left, right)
	}
	if containsString(string(left), "secret failure message") || containsString(string(left), "payload-secret") {
		t.Fatalf("projection copied payload: %s", left)
	}
}

func TestBuildProjectCommandCenterProjectionDeduplicatesOfflineCapacityAndActiveMissions(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	project := db.Project{ID: newTestUUID(), Title: "Project", UpdatedAt: timestamptz(now)}
	active := projectMission("mission-active", now, false)
	completed := projectMission("mission-completed", now, false)
	completed.Mission.Status = MissionStatusCompleted
	for _, mission := range []*MissionProjection{&active, &completed} {
		mission.Team[0].AgentID = "shared-agent"
		mission.Team[0].RuntimeID = "shared-runtime"
	}

	result := BuildProjectCommandCenterProjection(project, []MissionProjection{active, completed}, now, false)
	if result.Totals.OfflineAgents != 1 {
		t.Fatalf("offline agents=%d, want one unique capacity entry", result.Totals.OfflineAgents)
	}
	if got := result.Capacity.Agents[0].ActiveMissionIDs; len(got) != 1 || got[0] != "mission-active" {
		t.Fatalf("active mission ids=%v, want only the non-terminal mission", got)
	}
}

func projectMission(id string, now time.Time, attention bool) MissionProjection {
	node := TaskNodeProjection{ID: id + "-task", Title: "Task", Status: TaskStatusReview, Revision: 4, LatestRun: &RunProjection{ID: id + "-run", Status: RunStatusFailed, DispatchDeadlineAt: now.Add(-time.Hour), TimeoutSeconds: 1, StartedAt: timePointer(timestamptz(now.Add(-time.Hour))), FailureMessage: "secret failure message"}}
	if attention {
		node.LatestRun.Status = RunStatusRunning
		node.LatestRun.StartedAt = timePointer(timestamptz(now.Add(-time.Hour)))
		node.LatestRun.FailureMessage = "payload-secret"
	}
	if attention {
		node.LatestRun.Status = RunStatusQueued
	}
	timeoutNode := TaskNodeProjection{ID: id + "-timeout-task", Status: TaskStatusRunning, Revision: 2, LatestRun: &RunProjection{ID: id + "-timeout-run", Status: RunStatusRunning, TimeoutSeconds: 1, StartedAt: timePointer(timestamptz(now.Add(-time.Hour)))}}
	gate := HumanGateProjection{ID: id + "-gate", Status: "pending", Revision: 3}
	return MissionProjection{
		Mission: MissionProjectionSummary{ID: id, Title: id, Status: map[bool]MissionStatus{true: MissionStatusBlocked, false: MissionStatusRunning}[attention], CurrentPhase: "executing", Progress: MissionProgress{Total: 1}, Budget: BudgetProjection{Status: map[bool]string{true: "budget_exceeded", false: "approval_required"}[attention]}, Revision: 7, LastSequence: 9, UpdatedAt: now},
		Nodes:   []TaskNodeProjection{node, timeoutNode}, Team: []TeamMemberProjection{{AgentID: id + "-agent", AgentName: "Agent", Duty: DutyExecutor, RuntimeID: id + "-runtime", RuntimeName: "Runtime", RuntimeStatus: "offline", CurrentNodeIDs: []string{node.ID}}},
		HumanGates: []HumanGateProjection{gate}, Planning: PlanningProjection{Proposals: []PlanProposalProjection{{ID: id + "-proposal", Decision: "pending"}}},
	}
}

func mustJSON(value any) []byte { result, _ := json.Marshal(value); return result }
func containsString(value, needle string) bool {
	return len(needle) > 0 && len(value) >= len(needle) && stringIndex(value, needle) >= 0
}
func stringIndex(value, needle string) int {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
