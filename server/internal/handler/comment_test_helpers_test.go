package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func completeTaskViaHandler(t *testing.T, taskID, output string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+taskID+"/complete", map[string]any{"output": output}, testWorkspaceID, "legit-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	testHandler.CompleteTask(w, req)
	return w
}

func pendingTaskCountForAgentIssue(t *testing.T, issueID, agentID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status IN ('queued', 'dispatched')`, issueID, agentID).Scan(&n); err != nil {
		t.Fatalf("count pending tasks: %v", err)
	}
	return n
}

func queuedTaskCountForAgentIssue(t *testing.T, issueID, agentID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`, issueID, agentID).Scan(&n); err != nil {
		t.Fatalf("count queued tasks: %v", err)
	}
	return n
}

func taskTriggerOriginatorCoalesced(t *testing.T, issueID, agentID string) (string, string, []string) {
	t.Helper()
	var trigger, originator string
	var coalesced []string
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(trigger_comment_id::text, ''), COALESCE(originator_user_id::text, ''), coalesced_comment_ids::text[]
		FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 ORDER BY created_at DESC LIMIT 1
	`, issueID, agentID).Scan(&trigger, &originator, &coalesced); err != nil {
		t.Fatalf("read task trigger/originator/coalesced: %v", err)
	}
	return trigger, originator, coalesced
}
