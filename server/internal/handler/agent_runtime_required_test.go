package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mentionAgentReasonCode posts a comment mentioning the agent and returns the
// reason_code of its trigger outcome, or "" when the mention was admitted.
func mentionAgentReasonCode(t *testing.T, issueID, agentID string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "[@Agent](mention://agent/" + agentID + ") please take a look",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("CreateComment: expected 200/201, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		TriggerOutcomes []struct {
			TargetType string `json:"target_type"`
			TargetID   string `json:"target_id"`
			Status     string `json:"status"`
			ReasonCode string `json:"reason_code"`
		} `json:"trigger_outcomes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode comment response: %v", err)
	}
	for _, o := range body.TriggerOutcomes {
		if o.TargetID != agentID {
			continue
		}
		if o.Status == "blocked" {
			return o.ReasonCode
		}
		return ""
	}
	t.Fatalf("no trigger outcome for agent %s in %s", agentID, w.Body.String())
	return ""
}

func createMentionFixtureIssue(t *testing.T, ctx context.Context, title string) string {
	t.Helper()
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, number, title, description, status, creator_type, creator_id)
		VALUES ($1, (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1),
		        $2, '', 'todo', 'member', $3)
		RETURNING id
	`, testWorkspaceID, title, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("insert fixture issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})
	return issueID
}
