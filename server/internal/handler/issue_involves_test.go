package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type involvesFixture struct {
	userID       string
	otherID      string
	ownedAgentID string
	otherAgentID string
	otherWsID    string
	otherWsAgent string
}

func setupInvolvesFixture(t *testing.T) *involvesFixture {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	fx := &involvesFixture{userID: testUserID}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Involves Other User", fmt.Sprintf("involves-other-%d@liexiu.ai", suffix)).Scan(&fx.otherID); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, fx.otherID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
	`, testWorkspaceID, fx.otherID); err != nil {
		t.Fatalf("create other member: %v", err)
	}

	runtimeID := handlerTestRuntimeID(t)
	fx.ownedAgentID = insertAgent(t, ctx, testWorkspaceID, runtimeID, fx.userID, fmt.Sprintf("Involves Owned Agent %d", suffix))
	fx.otherAgentID = insertAgent(t, ctx, testWorkspaceID, runtimeID, fx.otherID, fmt.Sprintf("Involves Other Agent %d", suffix))

	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', 'OTH') RETURNING id
	`, fmt.Sprintf("InvolvesOtherWs-%d", suffix), fmt.Sprintf("involves-other-ws-%d", suffix)).Scan(&fx.otherWsID); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, fx.otherWsID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, fx.otherWsID, fx.userID); err != nil {
		t.Fatalf("create other-ws member: %v", err)
	}

	var otherRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at
		) VALUES ($1, NULL, $2, 'cloud', 'other_ws_runtime', 'online', $3, '{}'::jsonb, now())
		RETURNING id
	`, fx.otherWsID, fmt.Sprintf("OtherWs Runtime %d", suffix), "other-ws-runtime").Scan(&otherRuntimeID); err != nil {
		t.Fatalf("create other-ws runtime: %v", err)
	}
	fx.otherWsAgent = insertAgent(t, ctx, fx.otherWsID, otherRuntimeID, fx.userID, fmt.Sprintf("OtherWs Owned Agent %d", suffix))
	return fx
}

func insertAgent(t *testing.T, ctx context.Context, workspaceID, runtimeID, ownerID, name string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4)
		RETURNING id
	`, workspaceID, name, runtimeID, ownerID).Scan(&id); err != nil {
		t.Fatalf("create agent %q: %v", name, err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, id) })
	return id
}

func insertIssueTo(t *testing.T, ctx context.Context, workspaceID, title, assigneeType, assigneeID string) string {
	t.Helper()
	var number int32
	if err := testPool.QueryRow(ctx, `
		UPDATE workspace
		SET issue_counter = GREATEST(issue_counter, (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)) + 1
		WHERE id = $1 RETURNING issue_counter
	`, workspaceID).Scan(&number); err != nil {
		t.Fatalf("next issue number: %v", err)
	}
	var id string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, description, status, priority,
			assignee_type, assignee_id, creator_type, creator_id, position, number
		)
		VALUES ($1, $2, NULL, 'todo', 'none', $3, $4, 'member', $5, 0, $6)
		RETURNING id
	`, workspaceID, title, assigneeType, assigneeID, testUserID, number).Scan(&id); err != nil {
		t.Fatalf("create issue %q: %v", title, err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, id) })
	return id
}

func listIssuesInvolves(t *testing.T, userID string) []string {
	t.Helper()
	path := fmt.Sprintf("/api/issues?workspace_id=%s&involves_user_id=%s&limit=500", testWorkspaceID, userID)
	w := httptest.NewRecorder()
	testHandler.ListIssues(w, newRequest("GET", path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Issues []IssueResponse `json:"issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	ids := make([]string, 0, len(resp.Issues))
	for _, issue := range resp.Issues {
		ids = append(ids, issue.ID)
	}
	return ids
}

func listGroupedIssuesInvolves(t *testing.T, userID string) []string {
	t.Helper()
	path := fmt.Sprintf("/api/issues/grouped?workspace_id=%s&group_by=assignee&statuses=todo&involves_user_id=%s&limit=100", testWorkspaceID, userID)
	w := httptest.NewRecorder()
	testHandler.ListGroupedIssues(w, newRequest("GET", path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListGroupedIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp GroupedIssuesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode grouped response: %v", err)
	}
	var ids []string
	for _, group := range resp.Groups {
		for _, issue := range group.Issues {
			ids = append(ids, issue.ID)
		}
	}
	return ids
}

func containsIssueID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func TestListIssues_InvolvesUserID_MatchesOwnedAgentAssignee(t *testing.T) {
	fx := setupInvolvesFixture(t)
	wantID := insertIssueTo(t, context.Background(), testWorkspaceID, "issue assigned to my owned agent", "agent", fx.ownedAgentID)
	if got := listIssuesInvolves(t, fx.userID); !containsIssueID(got, wantID) {
		t.Fatalf("owned-agent assignee not surfaced (want %s, got %v)", wantID, got)
	}
}

func TestListIssues_InvolvesUserID_ExcludesDirectMemberAssignee(t *testing.T) {
	fx := setupInvolvesFixture(t)
	issueID := insertIssueTo(t, context.Background(), testWorkspaceID, "tab 3 must not surface member assignment", "member", fx.userID)
	if got := listIssuesInvolves(t, fx.userID); containsIssueID(got, issueID) {
		t.Fatalf("involves_user_id surfaced direct member assignment %s: %v", issueID, got)
	}
}

func TestListIssues_InvolvesUserID_ExcludesOtherWorkspaceAgent(t *testing.T) {
	fx := setupInvolvesFixture(t)
	issueID := insertIssueTo(t, context.Background(), testWorkspaceID, "cross-workspace agent must not leak", "agent", fx.otherWsAgent)
	if got := listIssuesInvolves(t, fx.userID); containsIssueID(got, issueID) {
		t.Fatalf("cross-workspace agent surfaced %s: %v", issueID, got)
	}
}

func TestListIssues_InvolvesUserID_CombinesWithCreatorID(t *testing.T) {
	fx := setupInvolvesFixture(t)
	excluded := insertIssueTo(t, context.Background(), testWorkspaceID, "involves matches but creator does not", "agent", fx.ownedAgentID)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET creator_id = $1 WHERE id = $2`, fx.otherID, excluded); err != nil {
		t.Fatalf("patch creator: %v", err)
	}
	path := fmt.Sprintf("/api/issues?workspace_id=%s&involves_user_id=%s&creator_id=%s&limit=500", testWorkspaceID, fx.userID, fx.userID)
	w := httptest.NewRecorder()
	testHandler.ListIssues(w, newRequest("GET", path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Issues []IssueResponse `json:"issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	for _, issue := range resp.Issues {
		if issue.ID == excluded {
			t.Fatalf("combined filter surfaced issue with non-matching creator: %s", excluded)
		}
	}
}

func TestListIssues_InvolvesUserID_InvalidUUIDReturns400(t *testing.T) {
	path := fmt.Sprintf("/api/issues?workspace_id=%s&involves_user_id=not-a-uuid", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListIssues(w, newRequest("GET", path, nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid UUID, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListGroupedIssues_InvolvesUserID_MatchesOwnedAgentAssignee(t *testing.T) {
	fx := setupInvolvesFixture(t)
	wantID := insertIssueTo(t, context.Background(), testWorkspaceID, "grouped issue assigned to my agent", "agent", fx.ownedAgentID)
	if got := listGroupedIssuesInvolves(t, fx.userID); !containsIssueID(got, wantID) {
		t.Fatalf("grouped owned-agent assignee not surfaced (want %s, got %v)", wantID, got)
	}
}
