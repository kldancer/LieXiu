package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createCommentTriggerPreviewIssue(t *testing.T, title string, assigneeType, assigneeID string) string {
	t.Helper()
	ctx := context.Background()

	var number int
	if err := testPool.QueryRow(ctx, `
		UPDATE workspace
		SET issue_counter = GREATEST(issue_counter, (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)) + 1
		WHERE id = $1 RETURNING issue_counter
	`, testWorkspaceID).Scan(&number); err != nil {
		t.Fatalf("next issue number: %v", err)
	}

	var assigneeTypeArg any
	var assigneeIDArg any
	if assigneeType != "" {
		assigneeTypeArg = assigneeType
	}
	if assigneeID != "" {
		assigneeIDArg = assigneeID
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_type, creator_id, title, assignee_type, assignee_id, number)
		VALUES ($1, 'member', $2, $3, $4, $5, $6)
		RETURNING id
	`, testWorkspaceID, testUserID, title, assigneeTypeArg, assigneeIDArg, number).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	return issueID
}

func previewCommentTriggersForTest(t *testing.T, issueID string, body any) CommentTriggerPreviewResponse {
	t.Helper()

	w := httptest.NewRecorder()
	r := newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments/trigger-preview", body)
	r = withURLParam(r, "id", issueID)
	testHandler.PreviewCommentTriggers(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("PreviewCommentTriggers: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp CommentTriggerPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	return resp
}

func postCommentForTriggerPreviewTest(t *testing.T, issueID string, body map[string]any) string {
	t.Helper()

	w := httptest.NewRecorder()
	r := newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", body)
	r = withURLParam(r, "id", issueID)
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode created comment: %v", err)
	}
	return resp.ID
}

func insertMemberRootCommentForTriggerPreviewTest(t *testing.T, issueID, content string) string {
	t.Helper()

	var commentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, $4)
		RETURNING id
	`, testWorkspaceID, issueID, testUserID, content).Scan(&commentID); err != nil {
		t.Fatalf("insert member root comment: %v", err)
	}
	return commentID
}

func updateCommentForTriggerPreviewTest(t *testing.T, commentID string, body map[string]any) {
	t.Helper()

	w := httptest.NewRecorder()
	r := newRequest(http.MethodPut, "/api/comments/"+commentID, body)
	r = withURLParam(r, "commentId", commentID)
	testHandler.UpdateComment(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateComment: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func countQueuedCommentTriggerTasks(t *testing.T, issueID, agentID string) int {
	t.Helper()

	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue
		WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'
	`, issueID, agentID).Scan(&n); err != nil {
		t.Fatalf("count queued tasks: %v", err)
	}
	return n
}

func countCommentTriggerTasksWithStatus(t *testing.T, issueID, agentID, status string) int {
	t.Helper()

	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue
		WHERE issue_id = $1 AND agent_id = $2 AND status = $3
	`, issueID, agentID, status).Scan(&n); err != nil {
		t.Fatalf("count %s tasks: %v", status, err)
	}
	return n
}

func requirePreviewAgents(t *testing.T, preview CommentTriggerPreviewResponse, wantIDs ...string) {
	t.Helper()
	if len(preview.Agents) != len(wantIDs) {
		t.Fatalf("preview agents = %+v, want ids %v", preview.Agents, wantIDs)
	}
	got := make(map[string]struct{}, len(preview.Agents))
	for _, agent := range preview.Agents {
		got[agent.ID] = struct{}{}
	}
	for _, want := range wantIDs {
		if _, ok := got[want]; !ok {
			t.Fatalf("preview agents = %+v, missing id %s", preview.Agents, want)
		}
	}
}

func TestPreviewCommentTriggers_NoteReturnsNoAgents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Preview Note Agent", nil)
	issueID := createCommentTriggerPreviewIssue(t, "comment trigger note", "agent", agentID)
	content := fmt.Sprintf("/note [@Agent](mention://agent/%s) human-only context", agentID)

	preview := previewCommentTriggersForTest(t, issueID, map[string]any{"content": content})
	if got := len(preview.Agents); got != 0 {
		t.Fatalf("note preview agents = %d, want 0: %+v", got, preview.Agents)
	}
}

func TestCreateComment_NoteMentionDoesNotQueueAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Create Note Agent", nil)
	issueID := createCommentTriggerPreviewIssue(t, "comment trigger create note", "agent", agentID)
	content := fmt.Sprintf("/note [@Agent](mention://agent/%s) human-only context", agentID)

	postCommentForTriggerPreviewTest(t, issueID, map[string]any{"content": content})

	if got := countQueuedCommentTriggerTasks(t, issueID, agentID); got != 0 {
		t.Fatalf("note create queued tasks = %d, want 0", got)
	}
}

// TestPreviewCommentTriggers_MalformedMentionIDDoesNotPanic pins the review
// finding on PR #6048: MentionRe accepts any `[0-9a-fA-F-]+` id, so
// `mention://agent/-` parses as a real mention. The resolver used to hand that
// straight to the panicking parseUUID (util.MustParseUUID), turning attacker-
// controlled comment text into a 500 — and on the create path the comment row
// was already committed before the panic. Malformed ids must be reported as
// blocked mentions, never as an error response.
//
// The reason is target_unavailable for malformed agent mentions (MUL-5548): a
// string that is not a UUID cannot name an entity in any workspace, so it
// conceals no existence and must not be blamed on invoke permission. This is
// deliberately NOT the well-formed-but-unresolved case,
// which stays invocation_not_allowed so a blocked reason can never confirm a
// private agent — that boundary is pinned by
// TestCreateComment_BlockedMentionReasonDoesNotEnumeratePrivateAgent.
func TestPreviewCommentTriggers_MalformedMentionIDDoesNotPanic(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	assigneeID := createHandlerTestAgent(t, "Preview Malformed Assignee", nil)
	issueID := createCommentTriggerPreviewIssue(t, "comment trigger malformed mention id", "agent", assigneeID)

	cases := []struct {
		name       string
		content    string
		targetType string
		targetID   string
		reason     DispatchReasonCode
	}{
		{
			name:       "bare dash agent id",
			content:    "[@Broken](mention://agent/-) please look",
			targetType: "agent",
			targetID:   "-",
			reason:     ReasonTargetUnavailable,
		},
		{
			name:       "short hex agent id",
			content:    "[@Broken](mention://agent/dead-beef) please look",
			targetType: "agent",
			targetID:   "dead-beef",
			reason:     ReasonTargetUnavailable,
		},
		{
			name:       "all plus malformed agent id",
			content:    "[@all](mention://all/all) heads up [@Broken](mention://agent/-) please look",
			targetType: "agent",
			targetID:   "-",
			reason:     ReasonTargetUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preview := previewCommentTriggersForTest(t, issueID, map[string]any{"content": tc.content})
			if got := len(preview.Agents); got != 0 {
				t.Fatalf("malformed mention preview agents = %d, want 0: %+v", got, preview.Agents)
			}
			if len(preview.Blocked) != 1 {
				t.Fatalf("malformed mention blocked = %+v, want exactly 1 outcome", preview.Blocked)
			}
			blocked := preview.Blocked[0]
			if blocked.TargetType != tc.targetType || blocked.TargetID != tc.targetID {
				t.Fatalf("blocked target = %s/%s, want %s/%s", blocked.TargetType, blocked.TargetID, tc.targetType, tc.targetID)
			}
			if blocked.Status != DispatchBlocked {
				t.Fatalf("blocked status = %q, want %q", blocked.Status, DispatchBlocked)
			}
			if blocked.ReasonCode != tc.reason {
				t.Fatalf("blocked reason = %q, want %q", blocked.ReasonCode, tc.reason)
			}

			// The create path must survive the same input and enqueue nothing.
			postCommentForTriggerPreviewTest(t, issueID, map[string]any{"content": tc.content})
			if got := countQueuedCommentTriggerTasks(t, issueID, assigneeID); got != 0 {
				t.Fatalf("assignee queued tasks = %d, want 0", got)
			}
		})
	}
}
