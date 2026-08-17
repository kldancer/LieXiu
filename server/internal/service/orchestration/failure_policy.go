package orchestration

import "strings"

const (
	FailureKindRuntimeOffline         = "runtime_offline"
	FailureKindDispatchTimeout        = "dispatch_timeout"
	FailureKindTimeout                = "timeout"
	FailureKindProviderNetwork        = "provider_network"
	FailureKindSkillBundleUnavailable = "skill_bundle_unavailable"
	FailureKindProtocolError          = "protocol_error"
	FailureKindWorktreeError          = "worktree_error"
	FailureKindAgentError             = "agent_error"
	FailureKindUnknown                = "unknown"
)

// FailurePolicy is the Orchestrator's pure decision for a failed Run.
// ExhaustedTaskStatus is the TaskNode status to use when no retry remains.
type FailurePolicy struct {
	FailureKind         string
	RetrySameAssignment bool
	ExhaustedTaskStatus TaskStatus
}

// EvaluateFailurePolicy normalizes a failure kind and decides whether the
// current Assignment may receive another technical Run. Attempts are
// one-based; a retry is allowed only while attempts is below maxAttempts.
func EvaluateFailurePolicy(failureKind string, attempts, maxAttempts int) FailurePolicy {
	kind := normalizePolicyFailureKind(failureKind)
	policy := FailurePolicy{
		FailureKind:         kind,
		ExhaustedTaskStatus: TaskStatusFailed,
	}

	switch kind {
	case FailureKindRuntimeOffline, FailureKindDispatchTimeout:
		policy.ExhaustedTaskStatus = TaskStatusBlocked
		policy.RetrySameAssignment = retryWithinBudget(attempts, maxAttempts)
	case FailureKindTimeout, FailureKindProviderNetwork, FailureKindSkillBundleUnavailable:
		policy.RetrySameAssignment = retryWithinBudget(attempts, maxAttempts)
	}
	return policy
}

func retryWithinBudget(attempts, maxAttempts int) bool {
	return attempts > 0 && maxAttempts > 0 && attempts < maxAttempts
}

func normalizePolicyFailureKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case FailureKindRuntimeOffline:
		return FailureKindRuntimeOffline
	case FailureKindDispatchTimeout:
		return FailureKindDispatchTimeout
	case FailureKindTimeout:
		return FailureKindTimeout
	case FailureKindProviderNetwork:
		return FailureKindProviderNetwork
	case FailureKindSkillBundleUnavailable:
		return FailureKindSkillBundleUnavailable
	case FailureKindProtocolError:
		return FailureKindProtocolError
	case FailureKindWorktreeError:
		return FailureKindWorktreeError
	case FailureKindAgentError:
		return FailureKindAgentError
	default:
		return FailureKindUnknown
	}
}
