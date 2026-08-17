package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestWorkspaceTeardownDirtyTriggersHaveGuard(t *testing.T) {
	ctx := context.Background()
	for _, triggerName := range []string{
		"trg_atq_dirty_hourly",
		"trg_issue_delete_dirty_hourly",
		"trg_tu_dirty_hourly",
	} {
		var definition string
		if err := testPool.QueryRow(ctx, `
SELECT pg_get_triggerdef(oid)
FROM pg_trigger
WHERE tgname = $1
  AND NOT tgisinternal
`, triggerName).Scan(&definition); err != nil {
			t.Fatalf("read trigger %s: %v", triggerName, err)
		}
		if !strings.Contains(definition, "liexiu.workspace_teardown") {
			t.Fatalf("trigger %s does not guard workspace teardown: %s", triggerName, definition)
		}
	}
}

func TestWorkspaceTeardownModeDoesNotLeakIntoOrdinaryDeletes(t *testing.T) {
	ctx := context.Background()
	conn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin teardown marker transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('liexiu.workspace_teardown', 'on', true)`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("set transaction-local teardown mode: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit teardown marker transaction: %v", err)
	}

	var teardownMode string
	if err := conn.QueryRow(ctx, `SELECT current_setting('liexiu.workspace_teardown', true)`).Scan(&teardownMode); err != nil {
		t.Fatalf("read teardown mode after commit: %v", err)
	}
	if teardownMode != "" {
		t.Fatalf("teardown mode leaked after commit: %q", teardownMode)
	}

	var runtimeID, agentID string
	if err := conn.QueryRow(ctx, `
SELECT runtime.id, agent.id
FROM agent_runtime AS runtime
JOIN agent ON agent.runtime_id = runtime.id
WHERE runtime.workspace_id = $1
LIMIT 1
`, testWorkspaceID).Scan(&runtimeID, &agentID); err != nil {
		t.Fatalf("load ordinary delete fixture agent: %v", err)
	}

	var issueID string
	if err := conn.QueryRow(ctx, `
INSERT INTO issue (workspace_id, title, creator_id, creator_type, number)
VALUES (
	$1, 'ordinary delete after workspace teardown', $2, 'member',
	(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1)
)
RETURNING id
`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create ordinary delete issue: %v", err)
	}
	var taskID string
	if err := conn.QueryRow(ctx, `
INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status)
VALUES ($1, $2, $3, 'completed')
RETURNING id
`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create ordinary delete task: %v", err)
	}

	const provider = "workspace-teardown-ordinary-delete"
	_, _ = conn.Exec(ctx, `DELETE FROM task_usage_hourly_dirty WHERE provider = $1`, provider)
	_, _ = conn.Exec(ctx, `DELETE FROM task_usage_hourly WHERE provider = $1`, provider)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM task_usage_hourly_dirty WHERE provider = $1`, provider)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM task_usage_hourly WHERE provider = $1`, provider)
	})

	var usageID string
	if err := conn.QueryRow(ctx, `
INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens)
VALUES ($1, $2, 'task-usage-delete', 10, 5)
RETURNING id
`, taskID, provider).Scan(&usageID); err != nil {
		t.Fatalf("create ordinary task usage: %v", err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM task_usage WHERE id = $1`, usageID); err != nil {
		t.Fatalf("delete ordinary task usage: %v", err)
	}

	var dirtyCount int
	if err := conn.QueryRow(ctx, `
SELECT COUNT(*)
FROM task_usage_hourly_dirty
WHERE provider = $1 AND model = 'task-usage-delete'
`, provider).Scan(&dirtyCount); err != nil {
		t.Fatalf("count task-usage delete dirty keys: %v", err)
	}
	if dirtyCount != 1 {
		t.Fatalf("ordinary task_usage DELETE dirty keys = %d, want 1", dirtyCount)
	}

	if _, err := conn.Exec(ctx, `
INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens)
VALUES ($1, $2, 'issue-delete', 10, 5)
`, taskID, provider); err != nil {
		t.Fatalf("create ordinary issue-delete usage: %v", err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID); err != nil {
		t.Fatalf("delete ordinary issue: %v", err)
	}
	if err := conn.QueryRow(ctx, `
SELECT COUNT(*)
FROM task_usage_hourly_dirty
WHERE provider = $1 AND model = 'issue-delete'
`, provider).Scan(&dirtyCount); err != nil {
		t.Fatalf("count issue delete dirty keys: %v", err)
	}
	if dirtyCount != 1 {
		t.Fatalf("ordinary issue DELETE dirty keys = %d, want 1", dirtyCount)
	}
}

func TestUpdateWorkspace_AvatarURL(t *testing.T) {
	ctx := context.Background()

	const slug = "handler-tests-avatar-url"
	_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)

	var wsID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description)
VALUES ($1, $2, $3)
RETURNING id
`, "Handler Test Avatar URL", slug, "UpdateWorkspace avatar_url test").Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
	})

	if _, err := testPool.Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role)
VALUES ($1, $2, 'owner')
`, wsID, testUserID); err != nil {
		t.Fatalf("create owner member: %v", err)
	}

	const avatarURL = "https://cdn.example.com/workspaces/abc/logo.png"

	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/workspaces/"+wsID, map[string]any{
		"avatar_url": avatarURL,
	})
	req = withURLParam(req, "id", wsID)
	testHandler.UpdateWorkspace(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from UpdateWorkspace, got %d: %s", w.Code, w.Body.String())
	}

	var resp WorkspaceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AvatarURL == nil || *resp.AvatarURL != avatarURL {
		t.Fatalf("expected avatar_url %q in response, got %v", avatarURL, resp.AvatarURL)
	}
	if resp.Name != "Handler Test Avatar URL" {
		t.Fatalf("name should be unchanged by avatar-only update, got %q", resp.Name)
	}

	var dbAvatar *string
	if err := testPool.QueryRow(ctx, `SELECT avatar_url FROM workspace WHERE id = $1`, wsID).Scan(&dbAvatar); err != nil {
		t.Fatalf("read avatar_url back: %v", err)
	}
	if dbAvatar == nil || *dbAvatar != avatarURL {
		t.Fatalf("expected avatar_url %q persisted, got %v", avatarURL, dbAvatar)
	}

	// A follow-up update that doesn't include avatar_url must leave it alone.
	w2 := httptest.NewRecorder()
	req2 := newRequest("PATCH", "/api/workspaces/"+wsID, map[string]any{
		"description": "new description",
	})
	req2 = withURLParam(req2, "id", wsID)
	testHandler.UpdateWorkspace(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 from second UpdateWorkspace, got %d: %s", w2.Code, w2.Body.String())
	}

	var resp2 WorkspaceResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if resp2.AvatarURL == nil || *resp2.AvatarURL != avatarURL {
		t.Fatalf("avatar_url should be preserved by partial update, got %v", resp2.AvatarURL)
	}
}

func TestUpdateWorkspace_ReposValidation(t *testing.T) {
	ctx := context.Background()

	const slug = "handler-tests-repos-validation"
	_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)

	var wsID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description)
VALUES ($1, $2, $3)
RETURNING id
`, "Handler Test Repos Validation", slug, "UpdateWorkspace repos validation test").Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
	})

	if _, err := testPool.Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role)
VALUES ($1, $2, 'owner')
`, wsID, testUserID); err != nil {
		t.Fatalf("create owner member: %v", err)
	}

	t.Run("rejects invalid repo URLs without persisting", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("PATCH", "/api/workspaces/"+wsID, map[string]any{
			"repos": []map[string]any{
				{"url": "not-a-url"},
			},
		})
		req = withURLParam(req, "id", wsID)
		testHandler.UpdateWorkspace(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 from invalid repos update, got %d: %s", w.Code, w.Body.String())
		}

		var raw []byte
		if err := testPool.QueryRow(ctx, `SELECT repos FROM workspace WHERE id = $1`, wsID).Scan(&raw); err != nil {
			t.Fatalf("read repos: %v", err)
		}
		if string(raw) != "[]" {
			t.Fatalf("invalid repos update should not persist, got %s", raw)
		}
	})

	t.Run("normalizes valid repos", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("PATCH", "/api/workspaces/"+wsID, map[string]any{
			"repos": []map[string]any{
				{
					"url":         "  https://github.com/kailonyang/liexiu.git  ",
					"description": "  main monorepo  ",
				},
				{
					"url": "https://github.com/kailonyang/liexiu.git",
				},
				{
					"url": "git@github.com:kailonyang/liexiu-cloud.git",
				},
			},
		})
		req = withURLParam(req, "id", wsID)
		testHandler.UpdateWorkspace(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 from valid repos update, got %d: %s", w.Code, w.Body.String())
		}

		var raw []byte
		if err := testPool.QueryRow(ctx, `SELECT repos FROM workspace WHERE id = $1`, wsID).Scan(&raw); err != nil {
			t.Fatalf("read repos: %v", err)
		}
		var repos []workspaceRepoRef
		if err := json.Unmarshal(raw, &repos); err != nil {
			t.Fatalf("decode repos: %v", err)
		}
		if len(repos) != 2 {
			t.Fatalf("expected duplicate URL to be deduped, got %d repos: %s", len(repos), raw)
		}
		if repos[0].URL != "https://github.com/kailonyang/liexiu.git" || repos[0].Description != "main monorepo" {
			t.Fatalf("first repo not normalized: %+v", repos[0])
		}
		if repos[1].URL != "git@github.com:kailonyang/liexiu-cloud.git" {
			t.Fatalf("second repo not preserved: %+v", repos[1])
		}
	})
}

// revocationFixture is a minimal (workspace, member-to-revoke, runtime,
// agent, queued-task, daemon-token) bundle used to drive the revocation
// tests. The "requester" is always testUserID (owner of the workspace) so
// `newRequest` passes the existing fixtures' auth context unchanged.
// TestDefaultIssuePrefixFromSlug pins the derivation new workspaces get
// (MUL-6050): alphanumerics of the slug, first 4, uppercased. The Chinese
// cases are the point of the change — under the old name-based derivation
// every one of them collapsed to "WS".
//
// Keep this table in sync with the client-side preview
// (packages/views/onboarding/steps/step-workspace.tsx). If the two ever
// disagree, the create screen lies about the identifier the user will get.
func TestDefaultIssuePrefixFromSlug(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"acme", "ACME"},
		{"front-end", "FRON"},
		{"growth", "GROW"},
		{"team-2", "TEAM"},
		{"a1b2c3", "A1B2"},
		{"ab", "AB"},
		{"x", "X"},
		// Slugs the create handler would have rejected anyway; the function
		// stays total so no caller can persist an empty prefix.
		{"", "WS"},
		{"--", "WS"},
	}

	for _, tc := range cases {
		if got := defaultIssuePrefixFromSlug(tc.slug); got != tc.want {
			t.Errorf("defaultIssuePrefixFromSlug(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
}

// TestLegacyIssuePrefixFromName_Frozen guards the read-time fallback for
// workspaces whose stored prefix is empty. Issue identifiers are computed
// from the current prefix on every read, so changing what this returns would
// silently rewrite the identifier of every historical issue in those
// workspaces. The product decision on MUL-6050 was explicit: no backfill,
// existing workspaces are left exactly as they are — which means this
// function must keep returning what it always did, including "WS" for
// non-ASCII names.
func TestLegacyIssuePrefixFromName_Frozen(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"Jiayuan's Workspace", "JIA"},
		{"My Team", "MYT"},
		{"AB", "AB"},
		{"Team 2", "TEA"},
		{"前端团队", "WS"},
		{"", "WS"},
	}

	for _, tc := range cases {
		if got := legacyIssuePrefixFromName(tc.name); got != tc.want {
			t.Errorf("legacyIssuePrefixFromName(%q) = %q, want %q — this function is frozen; changing it rewrites existing issue identifiers", tc.name, got, tc.want)
		}
	}
}

// TestIssuePrefixForWorkspace_LegacyFallbackFrozen guards the resolution rule
// itself, at the seam every read-time caller goes through (getIssuePrefix and
// the GitHub close-intent scan, which holds the row already).
//
// A stored prefix always wins; only an empty one falls back, and that fallback
// must stay on the old name-based derivation. Pointing it at
// defaultIssuePrefixFromSlug would rewrite the identifier of every issue in
// those legacy workspaces on the next read — the exact outcome the "no
// backfill, leave existing workspaces alone" decision on MUL-6050 rules out.
func TestIssuePrefixForWorkspace_LegacyFallbackFrozen(t *testing.T) {
	cases := []struct {
		label string
		ws    db.Workspace
		want  string
	}{
		{"stored prefix wins", db.Workspace{Name: "前端团队", Slug: "frontend", IssuePrefix: "FE"}, "FE"},
		{"empty prefix, CJK name", db.Workspace{Name: "前端团队", Slug: "frontend"}, "WS"},
		{"empty prefix, ASCII name", db.Workspace{Name: "My Team", Slug: "my-team"}, "MYT"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if got := issuePrefixForWorkspace(tc.ws); got != tc.want {
				t.Fatalf("issuePrefixForWorkspace(%+v) = %q, want %q — legacy workspaces must keep the identifiers they already have", tc.ws, got, tc.want)
			}
		})
	}
}

func TestNormalizeIssuePrefix(t *testing.T) {
	cases := []struct {
		raw   string
		want  string
		valid bool
	}{
		{"acme", "ACME", true},
		{"  acme  ", "ACME", true},
		{"AB12", "AB12", true},
		{"ABCDEFGHIJ", "ABCDEFGHIJ", true},
		// Absent / blank means "use the default", not "invalid".
		{"", "", true},
		{"   ", "", true},
		// Rejections the API accepted before MUL-6050.
		{"ABCDEFGHIJK", "", false},
		{"前端", "", false},
		{"AB-CD", "", false},
		{"AB CD", "", false},
		{"AB_CD", "", false},
	}

	for _, tc := range cases {
		got, ok := normalizeIssuePrefix(tc.raw)
		if ok != tc.valid {
			t.Errorf("normalizeIssuePrefix(%q) valid = %v, want %v", tc.raw, ok, tc.valid)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeIssuePrefix(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestUpdateWorkspace_RejectsInvalidIssuePrefix is the PATCH half of the same
// hole: settings normalizes client-side, but the endpoint took anything.
func TestUpdateWorkspace_RejectsInvalidIssuePrefix(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var before string
	if err := testPool.QueryRow(ctx, `SELECT issue_prefix FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&before); err != nil {
		t.Fatalf("read current prefix: %v", err)
	}

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PATCH", "/api/workspaces/"+testWorkspaceID, map[string]any{"issue_prefix": "前端团队前端团队前端"}),
		"id", testWorkspaceID,
	)
	testHandler.UpdateWorkspace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-ASCII issue_prefix, got %d: %s", w.Code, w.Body.String())
	}

	var after string
	if err := testPool.QueryRow(ctx, `SELECT issue_prefix FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&after); err != nil {
		t.Fatalf("re-read prefix: %v", err)
	}
	if after != before {
		t.Fatalf("issue_prefix changed on a rejected update: %q → %q", before, after)
	}
}
