package orchestration

import (
	"strings"
	"testing"
)

func TestDecodePlanBudgetContract(t *testing.T) {
	t.Parallel()

	plan, err := DecodePlan([]byte(`{
		"schema_version": 1,
		"mission_id": "mission-1",
		"plan_key": "plan-1",
		"limits": {
			"max_parallel_runs": 2,
			"max_task_attempts": 2,
			"max_rework_cycles": 1,
			"budget": {
				"max_tokens": 1000,
				"max_cost_usd_ticks": 2500,
				"gate": "fail_closed"
			}
		},
		"nodes": [{
			"key": "A",
			"budget_estimate": {"tokens": 100, "cost_usd_ticks": 250}
		}]
	}`))
	if err != nil {
		t.Fatalf("DecodePlan() error = %v, want budget contract to decode", err)
	}
	if plan.Limits.Budget.MaxTokens == nil || *plan.Limits.Budget.MaxTokens != 1000 {
		t.Fatalf("decoded max_tokens = %#v, want 1000", plan.Limits.Budget.MaxTokens)
	}
	if plan.Limits.Budget.MaxCostUSDTicks == nil || *plan.Limits.Budget.MaxCostUSDTicks != 2500 {
		t.Fatalf("decoded max_cost_usd_ticks = %#v, want 2500", plan.Limits.Budget.MaxCostUSDTicks)
	}
	if string(plan.Limits.Budget.Gate) != "fail_closed" {
		t.Fatalf("decoded budget gate = %q, want fail_closed", plan.Limits.Budget.Gate)
	}
	if got := plan.Nodes[0].BudgetEstimate; got.Tokens != 100 || got.CostUSDTicks != 250 {
		t.Fatalf("decoded node budget estimate = %#v, want tokens=100 cost=250", got)
	}
}

func TestValidateBudgetPolicy(t *testing.T) {
	t.Parallel()

	positive := int64(100)
	cases := []struct {
		name    string
		budget  BudgetPolicy
		wantErr string
	}{
		{
			name:   "disabled dimensions are valid",
			budget: BudgetPolicy{MaxTokens: int64Ptr(1), Gate: "fail_closed"},
		},
		{
			name:    "zero token limit",
			budget:  BudgetPolicy{MaxTokens: int64Ptr(0), Gate: "fail_closed"},
			wantErr: "max_tokens",
		},
		{
			name:    "negative cost limit",
			budget:  BudgetPolicy{MaxCostUSDTicks: int64Ptr(-1), Gate: "fail_closed"},
			wantErr: "max_cost_usd_ticks",
		},
		{
			name:    "unknown gate",
			budget:  BudgetPolicy{MaxTokens: &positive, Gate: "ask_later"},
			wantErr: "gate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := validateBudgetPolicy(&tc.budget)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("ValidateBudgetPolicy() errors = %#v, want none", errs)
				}
				return
			}
			if !validationErrorContains(errs, tc.wantErr) {
				t.Fatalf("ValidateBudgetPolicy() errors = %#v, want a path/message containing %q", errs, tc.wantErr)
			}
		})
	}
}

func TestValidatePlanRequiresPositiveEstimateForEnabledDimensions(t *testing.T) {
	t.Parallel()

	plan := walkingSkeletonPlan()
	plan.Limits.Budget = &BudgetPolicy{
		MaxTokens:       int64Ptr(1000),
		MaxCostUSDTicks: int64Ptr(2500),
		Gate:            "fail_closed",
	}
	for index := range plan.Nodes {
		plan.Nodes[index].BudgetEstimate = BudgetEstimate{Tokens: 100, CostUSDTicks: 250}
	}

	if errs := ValidatePlan(plan, "mission-1", DefaultPlanHardLimits()); len(errs) != 0 {
		t.Fatalf("ValidatePlan() with positive estimates errors = %#v, want none", errs)
	}

	for _, tc := range []struct {
		name   string
		budget *BudgetPolicy
	}{
		{
			name:   "cost dimension disabled",
			budget: &BudgetPolicy{MaxTokens: int64Ptr(1000), Gate: BudgetGateFailClosed},
		},
		{
			name:   "token dimension disabled",
			budget: &BudgetPolicy{MaxCostUSDTicks: int64Ptr(2500), Gate: BudgetGateFailClosed},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			candidate := plan
			candidate.Limits.Budget = tc.budget
			candidate.Nodes = append([]PlanNode(nil), plan.Nodes...)
			for index := range candidate.Nodes {
				if tc.name == "cost dimension disabled" {
					candidate.Nodes[index].BudgetEstimate.CostUSDTicks = 0
				} else {
					candidate.Nodes[index].BudgetEstimate.Tokens = 0
				}
			}
			if errs := ValidatePlan(candidate, "mission-1", DefaultPlanHardLimits()); len(errs) != 0 {
				t.Fatalf("ValidatePlan() errors = %#v, want disabled dimension to allow zero estimate", errs)
			}
		})
	}

	cases := []struct {
		name string
		edit func(*Plan)
		want string
	}{
		{
			name: "enabled tokens require every node estimate",
			edit: func(plan *Plan) { plan.Nodes[1].BudgetEstimate.Tokens = 0 },
			want: "budget_estimate.tokens",
		},
		{
			name: "enabled cost requires every node estimate",
			edit: func(plan *Plan) { plan.Nodes[2].BudgetEstimate.CostUSDTicks = 0 },
			want: "budget_estimate.cost_usd_ticks",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			candidate := plan
			candidate.Nodes = append([]PlanNode(nil), plan.Nodes...)
			tc.edit(&candidate)
			if errs := ValidatePlan(candidate, "mission-1", DefaultPlanHardLimits()); !validationErrorContains(errs, tc.want) {
				t.Fatalf("ValidatePlan() errors = %#v, want a path/message containing %q", errs, tc.want)
			}
		})
	}
}

func TestEvaluateBudgetAdmissionUsesConsumedReservationsAndCandidate(t *testing.T) {
	t.Parallel()

	policy := BudgetPolicy{
		MaxTokens:       int64Ptr(100),
		MaxCostUSDTicks: int64Ptr(1000),
		Gate:            "fail_closed",
	}
	cases := []struct {
		name          string
		consumed      BudgetEstimate
		active        BudgetEstimate
		candidate     BudgetEstimate
		allowance     BudgetAllowance
		wantAllowed   bool
		wantStatus    string
		wantDimension string
		wantLimit     int64
		wantEffective int64
	}{
		{
			name:          "under both limits",
			consumed:      BudgetEstimate{Tokens: 20, CostUSDTicks: 200},
			active:        BudgetEstimate{Tokens: 30, CostUSDTicks: 300},
			candidate:     BudgetEstimate{Tokens: 40, CostUSDTicks: 400},
			wantAllowed:   true,
			wantStatus:    BudgetGateStatusNone,
			wantDimension: "",
		},
		{
			name:          "exactly at both limits",
			consumed:      BudgetEstimate{Tokens: 20, CostUSDTicks: 200},
			active:        BudgetEstimate{Tokens: 30, CostUSDTicks: 300},
			candidate:     BudgetEstimate{Tokens: 50, CostUSDTicks: 500},
			wantAllowed:   true,
			wantStatus:    BudgetGateStatusNone,
			wantDimension: "",
		},
		{
			name:          "active reservation consumes token headroom",
			consumed:      BudgetEstimate{Tokens: 40, CostUSDTicks: 100},
			active:        BudgetEstimate{Tokens: 50, CostUSDTicks: 100},
			candidate:     BudgetEstimate{Tokens: 20, CostUSDTicks: 100},
			wantAllowed:   false,
			wantStatus:    BudgetGateStatusExceeded,
			wantDimension: "tokens",
			wantLimit:     100,
			wantEffective: 110,
		},
		{
			name:          "candidate exceeds cost headroom",
			consumed:      BudgetEstimate{Tokens: 10, CostUSDTicks: 700},
			active:        BudgetEstimate{Tokens: 10, CostUSDTicks: 200},
			candidate:     BudgetEstimate{Tokens: 10, CostUSDTicks: 101},
			wantAllowed:   false,
			wantStatus:    BudgetGateStatusExceeded,
			wantDimension: "cost_usd_ticks",
			wantLimit:     1000,
			wantEffective: 1001,
		},
		{
			name:          "owner allowance extends token ceiling",
			consumed:      BudgetEstimate{Tokens: 100},
			candidate:     BudgetEstimate{Tokens: 1},
			allowance:     BudgetAllowance{GrantTokens: 1},
			wantAllowed:   true,
			wantStatus:    BudgetGateStatusApproved,
			wantDimension: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateBudgetAdmission(
				&policy,
				BudgetUsage{
					ConsumedTokens:       tc.consumed.Tokens,
					ReservedTokens:       tc.active.Tokens,
					ConsumedCostUSDTicks: tc.consumed.CostUSDTicks,
					ReservedCostUSDTicks: tc.active.CostUSDTicks,
				},
				tc.allowance,
				tc.candidate,
			)
			if got.Allowed != tc.wantAllowed {
				t.Fatalf("EvaluateBudgetAdmission() allowed = %t, want %t; decision = %#v", got.Allowed, tc.wantAllowed, got)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("EvaluateBudgetAdmission() status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Dimension != tc.wantDimension {
				t.Fatalf("EvaluateBudgetAdmission() dimension = %q, want %q", got.Dimension, tc.wantDimension)
			}
			if got.Limit != tc.wantLimit || got.Effective != tc.wantEffective {
				t.Fatalf("EvaluateBudgetAdmission() limit/effective = %d/%d, want %d/%d", got.Limit, got.Effective, tc.wantLimit, tc.wantEffective)
			}
		})
	}
}

func TestEvaluateBudgetAdmissionOwnerApprovalIsDeterministic(t *testing.T) {
	t.Parallel()

	policy := &BudgetPolicy{
		MaxTokens: int64Ptr(100),
		Gate:      BudgetGateOwnerApproval,
	}
	usage := BudgetUsage{ConsumedTokens: 80, ReservedTokens: 10}
	candidate := BudgetEstimate{Tokens: 20}
	first := EvaluateBudgetAdmission(policy, usage, BudgetAllowance{}, candidate)
	if first.Allowed {
		t.Fatalf("owner approval over-limit decision = %#v, want denied admission", first)
	}
	if first.Status != BudgetGateStatusApprovalRequired {
		t.Fatalf("owner approval gate status = %q, want approval_required", first.Status)
	}
	replay := EvaluateBudgetAdmission(policy, usage, BudgetAllowance{}, candidate)
	if replay != first {
		t.Fatalf("repeated budget assessment = %#v, first = %#v; decision must be deterministic for activity deduplication", replay, first)
	}
}

func TestAddBudgetReservationAddsDimensions(t *testing.T) {
	t.Parallel()

	usage := AddBudgetReservation(BudgetUsage{ReservedTokens: 100, ReservedCostUSDTicks: 200}, BudgetEstimate{Tokens: 25, CostUSDTicks: 50})
	usage = AddBudgetReservation(usage, BudgetEstimate{Tokens: 75, CostUSDTicks: 125})
	if usage.ReservedTokens != 200 || usage.ReservedCostUSDTicks != 375 {
		t.Fatalf("accumulated reservation = %#v, want tokens=200 cost=375", usage)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}

func validationErrorContains(errs []ValidationError, want string) bool {
	for _, err := range errs {
		if strings.Contains(err.Path, want) || strings.Contains(err.Message, want) {
			return true
		}
	}
	return false
}
