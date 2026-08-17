package main

import (
	"io"
	"testing"
)

// createIssue creates a basic todo issue for comment and attachment CRUD tests.
func createIssue(t *testing.T, title string) string {
	t.Helper()
	resp := authRequest(t, "POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  title,
		"status": "todo",
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("CreateIssue: expected 201, got %d: %s", resp.StatusCode, body)
	}
	var issue map[string]any
	readJSON(t, resp, &issue)
	return issue["id"].(string)
}
