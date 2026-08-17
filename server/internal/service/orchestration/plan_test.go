package orchestration

import (
	"strings"
	"testing"
)

func TestDecodePlanRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := DecodePlan([]byte(`{
		"schema_version": 1,
		"mission_id": "mission-1",
		"plan_key": "plan-1",
		"limits": {"max_parallel_runs": 2, "max_task_attempts": 2, "max_rework_cycles": 1},
		"nodes": [],
		"unexpected": true
	}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodePlan() error = %v, want unknown field rejection", err)
	}
}

func TestValidatePlanAcceptsWalkingSkeleton(t *testing.T) {
	t.Parallel()

	plan := walkingSkeletonPlan()
	if got := ValidatePlan(plan, "mission-1", DefaultPlanHardLimits()); len(got) != 0 {
		t.Fatalf("ValidatePlan() errors = %#v, want none", got)
	}
}

func TestValidatePlanRejectsInvalidGraphAndContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*Plan)
		code string
	}{
		{
			name: "mission mismatch",
			edit: func(plan *Plan) { plan.MissionID = "another-mission" },
			code: "mission_mismatch",
		},
		{
			name: "limit above hard cap",
			edit: func(plan *Plan) {
				plan.Limits.MaxParallelRuns = DefaultPlanHardLimits().MaxParallelRuns + 1
			},
			code: "limit_exceeded",
		},
		{
			name: "duplicate key",
			edit: func(plan *Plan) { plan.Nodes[1].Key = plan.Nodes[0].Key },
			code: "duplicate_node_key",
		},
		{
			name: "unknown dependency",
			edit: func(plan *Plan) { plan.Nodes[2].DependsOn = append(plan.Nodes[2].DependsOn, "missing") },
			code: "unknown_dependency",
		},
		{
			name: "self dependency",
			edit: func(plan *Plan) { plan.Nodes[0].DependsOn = []string{"A"} },
			code: "self_dependency",
		},
		{
			name: "cycle",
			edit: func(plan *Plan) { plan.Nodes[0].DependsOn = []string{"C"} },
			code: "dependency_cycle",
		},
		{
			name: "missing acceptance criteria",
			edit: func(plan *Plan) { plan.Nodes[0].AcceptanceCriteria = nil },
			code: "missing_acceptance_criteria",
		},
		{
			name: "integrator without final delivery",
			edit: func(plan *Plan) { plan.Nodes[2].ArtifactKinds = []ArtifactKind{ArtifactKindCommit} },
			code: "missing_final_delivery",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan := walkingSkeletonPlan()
			tt.edit(&plan)
			errs := ValidatePlan(plan, "mission-1", DefaultPlanHardLimits())
			if !hasValidationCode(errs, tt.code) {
				t.Fatalf("ValidatePlan() errors = %#v, want code %q", errs, tt.code)
			}
		})
	}
}

func TestValidatePlanRejectsDependencyDepthAboveCap(t *testing.T) {
	t.Parallel()

	plan := walkingSkeletonPlan()
	plan.Nodes = []PlanNode{
		planNode("A", RoleExecutor),
		planNode("B", RoleExecutor, "A"),
		planNode("C", RoleExecutor, "B"),
		planNode("D", RoleExecutor, "C"),
		planNode("E", RoleIntegrator, "D"),
		planNode("F", RoleIntegrator, "E"),
	}
	errs := ValidatePlan(plan, "mission-1", DefaultPlanHardLimits())
	if !hasValidationCode(errs, "dependency_depth_exceeded") {
		t.Fatalf("ValidatePlan() errors = %#v, want dependency_depth_exceeded", errs)
	}
}

func TestReadyNodeKeysUsesDependenciesCapacityAndStableOrder(t *testing.T) {
	t.Parallel()

	nodes := []NodeSnapshot{
		{Key: "C", Status: TaskStatusPending, Priority: 100, CreatedOrder: 2, DependencyKeys: []string{"A", "B"}},
		{Key: "B", Status: TaskStatusPending, Priority: 10, CreatedOrder: 1},
		{Key: "A", Status: TaskStatusPending, Priority: 10, CreatedOrder: 0},
	}
	got, err := ReadyNodeKeys(MissionStatusRunning, nodes, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "A,B" {
		t.Fatalf("ReadyNodeKeys() = %v, want [A B]", got)
	}

	nodes[1].Status = TaskStatusCompleted
	nodes[2].Status = TaskStatusCompleted
	got, err = ReadyNodeKeys(MissionStatusRunning, nodes, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "C" {
		t.Fatalf("ReadyNodeKeys() after prerequisites = %v, want [C]", got)
	}
}

func TestReadyNodeKeysRejectsMalformedSnapshot(t *testing.T) {
	t.Parallel()

	_, err := ReadyNodeKeys(MissionStatusRunning, []NodeSnapshot{
		{Key: "A", Status: TaskStatusPending, DependencyKeys: []string{"missing"}},
	}, 0, 1)
	if err == nil || !strings.Contains(err.Error(), "unknown dependency") {
		t.Fatalf("ReadyNodeKeys() error = %v, want unknown dependency", err)
	}
}

func walkingSkeletonPlan() Plan {
	return Plan{
		SchemaVersion: PlanSchemaVersion,
		MissionID:     "mission-1",
		PlanKey:       "plan-1",
		Limits: PlanLimits{
			MaxParallelRuns: 2,
			MaxTaskAttempts: 2,
			MaxReworkCycles: 1,
		},
		Nodes: []PlanNode{
			planNode("A", RoleExecutor),
			planNode("B", RoleExecutor),
			planNode("C", RoleIntegrator, "A", "B"),
		},
	}
}

func planNode(key string, role Role, dependencies ...string) PlanNode {
	kinds := []ArtifactKind{ArtifactKindCommit, ArtifactKindTestReceipt}
	if role == RoleIntegrator {
		kinds = []ArtifactKind{ArtifactKindFinalDelivery}
	}
	return PlanNode{
		Key:                key,
		Title:              "Task " + key,
		Description:        "Deliver task " + key,
		Role:               role,
		AcceptanceCriteria: []string{"Produces a verifiable result"},
		ArtifactKinds:      kinds,
		DependsOn:          dependencies,
	}
}

func hasValidationCode(errs []ValidationError, code string) bool {
	for _, err := range errs {
		if err.Code == code {
			return true
		}
	}
	return false
}
