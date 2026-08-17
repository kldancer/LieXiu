package orchestration

import "fmt"

const (
	BudgetGateFailClosed    = "fail_closed"
	BudgetGateOwnerApproval = "owner_approval"

	BudgetGateStatusNone             = "none"
	BudgetGateStatusApproved         = "approved"
	BudgetGateStatusApprovalRequired = "approval_required"
	BudgetGateStatusExceeded         = "exceeded"
)

type BudgetUsage struct {
	ConsumedTokens       int64
	ReservedTokens       int64
	ConsumedCostUSDTicks int64
	ReservedCostUSDTicks int64
}

type BudgetAllowance struct {
	GrantTokens       int64
	GrantCostUSDTicks int64
}

type BudgetDecision struct {
	Allowed   bool
	Status    string
	Dimension string
	Limit     int64
	Consumed  int64
	Reserved  int64
	Candidate int64
	Effective int64
}

func EvaluateBudgetAdmission(policy *BudgetPolicy, usage BudgetUsage, allowance BudgetAllowance, candidate BudgetEstimate) BudgetDecision {
	if policy == nil {
		return BudgetDecision{Allowed: true, Status: BudgetGateStatusNone}
	}
	status := BudgetGateStatusNone
	if policy.MaxTokens != nil {
		limit := saturatingAdd(*policy.MaxTokens, allowance.GrantTokens)
		effective := saturatingAdd(saturatingAdd(usage.ConsumedTokens, usage.ReservedTokens), candidate.Tokens)
		if effective > limit {
			return exceededBudgetDecision(policy.Gate, "tokens", limit, usage.ConsumedTokens, usage.ReservedTokens, candidate.Tokens, effective)
		}
	}
	if policy.MaxCostUSDTicks != nil {
		limit := saturatingAdd(*policy.MaxCostUSDTicks, allowance.GrantCostUSDTicks)
		effective := saturatingAdd(saturatingAdd(usage.ConsumedCostUSDTicks, usage.ReservedCostUSDTicks), candidate.CostUSDTicks)
		if effective > limit {
			return exceededBudgetDecision(policy.Gate, "cost_usd_ticks", limit, usage.ConsumedCostUSDTicks, usage.ReservedCostUSDTicks, candidate.CostUSDTicks, effective)
		}
	}
	if allowance.GrantTokens > 0 || allowance.GrantCostUSDTicks > 0 {
		status = BudgetGateStatusApproved
	}
	return BudgetDecision{Allowed: true, Status: status}
}

func exceededBudgetDecision(gate, dimension string, limit, consumed, reserved, candidate, effective int64) BudgetDecision {
	status := BudgetGateStatusExceeded
	if gate == BudgetGateOwnerApproval {
		status = BudgetGateStatusApprovalRequired
	}
	return BudgetDecision{
		Allowed: false, Status: status, Dimension: dimension, Limit: limit,
		Consumed: consumed, Reserved: reserved, Candidate: candidate, Effective: effective,
	}
}

func AddBudgetReservation(usage BudgetUsage, estimate BudgetEstimate) BudgetUsage {
	usage.ReservedTokens = saturatingAdd(usage.ReservedTokens, estimate.Tokens)
	usage.ReservedCostUSDTicks = saturatingAdd(usage.ReservedCostUSDTicks, estimate.CostUSDTicks)
	return usage
}

func saturatingAdd(left, right int64) int64 {
	if right > 0 && left > int64(^uint64(0)>>1)-right {
		return int64(^uint64(0) >> 1)
	}
	return left + right
}

func validateBudgetPolicy(policy *BudgetPolicy) []ValidationError {
	if policy == nil {
		return nil
	}
	var errs []ValidationError
	if policy.MaxTokens == nil && policy.MaxCostUSDTicks == nil {
		errs = append(errs, ValidationError{Path: "limits.budget", Code: "missing_budget_limit", Message: "at least one budget limit is required"})
	}
	if policy.MaxTokens != nil && *policy.MaxTokens < 1 {
		errs = append(errs, ValidationError{Path: "limits.budget.max_tokens", Code: "invalid_budget_limit", Message: "max_tokens must be at least 1"})
	}
	if policy.MaxCostUSDTicks != nil && *policy.MaxCostUSDTicks < 1 {
		errs = append(errs, ValidationError{Path: "limits.budget.max_cost_usd_ticks", Code: "invalid_budget_limit", Message: "max_cost_usd_ticks must be at least 1"})
	}
	if policy.Gate != BudgetGateFailClosed && policy.Gate != BudgetGateOwnerApproval {
		errs = append(errs, ValidationError{Path: "limits.budget.gate", Code: "invalid_budget_gate", Message: fmt.Sprintf("budget gate must be %q or %q", BudgetGateFailClosed, BudgetGateOwnerApproval)})
	}
	return errs
}

func validateNodeBudgetEstimate(node PlanNode, index int, policy *BudgetPolicy) []ValidationError {
	if node.BudgetEstimate.Tokens < 0 || node.BudgetEstimate.CostUSDTicks < 0 {
		return []ValidationError{{Path: fmt.Sprintf("nodes[%d].budget_estimate", index), Code: "invalid_budget_estimate", Message: "budget estimates cannot be negative"}}
	}
	if policy == nil {
		return nil
	}
	var errs []ValidationError
	if policy.MaxTokens != nil && node.BudgetEstimate.Tokens < 1 {
		errs = append(errs, ValidationError{Path: fmt.Sprintf("nodes[%d].budget_estimate.tokens", index), Code: "missing_budget_estimate", Message: "a positive token estimate is required when max_tokens is enabled"})
	}
	if policy.MaxCostUSDTicks != nil && node.BudgetEstimate.CostUSDTicks < 1 {
		errs = append(errs, ValidationError{Path: fmt.Sprintf("nodes[%d].budget_estimate.cost_usd_ticks", index), Code: "missing_budget_estimate", Message: "a positive cost estimate is required when max_cost_usd_ticks is enabled"})
	}
	return errs
}
