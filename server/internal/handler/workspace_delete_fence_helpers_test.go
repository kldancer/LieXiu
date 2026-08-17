package handler

import (
	"context"
	"testing"
)

// workspaceDeletePathFixture is shared by the task-write fence tests. These
// fixtures exercise cross-workspace ownership resolution; they do not invoke
// the removed workspace-delete HTTP handler.
type workspaceDeletePathFixture struct {
	victimID    string
	neighbourID string

	victimAgent   string
	victimIssue   string
	victimRuntime string

	neighbourAgent   string
	neighbourIssue   string
	neighbourRuntime string

	taskViaAgent   string
	taskViaIssue   string
	taskViaRuntime string
	neighbourTask  string

	victimToken        string
	crossTaskToken     string
	crossAgentToken    string
	neighbourOnlyToken string
}

func newWorkspaceDeletePathFixture(t *testing.T, slugSuffix string) workspaceDeletePathFixture {
	t.Helper()
	ctx := context.Background()
	victimSlug := "handler-tests-delete-paths-victim-" + slugSuffix
	neighbourSlug := "handler-tests-delete-paths-neighbour-" + slugSuffix
	_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = ANY($1::text[])`,
		[]string{victimSlug, neighbourSlug})

	newWorkspace := func(name, slug string) string {
		var id string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
			name, slug).Scan(&id); err != nil {
			t.Fatalf("create workspace %s: %v", slug, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, id)
		})
		return id
	}

	f := workspaceDeletePathFixture{
		victimID:    newWorkspace("Delete Paths Victim "+slugSuffix, victimSlug),
		neighbourID: newWorkspace("Delete Paths Neighbour "+slugSuffix, neighbourSlug),
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		f.victimID, testUserID); err != nil {
		t.Fatalf("create owner member: %v", err)
	}

	newRuntime := func(wsID, name string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, owner_id)
VALUES ($1, $2, 'cloud', 'delete-test', 'offline', '', '{}'::jsonb, $3)
RETURNING id
`, wsID, name, testUserID).Scan(&id); err != nil {
			t.Fatalf("create runtime %s: %v", name, err)
		}
		return id
	}
	newAgent := func(wsID, runtimeID, name string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, owner_id)
VALUES ($1, $2, 'cloud', '{}'::jsonb, $3, $4)
RETURNING id
`, wsID, name, runtimeID, testUserID).Scan(&id); err != nil {
			t.Fatalf("create agent %s: %v", name, err)
		}
		return id
	}
	newIssue := func(wsID, title string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
INSERT INTO issue (workspace_id, title, creator_type, creator_id)
VALUES ($1, $2, 'member', $3)
RETURNING id
`, wsID, title, testUserID).Scan(&id); err != nil {
			t.Fatalf("create issue %s: %v", title, err)
		}
		return id
	}

	f.victimRuntime = newRuntime(f.victimID, "victim runtime")
	f.victimAgent = newAgent(f.victimID, f.victimRuntime, "victim agent")
	f.victimIssue = newIssue(f.victimID, "victim issue")
	f.neighbourRuntime = newRuntime(f.neighbourID, "neighbour runtime")
	f.neighbourAgent = newAgent(f.neighbourID, f.neighbourRuntime, "neighbour agent")
	f.neighbourIssue = newIssue(f.neighbourID, "neighbour issue")

	f.taskViaAgent = insertDeletePathTask(t, f.victimAgent, f.neighbourIssue, f.neighbourRuntime)
	f.taskViaIssue = insertDeletePathTask(t, f.neighbourAgent, f.victimIssue, f.neighbourRuntime)
	f.taskViaRuntime = insertDeletePathTask(t, f.neighbourAgent, f.neighbourIssue, f.victimRuntime)
	f.neighbourTask = insertDeletePathTask(t, f.neighbourAgent, f.neighbourIssue, f.neighbourRuntime)

	f.victimToken = insertDeletePathToken(t, "mul5999-victim-"+slugSuffix, f.taskViaAgent, f.victimAgent, f.victimID)
	f.crossTaskToken = insertDeletePathToken(t, "mul5999-cross-task-"+slugSuffix, f.taskViaIssue, f.neighbourAgent, f.neighbourID)
	f.crossAgentToken = insertDeletePathToken(t, "mul5999-cross-agent-"+slugSuffix, f.neighbourTask, f.victimAgent, f.neighbourID)
	f.neighbourOnlyToken = insertDeletePathToken(t, "mul5999-neighbour-"+slugSuffix, f.neighbourTask, f.neighbourAgent, f.neighbourID)

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM task_token WHERE id = ANY($1::uuid[])`,
			[]string{f.victimToken, f.crossTaskToken, f.crossAgentToken, f.neighbourOnlyToken})
		_, _ = testPool.Exec(bg, `DELETE FROM agent_task_queue WHERE id = ANY($1::uuid[])`,
			[]string{f.taskViaAgent, f.taskViaIssue, f.taskViaRuntime, f.neighbourTask})
	})
	return f
}

func insertDeletePathTask(t *testing.T, agentID, issueID, runtimeID string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, completed_at)
VALUES ($1, $2, $3, 'completed', now())
RETURNING id
`, agentID, issueID, runtimeID).Scan(&id); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return id
}

func insertDeletePathToken(t *testing.T, hash, taskID, agentID, wsID string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO task_token (token_hash, task_id, agent_id, workspace_id, user_id, expires_at)
VALUES ($1, $2, $3, $4, $5, now() + interval '1 hour')
RETURNING id
`, hash, taskID, agentID, wsID, testUserID).Scan(&id); err != nil {
		t.Fatalf("create task token %s: %v", hash, err)
	}
	return id
}

func rowExists(t *testing.T, table, id string) bool {
	t.Helper()
	var found bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, id).Scan(&found); err != nil {
		t.Fatalf("check %s %s: %v", table, id, err)
	}
	return found
}
