package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// taskStatus reads a task's current status straight from the DB so reject
// paths can assert that authorization caused no mutation.
func taskStatus(t *testing.T, taskID string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	return status
}

func cancelTaskByUserRequest(t *testing.T, userID, taskID string) *http.Request {
	t.Helper()
	req := newRequestAs(userID, http.MethodPost, "/api/tasks/"+taskID+"/cancel", nil)
	req = withURLParam(req, "taskId", taskID)
	return withChatTestWorkspaceCtx(t, req)
}

func TestCancelTaskByUser_QuickCreate_Succeeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelQuickCreateAgent", []byte("[]"))
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, context)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'running', 0, NULL,
		        '{"type":"quick_create","workspace_id":"ws","prompt":"do a thing"}'::jsonb)
		RETURNING id
	`, agentID).Scan(&taskID); err != nil {
		t.Fatalf("create quick_create task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, taskID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "cancelled" {
		t.Fatalf("task not cancelled: status = %q", got)
	}
}

func TestCancelTaskByUser_IssueTask_Succeeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelIssueTaskAgent", []byte("[]"))
	var issueID, taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'cancel-byid-issue', 'todo', 'medium', $2, 'member', 92001, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'queued', 0, $2)
		RETURNING id
	`, agentID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create issue task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, taskID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "cancelled" {
		t.Fatalf("task not cancelled: status = %q", got)
	}
}

func TestCancelTaskByUser_PrivateAgent_PlainMember_Returns403(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, _, memberID := privateAgentTestFixture(t)
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'queued', 0)
		RETURNING id
	`, agentID).Scan(&taskID); err != nil {
		t.Fatalf("create private-agent task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, memberID, taskID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "queued" {
		t.Fatalf("task was mutated: status = %q", got)
	}
}
