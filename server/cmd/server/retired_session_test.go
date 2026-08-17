package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// TestRetiredSessionExcludedFromIssueResume covers the case the poison filters
// structurally cannot see: a fresh-session retry that succeeds. The recovered
// task has no failure to classify, so retired_session_id is the only record of
// the abandoned provider session.
func TestRetiredSessionExcludedFromIssueResume(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	issueID, agentID, runtimeID := setupRerunTestFixture(t)
	t.Cleanup(func() { cleanupRerunFixture(t, issueID) })
	ctx := context.Background()

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, started_at, completed_at, session_id, work_dir)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '2 minutes', now() - interval '2 minutes', 'RETIRED-S', '/tmp/wd')
	`, agentID, runtimeID, issueID); err != nil {
		t.Fatalf("insert task A: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, started_at, completed_at, session_id, work_dir, retired_session_id)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '1 minute', now() - interval '1 minute', NULL, '/tmp/wd', 'RETIRED-S')
	`, agentID, runtimeID, issueID); err != nil {
		t.Fatalf("insert task B: %v", err)
	}

	queries := db.New(testPool)
	prior, err := queries.GetLastTaskSession(ctx, db.GetLastTaskSessionParams{
		AgentID: pgtype.UUID{Bytes: parseUUIDBytes(agentID), Valid: true},
		IssueID: pgtype.UUID{Bytes: parseUUIDBytes(issueID), Valid: true},
	})
	requireSessionExcluded(t, prior.SessionID, err)
}
