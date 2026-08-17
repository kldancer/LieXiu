package main

import (
	"context"
	"testing"
)

func createTestIssue(t *testing.T, workspaceID, creatorID string) string {
	t.Helper()
	var issueID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, position, number)
		VALUES ($1, 'activity test issue', 'todo', 'medium', 'member', $2, 0,
		        (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1))
		RETURNING id
	`, workspaceID, creatorID).Scan(&issueID)
	if err != nil {
		t.Fatalf("createTestIssue: %v", err)
	}
	return issueID
}

func createTestUser(t *testing.T, email string) string {
	t.Helper()
	var userID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Activity Test User", email).Scan(&userID)
	if err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
	return userID
}

func cleanupTestIssue(t *testing.T, issueID string) {
	t.Helper()
	_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
}

func cleanupTestUser(t *testing.T, email string) {
	t.Helper()
	_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE email = $1`, email)
}
