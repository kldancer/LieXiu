package orchestration

import (
	"testing"
	"time"

	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestBuildMissionProjectionUsesBusinessStateAndNewestArtifact(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	missionID := newTestUUID()
	nodeID := newTestUUID()
	newArtifactID := newTestUUID()
	oldArtifactID := newTestUUID()
	facts := projectionFacts{
		mission: db.Mission{
			IssueID: missionID, Status: string(MissionStatusRunning), Limits: []byte(`{"max_parallel_runs":2,"max_task_attempts":2,"max_rework_cycles":1}`),
			NextActivitySequence: 3, Revision: 4, CreatedAt: timestamptz(now.Add(-time.Hour)), UpdatedAt: timestamptz(now),
		},
		missionIssue: db.Issue{ID: missionID, Title: "Projection"},
		nodes: []db.TaskNode{{
			IssueID: nodeID, NodeKey: "A", Role: string(RoleExecutor), Status: string(TaskStatusReview),
			AcceptanceCriteria: []byte(`["accepted"]`), ArtifactKinds: []byte(`["commit","diff"]`), Revision: 2,
		}},
		issues: map[string]db.Issue{uuidText(nodeID): {ID: nodeID, Title: "Node A"}},
		artifacts: []db.Artifact{
			{ID: newArtifactID, TaskNodeID: nodeID, Kind: string(ArtifactKindCommit), Version: 2, Uri: "repo://new", Metadata: []byte(`{}`), CreatedAt: timestamptz(now)},
			{ID: oldArtifactID, TaskNodeID: nodeID, Kind: string(ArtifactKindDiff), Version: 1, Uri: "repo://old", Metadata: []byte(`{}`), CreatedAt: timestamptz(now.Add(-time.Minute))},
		},
		activities: []db.OrchestrationActivity{
			{ID: newTestUUID(), MissionID: missionID, Type: "mission.created", ActorType: "user", SubjectType: "mission", SubjectID: missionID, CausationID: newTestUUID(), CorrelationID: newTestUUID(), PayloadVersion: 1, Payload: []byte(`{}`), Sequence: 1, OccurredAt: timestamptz(now.Add(-time.Hour))},
			{ID: newTestUUID(), MissionID: missionID, TaskNodeID: nodeID, Type: "task.review_requested", ActorType: "orchestrator", SubjectType: "task_node", SubjectID: nodeID, CausationID: newTestUUID(), CorrelationID: newTestUUID(), PayloadVersion: 1, Payload: []byte(`{}`), Sequence: 2, OccurredAt: timestamptz(now)},
		},
		agents: map[string]db.Agent{}, runtimes: map[string]db.AgentRuntime{}, tasks: map[string]db.AgentTaskQueue{},
	}

	projection := buildMissionProjection(facts)
	if projection.Mission.CurrentPhase != "reviewing" || projection.Mission.LastSequence != 2 {
		t.Fatalf("unexpected mission projection: %#v", projection.Mission)
	}
	if projection.Mission.Progress != (MissionProgress{Completed: 0, Total: 1, Percent: 0}) {
		t.Fatalf("unexpected progress: %#v", projection.Mission.Progress)
	}
	if len(projection.Nodes) != 1 || projection.Nodes[0].LatestArtifact == nil || projection.Nodes[0].LatestArtifact.ID != uuidText(newArtifactID) {
		t.Fatalf("latest artifact followed query kind order instead of creation order: %#v", projection.Nodes)
	}
	if projection.Nodes[0].DependencyIDs == nil || projection.Team == nil || projection.Activities.Items == nil {
		t.Fatal("projection collections must encode as arrays, not null")
	}
}

func TestDeriveCurrentPhase(t *testing.T) {
	tests := []struct {
		name   string
		status MissionStatus
		nodes  []db.TaskNode
		want   string
	}{
		{name: "draft plans", status: MissionStatusDraft, want: "planning"},
		{name: "ready plans", status: MissionStatusReady, want: "planning"},
		{name: "ordinary work executes", status: MissionStatusRunning, nodes: []db.TaskNode{{Role: string(RoleExecutor), Status: string(TaskStatusRunning)}}, want: "executing"},
		{name: "review wins over parallel work", status: MissionStatusRunning, nodes: []db.TaskNode{{Role: string(RoleExecutor), Status: string(TaskStatusRunning)}, {Role: string(RoleExecutor), Status: string(TaskStatusReview)}}, want: "reviewing"},
		{name: "integrator work integrates", status: MissionStatusRunning, nodes: []db.TaskNode{{Role: string(RoleIntegrator), Status: string(TaskStatusRunning)}}, want: "integrating"},
		{name: "integrator review is reviewing", status: MissionStatusRunning, nodes: []db.TaskNode{{Role: string(RoleIntegrator), Status: string(TaskStatusReview)}}, want: "reviewing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := deriveCurrentPhase(test.status, test.nodes); actual != test.want {
				t.Fatalf("phase=%q, want %q", actual, test.want)
			}
		})
	}
}
