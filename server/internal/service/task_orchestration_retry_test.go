package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestOrchestrationAgentTaskNeverUsesLegacyAutoRetryPolicy(t *testing.T) {
	task := db.AgentTaskQueue{
		Attempt:            1,
		MaxAttempts:        5,
		IssueID:            pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		OrchestrationRunID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
	}
	for _, reason := range []string{
		"runtime_offline",
		"timeout",
		"codex_semantic_inactivity",
		"agent_error.provider_network",
		"skill_bundle_unavailable",
	} {
		if retryEligible(reason, task) {
			t.Errorf("orchestration AgentTask is legacy-retry eligible for %q", reason)
		}
	}

	// The same execution-plane fact remains eligible for a legacy Issue task;
	// W1B.4 moves only orchestration policy now and deletes the remaining
	// legacy producers in their own migration waves.
	task.OrchestrationRunID = pgtype.UUID{}
	if !retryEligible("timeout", task) {
		t.Fatal("legacy Issue task unexpectedly lost its compatibility retry")
	}
}
