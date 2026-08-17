package orchestration

import (
	"strings"
	"testing"
)

func TestQuickCreateMissionPlanIsDeterministicAndValid(t *testing.T) {
	missionID := newTestUUID()
	prompt := "Build a visible multi-agent project board"

	first := quickCreateMissionPlan(missionID, prompt)
	second := quickCreateMissionPlan(missionID, prompt)
	if errors := ValidatePlan(first, uuidText(missionID), DefaultPlanHardLimits()); len(errors) != 0 {
		t.Fatalf("quick-create plan validation errors: %#v", errors)
	}
	if first.PlanKey != quickCreatePlanKey || second.PlanKey != first.PlanKey {
		t.Fatalf("plan key is not deterministic: first=%q second=%q", first.PlanKey, second.PlanKey)
	}
	if len(first.Nodes) != 2 || first.Nodes[0].Role != RoleExecutor || first.Nodes[1].Role != RoleIntegrator {
		t.Fatalf("unexpected quick-create plan topology: %#v", first.Nodes)
	}
	if len(first.Nodes[1].DependsOn) != 1 || first.Nodes[1].DependsOn[0] != first.Nodes[0].Key {
		t.Fatalf("integrator does not depend on executor: %#v", first.Nodes[1].DependsOn)
	}
}

func TestQuickCreateMissionTitleUsesFirstLineAndRuneLimit(t *testing.T) {
	if got := quickCreateMissionTitle("  First line  \nsecond line"); got != "First line" {
		t.Fatalf("title = %q, want first line", got)
	}
	long := strings.Repeat("界", quickCreateTitleMaxRunes+10)
	if got := quickCreateMissionTitle(long); len([]rune(got)) != quickCreateTitleMaxRunes {
		t.Fatalf("title rune length = %d, want %d", len([]rune(got)), quickCreateTitleMaxRunes)
	}
}

func TestDerivedQuickCreateCommandIDIsPurposeScoped(t *testing.T) {
	commandID := newTestUUID()
	first := derivedQuickCreateCommandID(commandID, "submit-plan")
	replay := derivedQuickCreateCommandID(commandID, "submit-plan")
	other := derivedQuickCreateCommandID(commandID, "other")
	if first != replay {
		t.Fatalf("same command and purpose produced different derived ids")
	}
	if first == other {
		t.Fatalf("different purposes produced the same derived id")
	}
}
