package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Resource-label junction tables (agent_to_label / skill_to_label) deliberately
// carry no foreign keys, so every bulk hard-delete entry point that removes the
// owning agents/skills must clear their label links in the same transaction.
// These tests pin that cleanup on the four batch paths that never pass through a
// per-entity delete: runtime delete (strict + cascade), runtime-profile delete,
// and workspace delete. Without the sweep, a labelled agent/skill leaves a
// permanent, invisible orphan row once resource labels are enabled.

// insertLabelRow creates a real issue_label so the seeded junction row is valid
// regardless of whether a given database still carries the pre-release label_id
// foreign key. Registers cleanup.
func insertLabelRow(t *testing.T, ctx context.Context, workspaceID, resourceType string) string {
	t.Helper()
	var labelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue_label (workspace_id, resource_type, name, color)
		VALUES ($1, $2, $3, '#3b82f6')
		RETURNING id
	`, workspaceID, resourceType, resourceType+"-"+uuid.NewString()[:8]).Scan(&labelID); err != nil {
		t.Fatalf("insert issue_label: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue_label WHERE id = $1`, labelID)
	})
	return labelID
}

func seedAgentLabel(t *testing.T, ctx context.Context, workspaceID, agentID string) {
	t.Helper()
	labelID := insertLabelRow(t, ctx, workspaceID, "agent")
	if _, err := testPool.Exec(ctx,
		`INSERT INTO agent_to_label (agent_id, label_id) VALUES ($1, $2)`,
		agentID, labelID); err != nil {
		t.Fatalf("seed agent_to_label: %v", err)
	}
}

func seedSkillLabel(t *testing.T, ctx context.Context, workspaceID, skillID string) {
	t.Helper()
	labelID := insertLabelRow(t, ctx, workspaceID, "skill")
	if _, err := testPool.Exec(ctx,
		`INSERT INTO skill_to_label (skill_id, label_id) VALUES ($1, $2)`,
		skillID, labelID); err != nil {
		t.Fatalf("seed skill_to_label: %v", err)
	}
}

func countAgentLabelAssignments(t *testing.T, ctx context.Context, agentID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_to_label WHERE agent_id = $1`, agentID).Scan(&n); err != nil {
		t.Fatalf("count agent_to_label: %v", err)
	}
	return n
}

func countSkillLabelAssignments(t *testing.T, ctx context.Context, skillID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM skill_to_label WHERE skill_id = $1`, skillID).Scan(&n); err != nil {
		t.Fatalf("count skill_to_label: %v", err)
	}
	return n
}

func seedIsolatedRuntime(t *testing.T, name string) string {
	return createCascadeFixtureRuntime(t, context.Background(), name)
}

func seedAgentOnRuntime(t *testing.T, runtimeID, name string, archived bool) string {
	id := createCascadeFixtureAgent(t, context.Background(), runtimeID, name)
	if archived {
		if _, err := testPool.Exec(context.Background(), `UPDATE agent SET archived_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("archive agent: %v", err)
		}
	}
	return id
}

func agentExists(t *testing.T, agentID string) bool {
	t.Helper()
	var exists bool
	if err := testPool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM agent WHERE id = $1)`, agentID).Scan(&exists); err != nil {
		t.Fatalf("check agent exists: %v", err)
	}
	return exists
}

// TestDeleteAgentRuntime_KeepsUnboundAgentLabelAssignments: since MUL-5559 the
// strict runtime delete unbinds the archived agent instead of hard-deleting it,
// so its label links must SURVIVE. Clearing them by runtime — which is what the
// old sweep did — would strip labels off an agent that is still there.
func TestDeleteAgentRuntime_KeepsUnboundAgentLabelAssignments(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := seedIsolatedRuntime(t, "Label Cleanup Runtime")
	agentID := seedAgentOnRuntime(t, runtimeID, "Label Cleanup Archived Agent", true)
	seedAgentLabel(t, ctx, testWorkspaceID, agentID)

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/"+runtimeID, nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.DeleteAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteAgentRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !agentExists(t, agentID) {
		t.Fatalf("archived agent must survive its runtime as an unbound agent")
	}
	if n := countAgentLabelAssignments(t, ctx, agentID); n != 1 {
		t.Fatalf("agent_to_label rows for a surviving agent: got %d, want 1", n)
	}
}

// TestUnbindAgentsAndDeleteRuntime_KeepsAgentLabelAssignments: the confirmed
// endpoint unbinds the active agent, so its labels stay attached too.
func TestUnbindAgentsAndDeleteRuntime_KeepsAgentLabelAssignments(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createCascadeFixtureRuntime(t, ctx, "Label Cascade Runtime")
	agentID := createCascadeFixtureAgent(t, ctx, runtimeID, "Label Cascade Agent")
	seedAgentLabel(t, ctx, testWorkspaceID, agentID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/runtimes/"+runtimeID+"/unbind-agents-and-delete",
		map[string]any{"expected_active_agent_ids": []string{agentID}})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UnbindAgentsAndDeleteRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UnbindAgentsAndDeleteRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if n := countAgentLabelAssignments(t, ctx, agentID); n != 1 {
		t.Fatalf("agent_to_label rows for a surviving agent: got %d, want 1", n)
	}
}

// TestDeleteRuntimeProfile_KeepsAgentLabelAssignments: the profile teardown runs
// the same unbind, so the archived agent and its label links survive there too.
func TestDeleteRuntimeProfile_KeepsAgentLabelAssignments(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	profileID := insertRuntimeProfileFixture(t, ctx, "Label Cleanup Profile", "codex", "company-codex-label")
	runtimeID := insertProfileRuntimeFixture(t, ctx, profileID, "Label Cleanup Profile Runtime", "codex")
	agentID := createCascadeFixtureAgent(t, ctx, runtimeID, "Label Cleanup Profile Agent")
	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	seedAgentLabel(t, ctx, testWorkspaceID, agentID)

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/workspaces/"+testWorkspaceID+"/runtime-profiles/"+profileID, nil)
	req = withURLParams(req, "id", testWorkspaceID, "profileId", profileID)
	testHandler.DeleteRuntimeProfile(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteRuntimeProfile: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	if !agentExists(t, agentID) {
		t.Fatalf("archived agent must survive its runtime profile as an unbound agent")
	}
	if n := countAgentLabelAssignments(t, ctx, agentID); n != 1 {
		t.Fatalf("agent_to_label rows for a surviving agent: got %d, want 1", n)
	}
}
