package orchestration

import "testing"

func TestDutyIsClosedOverFourStateMachineResponsibilities(t *testing.T) {
	for _, duty := range []Duty{DutyPlanner, DutyExecutor, DutyReviewer, DutyIntegrator} {
		if !duty.Valid() {
			t.Fatalf("fixed duty %q is invalid", duty)
		}
	}
	if Duty("security_reviewer").Valid() {
		t.Fatal("custom role name became a state-machine duty")
	}
	if DutyPlanner.TaskNodeDuty() || DutyReviewer.TaskNodeDuty() {
		t.Fatal("planner/reviewer must not become plan TaskNode duties")
	}
	if !DutyExecutor.TaskNodeDuty() || !DutyIntegrator.TaskNodeDuty() {
		t.Fatal("executor/integrator must remain plan TaskNode duties")
	}
}

func TestNormalizeAndValidateRoleProfileConfig(t *testing.T) {
	maxTokens := int64(10_000)
	runtimeA := "11111111-1111-4111-8111-111111111111"
	runtimeB := "22222222-2222-4222-8222-222222222222"
	config, errs := normalizeAndValidateRoleProfileConfig(RoleProfileConfig{
		Instructions:         "  Review Go changes  ",
		RequiredCapabilities: []string{"go", "security", "go"},
		Runtime: RoleRuntimePreferences{
			AllowedRuntimeIDs:   []string{runtimeB, runtimeA},
			PreferredRuntimeIDs: []string{runtimeB, runtimeA, runtimeB},
			Providers:           []string{"openai"},
			Models:              []string{"gpt-5.6"},
		},
		Tools: RoleToolPermissions{
			AllowedTools: []string{"go", "rg"},
			AllowedPaths: []string{"server/"},
		},
		Budget:         RoleBudgetLimits{MaxTokens: &maxTokens, MaxReworkCycles: 2, MaxTechnicalRetries: 1},
		TimeoutSeconds: 1800,
		MaxConcurrency: 2,
	})
	if len(errs) > 0 {
		t.Fatalf("valid config rejected: %#v", errs)
	}
	if config.Instructions != "Review Go changes" {
		t.Fatalf("instructions were not normalized: %q", config.Instructions)
	}
	if len(config.RequiredCapabilities) != 2 || config.RequiredCapabilities[0] != "go" || config.RequiredCapabilities[1] != "security" {
		t.Fatalf("capabilities are not canonical: %#v", config.RequiredCapabilities)
	}
	if config.Runtime.AllowedRuntimeIDs[0] != runtimeA {
		t.Fatalf("runtime allow-list is not sorted: %#v", config.Runtime.AllowedRuntimeIDs)
	}
	if len(config.Runtime.PreferredRuntimeIDs) != 2 || config.Runtime.PreferredRuntimeIDs[0] != runtimeB || config.Runtime.PreferredRuntimeIDs[1] != runtimeA {
		t.Fatalf("runtime preference rank was not preserved: %#v", config.Runtime.PreferredRuntimeIDs)
	}
}

func TestRoleProfileRejectsCustomDutyAndPolicyEscapes(t *testing.T) {
	if errs := validateRoleProfileIdentity("security-reviewer", Duty("security_reviewer"), "Security reviewer", ""); !hasValidationCode(errs, "invalid_duty") {
		t.Fatalf("custom duty was not rejected: %#v", errs)
	}
	negative := int64(-1)
	_, errs := normalizeAndValidateRoleProfileConfig(RoleProfileConfig{
		Runtime: RoleRuntimePreferences{
			AllowedRuntimeIDs:   []string{"11111111-1111-4111-8111-111111111111"},
			PreferredRuntimeIDs: []string{"22222222-2222-4222-8222-222222222222"},
		},
		Budget:         RoleBudgetLimits{MaxTokens: &negative, MaxReworkCycles: -1},
		TimeoutSeconds: 0,
		MaxConcurrency: 0,
	})
	for _, code := range []string{"preferred_runtime_not_allowed", "invalid_timeout", "invalid_concurrency", "invalid_budget", "invalid_retry_budget"} {
		if !hasValidationCode(errs, code) {
			t.Errorf("missing validation code %q in %#v", code, errs)
		}
	}
}
