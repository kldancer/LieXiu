package handler

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

func orchestrationClaimUUID(s string) pgtype.UUID { return parseUUID(s) }

func orchestrationClaimFixture(purpose string) (db.AgentTaskQueue, db.OrchestrationRun, *db.TaskNode, pgtype.UUID) {
	workspaceID := orchestrationClaimUUID("11111111-1111-1111-1111-111111111111")
	missionID := orchestrationClaimUUID("22222222-2222-2222-2222-222222222222")
	nodeID := orchestrationClaimUUID("33333333-3333-3333-3333-333333333333")
	runID := orchestrationClaimUUID("44444444-4444-4444-4444-444444444444")
	task := db.AgentTaskQueue{ID: orchestrationClaimUUID("55555555-5555-5555-5555-555555555555"), IssueID: nodeID, OrchestrationRunID: runID, Context: []byte(`{"frozen":true}`)}
	run := db.OrchestrationRun{ID: runID, WorkspaceID: workspaceID, MissionID: missionID, TaskNodeID: nodeID, Purpose: purpose, Input: []byte(`{"frozen":true}`)}
	node := &db.TaskNode{IssueID: nodeID, WorkspaceID: workspaceID, MissionID: missionID, ArtifactKinds: []byte(`["commit","test_receipt"]`)}
	if purpose == "plan" {
		task.IssueID = missionID
		run.TaskNodeID = pgtype.UUID{}
		node = nil
	}
	return task, run, node, workspaceID
}

func TestAssembleOrchestrationRunClaimContextCapabilityFailClosed(t *testing.T) {
	task, run, node, workspaceID := orchestrationClaimFixture("execute")
	_, err := assembleOrchestrationRunClaimContext(task, run, node, workspaceID, false)
	if err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("error = %v, want capability rejection", err)
	}
}

func TestAssembleOrchestrationRunClaimContextSuccess(t *testing.T) {
	task, run, node, workspaceID := orchestrationClaimFixture("execute")
	got, err := assembleOrchestrationRunClaimContext(task, run, node, workspaceID, true)
	if err != nil {
		t.Fatalf("assemble context: %v", err)
	}
	if got.SchemaVersion != 1 || got.RunID != "44444444-4444-4444-4444-444444444444" || got.ResultContract.Kind != protocol.OrchestrationResultKindArtifact {
		t.Fatalf("context = %#v", got)
	}
	if len(got.ResultContract.AllowedArtifactKinds) != 2 || got.ResultContract.AllowedArtifactKinds[0] != "commit" {
		t.Fatalf("allowed kinds = %#v", got.ResultContract.AllowedArtifactKinds)
	}
}

func TestAssembleOrchestrationRunClaimContextRejectsInputMismatch(t *testing.T) {
	task, run, node, workspaceID := orchestrationClaimFixture("review")
	task.Context = []byte(`{"frozen":false}`)
	_, err := assembleOrchestrationRunClaimContext(task, run, node, workspaceID, true)
	if err == nil || !strings.Contains(err.Error(), "input") {
		t.Fatalf("error = %v, want input mismatch", err)
	}
}

func TestAssembleOrchestrationRunClaimContextRejectsNodeMappingAndKinds(t *testing.T) {
	task, run, node, workspaceID := orchestrationClaimFixture("integrate")
	node.IssueID = orchestrationClaimUUID("66666666-6666-6666-6666-666666666666")
	if _, err := assembleOrchestrationRunClaimContext(task, run, node, workspaceID, true); err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("mapping error = %v", err)
	}

	_, run, node, workspaceID = orchestrationClaimFixture("execute")
	node.ArtifactKinds = []byte(`[]`)
	if _, err := assembleOrchestrationRunClaimContext(task, run, node, workspaceID, true); err == nil || !strings.Contains(err.Error(), "artifact kinds") {
		t.Fatalf("artifact kinds error = %v", err)
	}
}

func TestFrozenRoleInstructionsAndPurposeDuty(t *testing.T) {
	got, err := frozenRoleInstructions([]byte(`{"instructions":"  approve valid evidence  ","max_concurrency":1}`))
	if err != nil || got != "approve valid evidence" {
		t.Fatalf("frozen instructions = %q, err=%v", got, err)
	}
	if _, err := frozenRoleInstructions(nil); err == nil {
		t.Fatal("empty frozen role config unexpectedly accepted")
	}
	for purpose, want := range map[string]string{"plan": "planner", "execute": "executor", "review": "reviewer", "integrate": "integrator"} {
		got, err := orchestrationDutyForPurpose(purpose)
		if err != nil || got != want {
			t.Fatalf("purpose %s duty=%q err=%v, want %q", purpose, got, err, want)
		}
	}
}
