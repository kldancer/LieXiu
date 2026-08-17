package orchestration

import "testing"

func TestEvaluateFailurePolicy(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		attempts   int
		max        int
		wantKind   string
		wantRetry  bool
		wantStatus TaskStatus
	}{
		{name: "runtime offline retry", input: " runtime_offline ", attempts: 1, max: 2, wantKind: FailureKindRuntimeOffline, wantRetry: true, wantStatus: TaskStatusBlocked},
		{name: "dispatch timeout exhausted", input: "DISPATCH_TIMEOUT", attempts: 2, max: 2, wantKind: FailureKindDispatchTimeout, wantStatus: TaskStatusBlocked},
		{name: "timeout retry", input: " Timeout ", attempts: 1, max: 2, wantKind: FailureKindTimeout, wantRetry: true, wantStatus: TaskStatusFailed},
		{name: "timeout exhausted", input: "timeout", attempts: 2, max: 2, wantKind: FailureKindTimeout, wantStatus: TaskStatusFailed},
		{name: "provider network retry", input: "provider_network", attempts: 1, max: 2, wantKind: FailureKindProviderNetwork, wantRetry: true, wantStatus: TaskStatusFailed},
		{name: "provider network exhausted", input: "provider_network", attempts: 2, max: 2, wantKind: FailureKindProviderNetwork, wantStatus: TaskStatusFailed},
		{name: "skill bundle retry", input: "skill_bundle_unavailable", attempts: 1, max: 2, wantKind: FailureKindSkillBundleUnavailable, wantRetry: true, wantStatus: TaskStatusFailed},
		{name: "skill bundle exhausted", input: "skill_bundle_unavailable", attempts: 2, max: 2, wantKind: FailureKindSkillBundleUnavailable, wantStatus: TaskStatusFailed},
		{name: "protocol error", input: "protocol_error", attempts: 1, max: 2, wantKind: FailureKindProtocolError, wantStatus: TaskStatusFailed},
		{name: "worktree error", input: "worktree_error", attempts: 1, max: 2, wantKind: FailureKindWorktreeError, wantStatus: TaskStatusFailed},
		{name: "agent error", input: "agent_error", attempts: 1, max: 2, wantKind: FailureKindAgentError, wantStatus: TaskStatusFailed},
		{name: "unknown value fails closed", input: "new_failure_kind", attempts: 1, max: 2, wantKind: FailureKindUnknown, wantStatus: TaskStatusFailed},
		{name: "blank value fails closed", input: " \t\n ", attempts: 1, max: 2, wantKind: FailureKindUnknown, wantStatus: TaskStatusFailed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := EvaluateFailurePolicy(test.input, test.attempts, test.max)
			if got.FailureKind != test.wantKind || got.RetrySameAssignment != test.wantRetry || got.ExhaustedTaskStatus != test.wantStatus {
				t.Fatalf("EvaluateFailurePolicy(%q, %d, %d) = %#v, want kind=%q retry=%v status=%q", test.input, test.attempts, test.max, got, test.wantKind, test.wantRetry, test.wantStatus)
			}
		})
	}
}

func TestEvaluateFailurePolicyBudgetAndFailClosed(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		max      int
		want     bool
	}{
		{name: "zero attempt fails closed", attempts: 0, max: 1},
		{name: "at budget", attempts: 1, max: 1},
		{name: "over budget", attempts: 2, max: 1},
		{name: "zero budget", attempts: 0, max: 0},
		{name: "negative attempt", attempts: -1, max: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := EvaluateFailurePolicy(FailureKindProviderNetwork, test.attempts, test.max)
			if got.RetrySameAssignment != test.want {
				t.Fatalf("retry = %v, want %v", got.RetrySameAssignment, test.want)
			}
		})
	}
}

func TestEvaluateFailurePolicyDoesNotTreatReviewReworkAsRetry(t *testing.T) {
	got := EvaluateFailurePolicy("review_rework", 1, 2)
	if got.FailureKind != FailureKindUnknown || got.RetrySameAssignment || got.ExhaustedTaskStatus != TaskStatusFailed {
		t.Fatalf("review rework must fail closed: %#v", got)
	}
}
