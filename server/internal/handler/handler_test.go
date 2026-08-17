package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kailonyang/liexiu/server/internal/analytics"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/realtime"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

var testHandler *Handler
var testPool *pgxpool.Pool
var testUserID string
var testWorkspaceID string
var testRuntimeID string

const (
	handlerTestEmail         = "handler-test@liexiu.ai"
	handlerTestName          = "Handler Test User"
	handlerTestWorkspaceSlug = "handler-tests"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Println("Skipping tests: DATABASE_URL is not set")
		os.Exit(0)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Skipping tests: could not connect to database: %v\n", err)
		os.Exit(0)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("Skipping tests: database not reachable: %v\n", err)
		pool.Close()
		os.Exit(0)
	}

	queries := db.New(pool)
	hub := realtime.NewHub()
	go hub.Run()
	bus := events.New()
	testHandler = New(queries, pool, hub, bus, nil, nil, analytics.NoopClient{}, Config{})
	// httptest.NewRequest defaults RemoteAddr to 192.0.2.1, so every webhook
	// test in the suite shares one IP bucket. With the production default
	// (30/min) the budget runs out partway through the suite and unrelated
	// downstream tests see a 429 from the IP gate instead of the response
	// they're asserting. Tests that exercise rate limiting deliberately
	// swap in a tight limiter with t.Cleanup; this generous default keeps
	// the rest of the suite hermetic.
	testHandler.WebhookRateLimiter = NewMemoryWebhookRateLimiter(WebhookRateLimit{Limit: 1_000_000, Window: time.Minute})
	testHandler.WebhookIPRateLimiter = NewMemoryWebhookIPRateLimiter(WebhookRateLimit{Limit: 1_000_000, Window: time.Minute})
	testHandler.WebhookAbsoluteIPRateLimiter = NewMemoryWebhookAbsoluteIPRateLimiter(WebhookRateLimit{Limit: 1_000_000, Window: time.Minute})
	testPool = pool

	testUserID, testWorkspaceID, err = setupHandlerTestFixture(ctx, pool)
	if err != nil {
		fmt.Printf("Failed to set up handler test fixture: %v\n", err)
		pool.Close()
		os.Exit(1)
	}

	code := m.Run()
	if err := cleanupHandlerTestFixture(context.Background(), pool); err != nil {
		fmt.Printf("Failed to clean up handler test fixture: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	pool.Close()
	os.Exit(code)
}

func setupHandlerTestFixture(ctx context.Context, pool *pgxpool.Pool) (string, string, error) {
	if err := cleanupHandlerTestFixture(ctx, pool); err != nil {
		return "", "", err
	}

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, handlerTestName, handlerTestEmail).Scan(&userID); err != nil {
		return "", "", err
	}

	var workspaceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Handler Tests", handlerTestWorkspaceSlug, "Temporary workspace for handler tests", "HAN").Scan(&workspaceID); err != nil {
		return "", "", err
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		return "", "", err
	}

	var runtimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, NULL, $2, 'cloud', $3, 'online', $4, '{}'::jsonb, $5, now())
		RETURNING id
	`, workspaceID, "Handler Test Runtime", "handler_test_runtime", "Handler test runtime", userID).Scan(&runtimeID); err != nil {
		return "", "", err
	}
	testRuntimeID = runtimeID

	var seededAgentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, permission_mode, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 'public_to', 1, $4)
		RETURNING id
	`, workspaceID, "Handler Test Agent", runtimeID, userID).Scan(&seededAgentID); err != nil {
		return "", "", err
	}
	// MUL-3963: the seeded workspace-visible agent is invocable by workspace
	// members and A2A triggers, so seed its workspace invocation target.
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_invocation_target (agent_id, target_type, target_id)
		VALUES ($1, 'workspace', $2)
		ON CONFLICT (agent_id, target_type, target_id) DO NOTHING
	`, seededAgentID, workspaceID); err != nil {
		return "", "", err
	}

	return userID, workspaceID, nil
}

func cleanupHandlerTestFixture(ctx context.Context, pool *pgxpool.Pool) error {
	var hasClientUsageTable bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('client_usage_daily') IS NOT NULL`).Scan(&hasClientUsageTable); err != nil {
		return err
	}
	if hasClientUsageTable {
		if _, err := pool.Exec(ctx, `DELETE FROM client_usage_daily WHERE user_id IN (SELECT id FROM "user" WHERE email = $1)`, handlerTestEmail); err != nil {
			return err
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, handlerTestWorkspaceSlug); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, handlerTestEmail); err != nil {
		return err
	}
	return nil
}

func newRequest(method, path string, body any) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	return req
}

func withURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func setWorkspaceIssuePrefixForTest(t *testing.T, prefix string) {
	t.Helper()

	ctx := context.Background()
	var previous string
	if err := testPool.QueryRow(ctx, `SELECT issue_prefix FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&previous); err != nil {
		t.Fatalf("load workspace prefix: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET issue_prefix = $1 WHERE id = $2`, prefix, testWorkspaceID); err != nil {
		t.Fatalf("set workspace prefix: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `UPDATE workspace SET issue_prefix = $1 WHERE id = $2`, previous, testWorkspaceID)
	})
}

func handlerTestRuntimeID(t *testing.T) string {
	t.Helper()

	var runtimeID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM agent_runtime WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&runtimeID); err != nil {
		t.Fatalf("failed to load handler test runtime: %v", err)
	}

	return runtimeID
}

func createHandlerTestAgent(t *testing.T, name string, mcpConfig []byte) string {
	t.Helper()

	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, permission_mode, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 'public_to', 1, $4, '', '{}'::jsonb, '[]'::jsonb, $5)
		RETURNING id
	`, testWorkspaceID, name, handlerTestRuntimeID(t), testUserID, mcpConfig).Scan(&agentID); err != nil {
		t.Fatalf("failed to create handler test agent: %v", err)
	}
	// Generic test agents are workspace-invocable (MUL-3963): seed the
	// matching workspace invocation target so canInvokeAgent admits workspace
	// members and A2A triggers, mirroring the pre-permission-model behavior
	// where a workspace-visible agent could be triggered by anyone in the
	// workspace. Dedicated private-agent tests use privateAgentTestFixture.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_invocation_target (agent_id, target_type, target_id)
		VALUES ($1, 'workspace', $2)
		ON CONFLICT (agent_id, target_type, target_id) DO NOTHING
	`, agentID, testWorkspaceID); err != nil {
		t.Fatalf("failed to seed workspace invocation target: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	return agentID
}

// createHandlerTestTaskForAgent seeds a running agent_task_queue row for the
// given agent (with no associated issue) and returns the task UUID. Used by
// tests that need to set X-Task-ID alongside X-Agent-ID — resolveActor now
// requires the pair to be present and consistent before granting "agent"
// actor identity.
func createHandlerTestTaskForAgent(t *testing.T, agentID string) string {
	return createHandlerTestTaskForAgentOnIssue(t, agentID, "")
}

// createHandlerTestTaskForAgentOnIssue seeds a running agent_task_queue row
// for the given agent, optionally bound to an issue (pass "" to leave
// issue_id NULL). The bound-issue form is needed by the self-loop guard
// test, which compares the calling task's issue_id against the promoted
// issue — only a same-issue match counts as a true self-loop.
//
// Status is 'running' because X-Task-ID is something a currently-executing
// task sends. Using 'running' also keeps the seed outside the
// idx_one_pending_task_per_issue_agent unique index (queued/dispatched only)
// and outside callers' `status='queued'` count assertions, so tests can
// assert that the handler did or did not enqueue a NEW task without
// double-counting the seed.
func createHandlerTestTaskForAgentOnIssue(t *testing.T, agentID, issueID string) string {
	t.Helper()

	var issueArg any
	if issueID == "" {
		issueArg = nil
	} else {
		issueArg = issueID
	}

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, started_at)
		VALUES ($1, $2, 'running', 0, $3, now())
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), issueArg).Scan(&taskID); err != nil {
		t.Fatalf("failed to create handler test task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	return taskID
}

func fetchAgentMcpConfig(t *testing.T, agentID string) []byte {
	t.Helper()

	var mcpConfig []byte
	if err := testPool.QueryRow(context.Background(), `SELECT mcp_config FROM agent WHERE id = $1`, agentID).Scan(&mcpConfig); err != nil {
		t.Fatalf("failed to load agent mcp_config: %v", err)
	}

	return mcpConfig
}

func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()

	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("failed to unmarshal got JSON %q: %v", string(got), err)
	}

	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("failed to unmarshal want JSON %q: %v", want, err)
	}

	gotJSON, err := json.Marshal(gotValue)
	if err != nil {
		t.Fatalf("failed to marshal normalized got JSON: %v", err)
	}
	wantJSON, err := json.Marshal(wantValue)
	if err != nil {
		t.Fatalf("failed to marshal normalized want JSON: %v", err)
	}

	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("expected JSON %s, got %s", string(wantJSON), string(gotJSON))
	}
}

func TestIssueCRUD(t *testing.T) {
	// Create
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "Test issue from Go test",
		"status":   "todo",
		"priority": "medium",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	if created.Title != "Test issue from Go test" {
		t.Fatalf("CreateIssue: expected title 'Test issue from Go test', got '%s'", created.Title)
	}
	if created.Status != "todo" {
		t.Fatalf("CreateIssue: expected status 'todo', got '%s'", created.Status)
	}
	issueID := created.ID

	// Get
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueID, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.GetIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var fetched IssueResponse
	json.NewDecoder(w.Body).Decode(&fetched)
	if fetched.ID != issueID {
		t.Fatalf("GetIssue: expected id '%s', got '%s'", issueID, fetched.ID)
	}

	// Update - partial (only status)
	w = httptest.NewRecorder()
	status := "in_progress"
	req = newRequest("PUT", "/api/issues/"+issueID, map[string]any{
		"status": status,
	})
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated IssueResponse
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Status != "in_progress" {
		t.Fatalf("UpdateIssue: expected status 'in_progress', got '%s'", updated.Status)
	}
	if updated.Title != "Test issue from Go test" {
		t.Fatalf("UpdateIssue: title should be preserved, got '%s'", updated.Title)
	}
	if updated.Priority != "medium" {
		t.Fatalf("UpdateIssue: priority should be preserved, got '%s'", updated.Priority)
	}

	// List
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues?workspace_id="+testWorkspaceID, nil)
	testHandler.ListIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var listResp map[string]any
	json.NewDecoder(w.Body).Decode(&listResp)
	issues := listResp["issues"].([]any)
	if len(issues) == 0 {
		t.Fatal("ListIssues: expected at least 1 issue")
	}

	// Delete
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+issueID, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.DeleteIssue(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssue: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Verify deleted
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueID, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.GetIssue(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetIssue after delete: expected 404, got %d", w.Code)
	}
}

// TestDeleteIssueByIdentifier guards against #1661 — DELETE /api/issues/{id}
// must actually delete the row when the path segment is a human-readable
// identifier ("HAN-42") rather than a UUID. Before the PR #1680 + MUL-1410
// refactor, parseUUID(rawString) silently produced a zero UUID, the SQL
// DELETE matched nothing, and the handler still returned 204.
//
// Also asserts the issue:deleted WS event payload carries the resolved UUID,
// not the raw identifier — frontend caches key by UUID and would otherwise
// leave stale entries on other clients after an identifier-path delete.
func TestDeleteIssueByIdentifier(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "Issue to delete by identifier",
		"status":   "todo",
		"priority": "medium",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	if created.Identifier == "" {
		t.Fatalf("CreateIssue: expected identifier to be populated, got empty")
	}

	// Capture the issue:deleted event payload via the bus.
	gotPayload := make(chan map[string]any, 1)
	testHandler.Bus.Subscribe(protocol.EventIssueDeleted, func(e events.Event) {
		if payload, ok := e.Payload.(map[string]any); ok {
			select {
			case gotPayload <- payload:
			default:
			}
		}
	})

	// Delete using the human-readable identifier (e.g. "HAN-1") rather than the UUID.
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+created.Identifier, nil)
	req = withURLParam(req, "id", created.Identifier)
	testHandler.DeleteIssue(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssue by identifier: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the row is actually gone — the silent-data-loss bug would have
	// returned 204 here too, but the row would still exist.
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM issue WHERE id = $1`, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Fatalf("DeleteIssue by identifier returned 204 but row still exists (count=%d) — silent-data-loss regression", count)
	}

	// Event payload must carry the resolved UUID, not the identifier string.
	select {
	case payload := <-gotPayload:
		issueID, _ := payload["issue_id"].(string)
		if issueID != created.ID {
			t.Fatalf("issue:deleted event payload issue_id = %q; want resolved UUID %q (must not leak identifier %q)", issueID, created.ID, created.Identifier)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive issue:deleted event within timeout")
	}
}

// TestGetIssueByIdentifierEnforcesPrefix covers the contract behind the
// human-readable issue URL `/{ws}/issues/{key}`: the prefix is part of the key,
// not decoration. Identifier resolution used to compare the number only, so
// every prefix with the right number opened the same issue — which means no
// identifier URL could be treated as canonical, and a mistyped or stale prefix
// silently opened someone else's link target.
func TestGetIssueByIdentifierEnforcesPrefix(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "Issue addressed by identifier",
		"status":   "todo",
		"priority": "medium",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)

	idx := strings.LastIndex(created.Identifier, "-")
	if idx <= 0 {
		t.Fatalf("CreateIssue: unexpected identifier %q", created.Identifier)
	}
	prefix, number := created.Identifier[:idx], created.Identifier[idx+1:]

	// The workspace's own prefix resolves, in either case — a hand-typed
	// lowercase key from a chat message must open the same issue.
	for _, id := range []string{created.Identifier, strings.ToLower(created.Identifier)} {
		w = httptest.NewRecorder()
		req = newRequest("GET", "/api/issues/"+id, nil)
		req = withURLParam(req, "id", id)
		testHandler.GetIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GetIssue(%q): expected 200, got %d: %s", id, w.Code, w.Body.String())
		}
		var got IssueResponse
		json.NewDecoder(w.Body).Decode(&got)
		if got.ID != created.ID {
			t.Fatalf("GetIssue(%q): resolved to %s, want %s", id, got.ID, created.ID)
		}
	}

	// A foreign prefix carrying the right number must not resolve.
	foreign := prefix + "X-" + number
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+foreign, nil)
	req = withURLParam(req, "id", foreign)
	testHandler.GetIssue(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetIssue(%q): expected 404 for a foreign prefix, got %d: %s", foreign, w.Code, w.Body.String())
	}
}

// TestDeleteIssueRejectsInvalidUUID verifies that a path segment that is
// neither a valid UUID nor a valid identifier returns 404 (not 204) — the
// handler must never silently succeed on malformed input.
func TestDeleteIssueRejectsInvalidUUID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/issues/not-a-uuid-or-identifier", nil)
	req = withURLParam(req, "id", "not-a-uuid-or-identifier")
	testHandler.DeleteIssue(w, req)
	if w.Code == http.StatusNoContent {
		t.Fatalf("DeleteIssue with invalid id: must not return 204; got %d", w.Code)
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("DeleteIssue with invalid id: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueDefaultStatusIsTodo verifies that issues created without an
// explicit status default to "todo" so the daemon picks them up immediately.
// Before this fix the default was "backlog", which daemons ignore.
func TestCreateIssueDefaultStatusIsTodo(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Issue with no explicit status",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	if created.Status != "todo" {
		t.Fatalf("CreateIssue: expected default status 'todo', got '%s'", created.Status)
	}

	// Cleanup
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

// TestCreateIssueExplicitBacklogPreserved verifies that explicitly requesting
// "backlog" status is still respected — only the implicit default changed.
func TestCreateIssueExplicitBacklogPreserved(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Explicit backlog issue",
		"status": "backlog",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	if created.Status != "backlog" {
		t.Fatalf("CreateIssue: expected explicit 'backlog' to be preserved, got '%s'", created.Status)
	}

	// Cleanup
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

// TestCreateIssueRejectsCrossWorkspaceParent guards the workspace
// boundary check that lives in service.IssueService.Create. A request
// that pins parent_issue_id to an issue in a foreign workspace must be
// rejected before the row is created — this is the structural reason
// IssueService owns the parent lookup (not the HTTP handler). The test
// inserts a foreign workspace + issue directly via SQL, then drives the
// request through the regular handler entry point.
func TestCreateIssueRejectsCrossWorkspaceParent(t *testing.T) {
	ctx := context.Background()

	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Cross-workspace parent test", "xwp-parent-test", "Foreign workspace", "XWP").Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	var foreignParentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		VALUES ($1, $2, 'todo', 'none', 'member', $3, 1)
		RETURNING id
	`, otherWorkspaceID, "Foreign parent", testUserID).Scan(&foreignParentID); err != nil {
		t.Fatalf("insert foreign parent: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "Should be rejected",
		"parent_issue_id": foreignParentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue with foreign parent: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "parent issue not found in this workspace") {
		t.Fatalf("CreateIssue with foreign parent: expected boundary error message, got %s", w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue WHERE workspace_id = $1 AND title = $2`,
		testWorkspaceID, "Should be rejected",
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected create still wrote a row (count=%d) — service-layer boundary check failed", count)
	}
}

// TestCreateIssueRejectsCrossWorkspaceProject mirrors the parent test for
// the project workspace boundary. Same reasoning: future create entries
// (Lark /issue, MCP, API keys) must inherit this guard from the service
// without re-implementing it.
func TestCreateIssueRejectsCrossWorkspaceProject(t *testing.T) {
	ctx := context.Background()

	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Cross-workspace project test", "xwp-project-test", "Foreign workspace", "XWP").Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	var foreignProjectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status, priority)
		VALUES ($1, $2, 'planned', 'none')
		RETURNING id
	`, otherWorkspaceID, "Foreign project").Scan(&foreignProjectID); err != nil {
		t.Fatalf("insert foreign project: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Should be rejected",
		"project_id": foreignProjectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue with foreign project: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "project not found in this workspace") {
		t.Fatalf("CreateIssue with foreign project: expected boundary error message, got %s", w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue WHERE workspace_id = $1 AND title = $2`,
		testWorkspaceID, "Should be rejected",
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected create still wrote a row (count=%d) — service-layer boundary check failed", count)
	}
}

func TestCreateSubIssueInheritsParentProject(t *testing.T) {
	var projectID, parentID, childID string
	defer func() {
		for _, issueID := range []string{childID, parentID} {
			if issueID == "" {
				continue
			}
			req := newRequest("DELETE", "/api/issues/"+issueID, nil)
			req = withURLParam(req, "id", issueID)
			testHandler.DeleteIssue(httptest.NewRecorder(), req)
		}
		if projectID != "" {
			req := newRequest("DELETE", "/api/projects/"+projectID, nil)
			req = withURLParam(req, "id", projectID)
			testHandler.DeleteProject(httptest.NewRecorder(), req)
		}
	}()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Sub-issue inheritance project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	json.NewDecoder(w.Body).Decode(&project)
	projectID = project.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Parent with project",
		"project_id": projectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parent IssueResponse
	json.NewDecoder(w.Body).Decode(&parent)
	parentID = parent.ID
	if parent.ProjectID == nil || *parent.ProjectID != projectID {
		t.Fatalf("CreateIssue parent: expected project_id %q, got %v", projectID, parent.ProjectID)
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "Child without explicit project",
		"parent_issue_id": parentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue child: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var child IssueResponse
	json.NewDecoder(w.Body).Decode(&child)
	childID = child.ID

	if child.ParentIssueID == nil || *child.ParentIssueID != parentID {
		t.Fatalf("CreateIssue child: expected parent_issue_id %q, got %v", parentID, child.ParentIssueID)
	}
	if child.ProjectID == nil || *child.ProjectID != projectID {
		t.Fatalf("CreateIssue child: expected inherited project_id %q, got %v", projectID, child.ProjectID)
	}
}

func TestCreateSubIssueUsesExplicitProjectOverParentProject(t *testing.T) {
	var parentProjectID, childProjectID, parentID, childID string
	defer func() {
		for _, issueID := range []string{childID, parentID} {
			if issueID == "" {
				continue
			}
			req := newRequest("DELETE", "/api/issues/"+issueID, nil)
			req = withURLParam(req, "id", issueID)
			testHandler.DeleteIssue(httptest.NewRecorder(), req)
		}
		for _, projectID := range []string{childProjectID, parentProjectID} {
			if projectID == "" {
				continue
			}
			req := newRequest("DELETE", "/api/projects/"+projectID, nil)
			req = withURLParam(req, "id", projectID)
			testHandler.DeleteProject(httptest.NewRecorder(), req)
		}
	}()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Parent project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parentProject ProjectResponse
	json.NewDecoder(w.Body).Decode(&parentProject)
	parentProjectID = parentProject.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Child explicit project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject child: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var childProject ProjectResponse
	json.NewDecoder(w.Body).Decode(&childProject)
	childProjectID = childProject.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Parent with project",
		"project_id": parentProjectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parent IssueResponse
	json.NewDecoder(w.Body).Decode(&parent)
	parentID = parent.ID
	if parent.ProjectID == nil || *parent.ProjectID != parentProjectID {
		t.Fatalf("CreateIssue parent: expected project_id %q, got %v", parentProjectID, parent.ProjectID)
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "Child with explicit project",
		"parent_issue_id": parentID,
		"project_id":      childProjectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue child: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var child IssueResponse
	json.NewDecoder(w.Body).Decode(&child)
	childID = child.ID

	if child.ParentIssueID == nil || *child.ParentIssueID != parentID {
		t.Fatalf("CreateIssue child: expected parent_issue_id %q, got %v", parentID, child.ParentIssueID)
	}
	if child.ProjectID == nil || *child.ProjectID != childProjectID {
		t.Fatalf("CreateIssue child: expected explicit project_id %q, got %v", childProjectID, child.ProjectID)
	}
}

func TestCreateIssueRejectsActiveDuplicate(t *testing.T) {
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var projectID, parentID, issueID, duplicateID string
	defer func() {
		for _, id := range []string{duplicateID, issueID, parentID} {
			if id != "" {
				testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, id)
			}
		}
		if projectID != "" {
			testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, projectID)
		}
	}()

	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, $2)
		RETURNING id
	`, testWorkspaceID, "Duplicate guard project "+suffix).Scan(&projectID); err != nil {
		t.Fatalf("create project fixture: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Duplicate guard parent " + suffix,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parent IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&parent); err != nil {
		t.Fatalf("decode parent: %v", err)
	}
	parentID = parent.ID

	title := "SH-PM-SYNTH-01 Synthesize recommendation-to-shortlist planning outputs " + suffix
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           title,
		"status":          "in_progress",
		"parent_issue_id": parentID,
		"project_id":      projectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue original: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var original IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&original); err != nil {
		t.Fatalf("decode original: %v", err)
	}
	issueID = original.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "  sh-pm-synth-01   synthesize recommendation-to-shortlist planning outputs " + suffix + "  ",
		"parent_issue_id": parentID,
		"project_id":      projectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("CreateIssue duplicate: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var conflict struct {
		Code  string        `json:"code"`
		Error string        `json:"error"`
		Issue IssueResponse `json:"issue"`
	}
	if err := json.NewDecoder(w.Body).Decode(&conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Code != "active_duplicate_issue" {
		t.Fatalf("code = %q, want active_duplicate_issue", conflict.Code)
	}
	if conflict.Issue.ID != issueID || conflict.Issue.Status != "in_progress" {
		t.Fatalf("conflict issue = %#v, want original %s in_progress", conflict.Issue, issueID)
	}
	if !strings.Contains(conflict.Error, original.Identifier+" "+title) || !strings.Contains(conflict.Error, "allow_duplicate=true") || !strings.Contains(conflict.Error, "--allow-duplicate") {
		t.Fatalf("unexpected duplicate message: %q", conflict.Error)
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           title,
		"parent_issue_id": parentID,
		"project_id":      projectID,
		"allow_duplicate": true,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue allow duplicate: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var duplicate IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&duplicate); err != nil {
		t.Fatalf("decode duplicate: %v", err)
	}
	duplicateID = duplicate.ID
	if duplicateID == issueID {
		t.Fatalf("allow duplicate returned original issue id %s", duplicateID)
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           title,
		"parent_issue_id": parentID,
		"project_id":      projectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("CreateIssue duplicate after allow-duplicate: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&conflict); err != nil {
		t.Fatalf("decode second conflict: %v", err)
	}
	if conflict.Issue.ID != issueID {
		t.Fatalf("conflict issue = %s, want oldest active issue %s", conflict.Issue.ID, issueID)
	}
}

func TestCreateIssueAllowsDuplicateAfterCancelled(t *testing.T) {
	ctx := context.Background()
	title := fmt.Sprintf("Cancelled duplicate guard %d", time.Now().UnixNano())
	var firstID, secondID string
	defer func() {
		for _, id := range []string{secondID, firstID} {
			if id != "" {
				testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, id)
			}
		}
	}()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  title,
		"status": "cancelled",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue cancelled: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var first IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&first); err != nil {
		t.Fatalf("decode cancelled: %v", err)
	}
	firstID = first.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": title,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue after cancelled: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var second IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	secondID = second.ID
	if secondID == firstID {
		t.Fatalf("new issue reused cancelled issue id %s", secondID)
	}
}

func TestCreateIssueAllowsDuplicateAfterDone(t *testing.T) {
	ctx := context.Background()
	title := fmt.Sprintf("Done duplicate guard %d", time.Now().UnixNano())
	var firstID, secondID string
	defer func() {
		for _, id := range []string{secondID, firstID} {
			if id != "" {
				testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, id)
			}
		}
	}()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  title,
		"status": "done",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue done: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var first IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&first); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	firstID = first.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": title,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue after done: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var second IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	secondID = second.ID
	if secondID == firstID {
		t.Fatalf("new issue reused done issue id %s", secondID)
	}
}

// TestCreateIssueRejectsNonexistentMemberAssignee covers the bug where any
// well-formed UUID was accepted as assignee_id without checking workspace
// membership.
func TestCreateIssueRejectsNonexistentMemberAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Ghost member assignee",
		"assignee_type": "member",
		"assignee_id":   "00000000-0000-0000-0000-000000000000",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for nonexistent member, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueRejectsNonexistentAgentAssignee verifies the same check on
// the agent branch — previously rejected with 403 "agent not found"; we want a
// consistent 400 from the new validator.
func TestCreateIssueRejectsNonexistentAgentAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Ghost agent assignee",
		"assignee_type": "agent",
		"assignee_id":   "00000000-0000-0000-0000-000000000000",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for nonexistent agent, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueRejectsAssigneeTypeWithoutID rejects requests where only one
// of the two fields was supplied — historically this would create an issue
// with an inconsistent state.
func TestCreateIssueRejectsAssigneeTypeWithoutID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Lone assignee_type",
		"assignee_type": "member",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 when only assignee_type is set, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueRejectsAssigneeIDWithoutType is the symmetric case.
func TestCreateIssueRejectsAssigneeIDWithoutType(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":       "Lone assignee_id",
		"assignee_id": testUserID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 when only assignee_id is set, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueRejectsUnknownAssigneeType guards against typos like
// "members" or "user" that previously sneaked through.
func TestCreateIssueRejectsUnknownAssigneeType(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Bogus assignee_type",
		"assignee_type": "user",
		"assignee_id":   testUserID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for unknown assignee_type, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueAcceptsValidMemberAssignee is the positive control — the
// validator must not block legitimate workspace members.
func TestCreateIssueAcceptsValidMemberAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Valid member assignee",
		"assignee_type": "member",
		"assignee_id":   testUserID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201 for valid member assignee, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

// TestCreateIssueRejectsMalformedAssigneeID covers the case where parseUUID
// silently produces an invalid pgtype.UUID and the validator would otherwise
// treat (no type + unparseable id) as "no assignee" and accept the request.
func TestCreateIssueRejectsMalformedAssigneeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":       "Malformed assignee_id only",
		"assignee_id": "not-a-uuid",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for malformed assignee_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateIssueRejectsMalformedAttachmentIDBeforeWrite(t *testing.T) {
	var before int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id = $1`, testWorkspaceID).Scan(&before); err != nil {
		t.Fatalf("count issues before: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":          "Malformed attachment issue",
		"attachment_ids": []string{"not-a-uuid"},
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for malformed attachment_ids, got %d: %s", w.Code, w.Body.String())
	}

	var after int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id = $1`, testWorkspaceID).Scan(&after); err != nil {
		t.Fatalf("count issues after: %v", err)
	}
	if after != before {
		t.Fatalf("CreateIssue: malformed attachment_ids should not create issue, count before=%d after=%d", before, after)
	}
}

// TestUpdateIssueRejectsMalformedAssigneeID is the equivalent for the update
// path, where the same parseUUID-shaped gap existed on a previously-unassigned
// issue.
func TestUpdateIssueRejectsMalformedAssigneeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Update malformed assignee target",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	defer func() {
		cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
		cleanupReq = withURLParam(cleanupReq, "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
	}()

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"assignee_id": "not-a-uuid",
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateIssue: expected 400 for malformed assignee_id, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateIssueRejectsNonexistentMemberAssignee verifies the same gap is
// closed on the update path — UpdateIssue previously only validated agents.
func TestUpdateIssueRejectsNonexistentMemberAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Update assignee target",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	defer func() {
		cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
		cleanupReq = withURLParam(cleanupReq, "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
	}()

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"assignee_type": "member",
		"assignee_id":   "00000000-0000-0000-0000-000000000000",
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateIssue: expected 400 for nonexistent member, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateIssueAllowsExplicitUnassign verifies that sending null for both
// fields still works after the new validator landed — clearing the assignee
// must not be misclassified as a mismatched pair.
func TestUpdateIssueAllowsExplicitUnassign(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Issue to unassign",
		"assignee_type": "member",
		"assignee_id":   testUserID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	defer func() {
		cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
		cleanupReq = withURLParam(cleanupReq, "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
	}()

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"assignee_type": nil,
		"assignee_id":   nil,
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200 for unassign, got %d: %s", w.Code, w.Body.String())
	}
	var updated IssueResponse
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.AssigneeType != nil || updated.AssigneeID != nil {
		t.Fatalf("UpdateIssue: expected assignee cleared, got type=%v id=%v", updated.AssigneeType, updated.AssigneeID)
	}
}

func TestCommentCRUD(t *testing.T) {
	// Create an issue first
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Comment test issue",
	})
	testHandler.CreateIssue(w, req)
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	issueID := issue.ID

	// Create comment
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "Test comment from Go test",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// List comments
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueID+"/comments", nil)
	req = withURLParam(req, "id", issueID)
	testHandler.ListComments(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListComments: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var comments []CommentResponse
	json.NewDecoder(w.Body).Decode(&comments)
	if len(comments) != 1 {
		t.Fatalf("ListComments: expected 1 comment, got %d", len(comments))
	}
	if comments[0].Content != "Test comment from Go test" {
		t.Fatalf("ListComments: expected content 'Test comment from Go test', got '%s'", comments[0].Content)
	}

	// Cleanup
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+issueID, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.DeleteIssue(w, req)
}

func TestCommentWritePathsPreserveIssueIdentifiers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("requires DB")
	}

	ctx := context.Background()
	setWorkspaceIssuePrefixForTest(t, "MUL")

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_type, creator_id, title, number)
		VALUES ($1, 'member', $2, $3, 3310)
		RETURNING id
	`, testWorkspaceID, testUserID, "preserve bare issue identifiers").Scan(&issueID); err != nil {
		t.Fatalf("create issue fixture: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	explicitMention := fmt.Sprintf("[MUL-3310](mention://issue/%s)", issueID)
	createCases := []string{
		"MUL-3310",
		"issue/MUL-3310",
		"feature/MUL-3310",
		explicitMention,
	}

	var firstCommentID string
	for _, content := range createCases {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
			"content": content,
		})
		req = withURLParam(req, "id", issueID)
		testHandler.CreateComment(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateComment(%q): expected 201, got %d: %s", content, w.Code, w.Body.String())
		}

		var created CommentResponse
		if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
			t.Fatalf("decode created comment: %v", err)
		}
		if created.Content != content {
			t.Fatalf("CreateComment(%q) stored %q", content, created.Content)
		}
		if firstCommentID == "" {
			firstCommentID = created.ID
		}
	}

	updatedContent := "updated MUL-3310 issue/MUL-3310 feature/MUL-3310 " + explicitMention
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/comments/"+firstCommentID, map[string]any{
		"content": updatedContent,
	})
	req = withURLParam(req, "commentId", firstCommentID)
	testHandler.UpdateComment(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateComment: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated comment: %v", err)
	}
	if updated.Content != updatedContent {
		t.Fatalf("UpdateComment stored %q, want %q", updated.Content, updatedContent)
	}
}

func TestCreateCommentRejectsMalformedParentID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Comment malformed parent issue",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+issue.ID+"/comments", map[string]any{
		"content":   "bad parent",
		"parent_id": "not-a-uuid",
	})
	req = withURLParam(req, "id", issue.ID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateComment: expected 400 for malformed parent_id, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+issue.ID, nil)
	req = withURLParam(req, "id", issue.ID)
	testHandler.DeleteIssue(w, req)
}

func TestUpdateAgentRejectsMalformedAgentID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/not-a-uuid", map[string]any{
		"name": "Malformed agent id",
	})
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgent: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateAgentRejectsMalformedRuntimeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/agents", map[string]any{
		"name":       "Malformed runtime agent",
		"runtime_id": "not-a-uuid",
	})
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateAgent: expected 400 for malformed runtime_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateAgentRejectsMalformedRuntimeID(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Malformed Runtime Update", nil)

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"runtime_id": "not-a-uuid",
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgent: expected 400 for malformed runtime_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateWorkspaceRejectsMalformedID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/workspaces/not-a-uuid", map[string]any{
		"name": "Malformed workspace id",
	})
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.UpdateWorkspace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateWorkspace: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateCommentRejectsMalformedCommentID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/comments/not-a-uuid", map[string]any{
		"content": "updated",
	})
	req = withURLParam(req, "commentId", "not-a-uuid")
	testHandler.UpdateComment(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateComment: expected 400 for malformed commentId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRevokePersonalAccessTokenRejectsMalformedID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/tokens/not-a-uuid", nil)
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.RevokePersonalAccessToken(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("RevokePersonalAccessToken: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequestBodyUUIDFieldsRejectMalformed(t *testing.T) {
	tests := []struct {
		name   string
		req    *http.Request
		handle func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "daemon register workspace_id",
			req: newRequest("POST", "/api/daemon/register", map[string]any{
				"workspace_id": "not-a-uuid",
				"daemon_id":    "daemon-malformed-workspace",
				"runtimes": []map[string]any{
					{"name": "codex", "type": "codex", "status": "online"},
				},
			}),
			handle: testHandler.DaemonRegister,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tt.handle(w, tt.req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400 for malformed body UUID, got %d: %s", tt.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestDaemonDeregisterRejectsMalformedRuntimeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/deregister", map[string]any{
		"runtime_ids": []string{"not-a-uuid"},
	})
	testHandler.DaemonDeregister(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("DaemonDeregister: expected 400 for malformed runtime_ids, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetIssueGCCheckRejectsMalformedIssueID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/daemon/issues/not-a-uuid/gc-check", nil)
	req = withURLParam(req, "issueId", "not-a-uuid")
	testHandler.GetIssueGCCheck(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("GetIssueGCCheck: expected 400 for malformed issueId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBatchIssueGCCheckRejectsInvalidRequests(t *testing.T) {
	h := &Handler{}
	workspaceID := "00000000-0000-0000-0000-000000000001"
	tooMany := make([]string, maxIssueGCBatchSize+1)
	for i := range tooMany {
		tooMany[i] = "00000000-0000-0000-0000-000000000002"
	}

	tests := []struct {
		name        string
		workspaceID string
		body        any
	}{
		{name: "malformed workspace", workspaceID: "not-a-uuid", body: map[string]any{"issue_ids": []string{}}},
		{name: "malformed issue", workspaceID: workspaceID, body: map[string]any{"issue_ids": []string{"not-a-uuid"}}},
		{name: "too many issues", workspaceID: workspaceID, body: map[string]any{"issue_ids": tooMany}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newDaemonTokenRequest("POST", "/api/daemon/workspaces/"+tt.workspaceID+"/issues/gc-check", tt.body,
				tt.workspaceID, "test-daemon")
			req = withURLParam(req, "workspaceId", tt.workspaceID)
			h.BatchIssueGCCheck(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestSetAgentSkillsRejectsMalformedSkillID(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Malformed Skill Assignment", nil)

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID+"/skills", map[string]any{
		"skill_ids": []string{"not-a-uuid"},
	})
	req = withURLParam(req, "id", agentID)
	testHandler.SetAgentSkills(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("SetAgentSkills: expected 400 for malformed skill_ids, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAddAgentSkillsPreservesExistingAssignments(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Add Skill Preserves Existing", nil)
	existingSkillID := insertHandlerTestSkill(t, "add-preserve-existing", "existing body")
	newSkillID := insertHandlerTestSkill(t, "add-preserve-new", "new body")

	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`,
		agentID, existingSkillID,
	); err != nil {
		t.Fatalf("seed existing skill assignment: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/agents/"+agentID+"/skills/add", map[string]any{
		"skill_ids": []string{newSkillID},
	})
	req = withURLParam(req, "id", agentID)
	testHandler.AddAgentSkills(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("AddAgentSkills: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []SkillSummaryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	assertSkillIDsPresent(t, resp, existingSkillID, newSkillID)
	assertAgentSkillRowCount(t, agentID, 2)
}

func TestAddAgentSkillsAddsMultipleAndIsIdempotent(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Add Multiple Skills", nil)
	skillA := insertHandlerTestSkill(t, "add-multiple-a", "a body")
	skillB := insertHandlerTestSkill(t, "add-multiple-b", "b body")

	for attempt := 0; attempt < 2; attempt++ {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/agents/"+agentID+"/skills/add", map[string]any{
			"skill_ids": []string{skillA, skillB},
		})
		req = withURLParam(req, "id", agentID)
		testHandler.AddAgentSkills(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("AddAgentSkills attempt %d: expected 200, got %d: %s", attempt+1, w.Code, w.Body.String())
		}
	}

	assertAgentSkillRowCount(t, agentID, 2)
}

func TestAddAgentSkillsRejectsMalformedSkillID(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Add Malformed Skill Assignment", nil)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/agents/"+agentID+"/skills/add", map[string]any{
		"skill_ids": []string{"not-a-uuid"},
	})
	req = withURLParam(req, "id", agentID)
	testHandler.AddAgentSkills(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("AddAgentSkills: expected 400 for malformed skill_ids, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAddAgentSkillsRejectsCrossWorkspaceSkillID(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Add Cross Workspace Skill", nil)
	foreignSkillID := insertHandlerTestSkillInForeignWorkspace(t, "add-cross-workspace", "foreign body")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/agents/"+agentID+"/skills/add", map[string]any{
		"skill_ids": []string{foreignSkillID},
	})
	req = withURLParam(req, "id", agentID)
	testHandler.AddAgentSkills(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("AddAgentSkills: expected 404 for cross-workspace skill_id, got %d: %s", w.Code, w.Body.String())
	}
	assertAgentSkillRowCount(t, agentID, 0)
}

func insertHandlerTestSkillInForeignWorkspace(t *testing.T, namePrefix, content string) string {
	t.Helper()
	ctx := context.Background()
	slug := "foreign-skill-" + strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-"))

	var workspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Foreign Skill Workspace "+t.Name(), slug, "", "FSW").Scan(&workspaceID); err != nil {
		t.Fatalf("insert foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})

	name := namePrefix + "-" + t.Name()
	var skillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, $3, $4, '{}'::jsonb, $5)
		RETURNING id
	`, workspaceID, name, "fixture", content, testUserID).Scan(&skillID); err != nil {
		t.Fatalf("insert foreign skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, skillID)
	})
	return skillID
}

func assertSkillIDsPresent(t *testing.T, skills []SkillSummaryResponse, wantIDs ...string) {
	t.Helper()
	got := make(map[string]bool, len(skills))
	for _, s := range skills {
		got[s.ID] = true
	}
	for _, want := range wantIDs {
		if !got[want] {
			t.Fatalf("response missing skill %s; got %+v", want, skills)
		}
	}
}

func assertAgentSkillRowCount(t *testing.T, agentID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_skill WHERE agent_id = $1`,
		agentID,
	).Scan(&got); err != nil {
		t.Fatalf("count agent_skill: %v", err)
	}
	if got != want {
		t.Fatalf("agent_skill row count: got %d, want %d", got, want)
	}
}

func TestAgentCRUD(t *testing.T) {
	// List agents
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/agents?workspace_id="+testWorkspaceID, nil)
	testHandler.ListAgents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgents: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var agents []AgentResponse
	json.NewDecoder(w.Body).Decode(&agents)
	if len(agents) == 0 {
		t.Fatal("ListAgents: expected at least 1 agent")
	}

	// Update agent status
	agentID := agents[0].ID
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"status": "idle",
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated AgentResponse
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Status != "idle" {
		t.Fatalf("UpdateAgent: expected status 'idle', got '%s'", updated.Status)
	}
	if updated.Name != agents[0].Name {
		t.Fatalf("UpdateAgent: name should be preserved, got '%s'", updated.Name)
	}
}

func TestUpdateAgentMcpConfigAbsentPreservesValue(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Mcp Preserve", []byte(`{"preset":"keep"}`))

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"name": "Handler Mcp Preserve Updated",
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateAgent: decode response: %v", err)
	}
	assertJSONEqual(t, updated.McpConfig, `{"preset":"keep"}`)
	assertJSONEqual(t, fetchAgentMcpConfig(t, agentID), `{"preset":"keep"}`)
}

func TestUpdateAgentMcpConfigNullClearsValue(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Mcp Clear", []byte(`{"preset":"clear"}`))

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"mcp_config": nil,
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateAgent: decode response: %v", err)
	}
	assertJSONEqual(t, updated.McpConfig, `null`)
	if fetchAgentMcpConfig(t, agentID) != nil {
		t.Fatalf("UpdateAgent: expected DB mcp_config to be SQL NULL")
	}
}

func TestUpdateAgentMcpConfigObjectUpdatesValue(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Mcp Update", []byte(`{"preset":"old"}`))

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"mcp_config": map[string]any{"preset": "new"},
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateAgent: decode response: %v", err)
	}
	assertJSONEqual(t, updated.McpConfig, `{"preset":"new"}`)
	assertJSONEqual(t, fetchAgentMcpConfig(t, agentID), `{"preset":"new"}`)
}

func TestCreateAgentMcpConfigNullStoresSQLNull(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/agents", map[string]any{
		"name":        "Handler Mcp Create Null",
		"runtime_id":  handlerTestRuntimeID(t),
		"mcp_config":  nil,
		"custom_env":  map[string]string{},
		"custom_args": []string{},
	})
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgent: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("CreateAgent: decode response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.ID)
	})

	assertJSONEqual(t, created.McpConfig, `null`)
	if fetchAgentMcpConfig(t, created.ID) != nil {
		t.Fatalf("CreateAgent: expected DB mcp_config to be SQL NULL")
	}
}

func TestGetWorkspace(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces/"+testWorkspaceID, nil)
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.GetWorkspace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetWorkspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestResolveActor(t *testing.T) {
	ctx := context.Background()

	// Look up the agent created by the test fixture.
	var agentID string
	err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// Create a task for the agent so we can test X-Task-ID validation.
	var issueID string
	err = testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		 VALUES ($1, 'resolveActor test', 'todo', 'none', 'member', $2, 9999, 0)
		 RETURNING id`, testWorkspaceID, testUserID,
	).Scan(&issueID)
	if err != nil {
		t.Fatalf("failed to create test issue: %v", err)
	}

	// Look up runtime_id for the agent.
	var runtimeID string
	err = testPool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1`, agentID).Scan(&runtimeID)
	if err != nil {
		t.Fatalf("failed to get agent runtime_id: %v", err)
	}

	var taskID string
	err = testPool.QueryRow(ctx,
		`INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		 VALUES ($1, $2, $3, 'queued', 0)
		 RETURNING id`, agentID, runtimeID, issueID,
	).Scan(&taskID)
	if err != nil {
		t.Fatalf("failed to create test task: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	tests := []struct {
		name          string
		agentIDHeader string
		taskIDHeader  string
		wantActorType string
		wantIsAgent   bool
	}{
		{
			name:          "no headers returns member",
			wantActorType: "member",
		},
		{
			// X-Agent-ID without X-Task-ID is not trusted — otherwise a
			// workspace member who guesses an agent's UUID could impersonate
			// it and bypass the private-agent gate. See resolveActor for the
			// rationale.
			name:          "agent ID without task ID returns member",
			agentIDHeader: agentID,
			wantActorType: "member",
		},
		{
			name:          "non-existent agent ID with task returns member",
			agentIDHeader: "00000000-0000-0000-0000-000000000099",
			taskIDHeader:  taskID,
			wantActorType: "member",
		},
		{
			name:          "valid agent + valid task returns agent",
			agentIDHeader: agentID,
			taskIDHeader:  taskID,
			wantActorType: "agent",
			wantIsAgent:   true,
		},
		{
			name:          "valid agent + wrong task returns member",
			agentIDHeader: agentID,
			taskIDHeader:  "00000000-0000-0000-0000-000000000099",
			wantActorType: "member",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newRequest("GET", "/test", nil)
			if tt.agentIDHeader != "" {
				req.Header.Set("X-Agent-ID", tt.agentIDHeader)
			}
			if tt.taskIDHeader != "" {
				req.Header.Set("X-Task-ID", tt.taskIDHeader)
			}

			actorType, actorID := testHandler.resolveActor(req, testUserID, testWorkspaceID)

			if actorType != tt.wantActorType {
				t.Errorf("actorType = %q, want %q", actorType, tt.wantActorType)
			}
			if tt.wantIsAgent {
				if actorID != tt.agentIDHeader {
					t.Errorf("actorID = %q, want agent %q", actorID, tt.agentIDHeader)
				}
			} else {
				if actorID != testUserID {
					t.Errorf("actorID = %q, want user %q", actorID, testUserID)
				}
			}
		})
	}
}

// TestBacklogNoTriggerOnCreate verifies that creating a backlog issue with an
// agent assignee does NOT enqueue a task — backlog is a parking lot.
func TestBacklogNoTriggerOnCreate(t *testing.T) {
	ctx := context.Background()

	var agentID string
	err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Backlog no-trigger test",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)

	var taskCount int
	err = testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`,
		created.ID,
	).Scan(&taskCount)
	if err != nil {
		t.Fatalf("failed to count tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("expected no tasks for backlog issue on creation, got %d", taskCount)
	}

	// Cleanup
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

// TestBacklogToTodoDoesNotTriggerAgent verifies that moving an agent-assigned
// generic Issue from backlog to todo remains metadata-only.
func TestBacklogToTodoDoesNotTriggerAgent(t *testing.T) {
	ctx := context.Background()

	var agentID string
	err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// Create a backlog issue assigned to the agent — should NOT trigger.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Backlog trigger test",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)

	// Move the issue from backlog to todo — ownership remains metadata-only.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"status": "todo",
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Generic Issue status changes cannot create executable work.
	var taskCount int
	err = testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
		created.ID, agentID,
	).Scan(&taskCount)
	if err != nil {
		t.Fatalf("failed to count tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("generic backlog->todo transition enqueued %d tasks", taskCount)
	}

	// Cleanup
	testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, created.ID)
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

// TestBacklogToTodoByAgentDoesNotTriggerDifferentAssignee verifies agent-authored
// Issue updates cannot bypass Mission Commands to start another agent.
func TestBacklogToTodoByAgentDoesNotTriggerDifferentAssignee(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Parent agent (the actor) + child agent (the assignee).
	parentAgent := createHandlerTestAgent(t, "Backlog Parent Agent", nil)
	childAgent := createHandlerTestAgent(t, "Backlog Child Agent", nil)
	parentTask := createHandlerTestTaskForAgent(t, parentAgent)

	// Create a backlog issue assigned to the child agent — should NOT trigger
	// on creation (backlog parking-lot rule).
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Serial sub-task Step 2",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   childAgent,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	// Parent agent promotes backlog → todo on behalf of its current task. This
	// changes Issue metadata only.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{"status": "todo"})
	req = withURLParam(req, "id", created.ID)
	req.Header.Set("X-Agent-ID", parentAgent)
	req.Header.Set("X-Task-ID", parentTask)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var childTasks int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
		created.ID, childAgent,
	).Scan(&childTasks); err != nil {
		t.Fatalf("failed to count child tasks: %v", err)
	}
	if childTasks != 0 {
		t.Fatalf("agent-driven generic Issue update enqueued %d child tasks", childTasks)
	}
}

// TestBacklogToTodoByAgentSameIssueDoesNotSelfTrigger verifies the
// task-issue-scoped self-loop guard: an agent whose CURRENT task is
// running on issue I and who flips I from backlog to an active status
// must NOT enqueue itself for I again. Without this guard the agent
// would re-trigger every cycle it completed on I and immediately
// re-enter the same path.
//
// This is the true self-loop case (calling task is on the SAME issue
// being promoted). The complementary case — same agent, DIFFERENT
// issue — is the documented serial chain and is covered by
// TestBacklogToTodoByAgentSameAgentDifferentIssue.
func TestBacklogToTodoByAgentSameIssueDoesNotSelfTrigger(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	selfAgent := createHandlerTestAgent(t, "Backlog Self Agent", nil)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Self-promoted backlog",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   selfAgent,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	// Task bound to the SAME issue being promoted — true self-loop.
	selfTask := createHandlerTestTaskForAgentOnIssue(t, selfAgent, created.ID)

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{"status": "todo"})
	req = withURLParam(req, "id", created.ID)
	req.Header.Set("X-Agent-ID", selfAgent)
	req.Header.Set("X-Task-ID", selfTask)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var tasks int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
		created.ID, selfAgent,
	).Scan(&tasks); err != nil {
		t.Fatalf("failed to count tasks: %v", err)
	}
	if tasks != 0 {
		t.Fatalf("expected no self-trigger when agent promotes the same issue its task is running on, got %d queued tasks", tasks)
	}
}

// TestBacklogToTodoByAgentSameAgentDifferentIssueDoesNotTrigger verifies even
// a cross-Issue handoff must use a Mission Command after Wave 1B.2.
func TestBacklogToTodoByAgentSameAgentDifferentIssueDoesNotTrigger(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "Backlog Same-Agent Chain", nil)

	// Step 1 issue — the one the agent is currently working on.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Step 1 (running)",
		"status":        "in_progress",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue step1: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var step1 IssueResponse
	json.NewDecoder(w.Body).Decode(&step1)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, step1.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, step1.ID)
	})

	// Step 2 issue — backlog, also assigned to the same agent.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Step 2 (backlog)",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue step2: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var step2 IssueResponse
	json.NewDecoder(w.Body).Decode(&step2)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, step2.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, step2.ID)
	})

	// Task is running on step1, but promoting step2 remains metadata-only.
	step1Task := createHandlerTestTaskForAgentOnIssue(t, agentID, step1.ID)

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+step2.ID, map[string]any{"status": "todo"})
	req = withURLParam(req, "id", step2.ID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", step1Task)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue step2: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var step2Tasks int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
		step2.ID, agentID,
	).Scan(&step2Tasks); err != nil {
		t.Fatalf("failed to count step2 tasks: %v", err)
	}
	if step2Tasks != 0 {
		t.Fatalf("cross-Issue generic promotion enqueued %d tasks", step2Tasks)
	}
}

// TestAssignIssueToSelfWithActiveTargetRunDoesNotDuplicate covers the direct
// assignment form of #6947: an agent already working on an issue may claim its
// ownership, but that ownership write must not enqueue a second run for the
// same (issue, agent) pair.
func TestAssignIssueToSelfWithActiveTargetRunDoesNotDuplicate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Self Assign Active Target", nil)

	issue := createIssueForTest(t, map[string]any{"title": "self-assign active target", "status": "todo"})
	runningTask := createHandlerTestTaskForAgentOnIssue(t, agentID, issue.ID)

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
		"assignee_type": "agent",
		"assignee_id":   agentID,
	}), "id", issue.ID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", runningTask)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := queuedTaskCountFor(t, issue.ID, agentID); got != 0 {
		t.Fatalf("self-assignment duplicated an active target run: got %d queued task(s)", got)
	}
	if got := taskStatus(t, runningTask); got != "running" {
		t.Fatalf("existing target run must survive ownership claim, got status %q", got)
	}
}

// TestAssignDifferentIssueToSelfDoesNotEnqueue locks in the Wave 1B.2 rule for
// cross-Issue ownership changes.
func TestAssignDifferentIssueToSelfDoesNotEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Cross Issue Self Assign", nil)
	source := createIssueForTest(t, map[string]any{"title": "cross-issue source", "status": "todo"})
	target := createIssueForTest(t, map[string]any{"title": "cross-issue target", "status": "todo"})
	sourceTask := createHandlerTestTaskForAgentOnIssue(t, agentID, source.ID)

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/issues/"+target.ID, map[string]any{
		"assignee_type": "agent",
		"assignee_id":   agentID,
	}), "id", target.ID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", sourceTask)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := queuedTaskCountFor(t, target.ID, agentID); got != 0 {
		t.Fatalf("cross-issue self-assignment enqueued %d runs", got)
	}
}

// TestBatchAssignFreshIssuesToSelfDoesNotEnqueue verifies the batch path does
// not retain a second direct-agent producer.
func TestBatchAssignFreshIssuesToSelfDoesNotEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Batch Fresh Self Assign", nil)
	source := createIssueForTest(t, map[string]any{"title": "batch source", "status": "todo"})
	target1 := createIssueForTest(t, map[string]any{"title": "batch target one", "status": "todo"})
	target2 := createIssueForTest(t, map[string]any{"title": "batch target two", "status": "todo"})
	sourceTask := createHandlerTestTaskForAgentOnIssue(t, agentID, source.ID)

	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
		"issue_ids": []string{target1.ID, target2.ID},
		"updates": map[string]any{
			"assignee_type": "agent",
			"assignee_id":   agentID,
		},
	})
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", sourceTask)
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("BatchUpdateIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	for _, target := range []IssueResponse{target1, target2} {
		if got := queuedTaskCountFor(t, target.ID, agentID); got != 0 {
			t.Fatalf("fresh target %s enqueued %d runs", target.ID, got)
		}
	}
}

// TestAssignActiveIssueToDifferentAgentIsOwnershipOnly verifies the old task
// survives while the new assignee does not start outside Orchestrator.
func TestAssignActiveIssueToDifferentAgentIsOwnershipOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	actorAgent := createHandlerTestAgent(t, "Active Transfer Actor", nil)
	targetAgent := createHandlerTestAgent(t, "Active Transfer Target", nil)
	issue := createIssueForTest(t, map[string]any{"title": "active transfer", "status": "todo"})
	actorTask := createHandlerTestTaskForAgentOnIssue(t, actorAgent, issue.ID)

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
		"assignee_type": "agent",
		"assignee_id":   targetAgent,
	}), "id", issue.ID)
	req.Header.Set("X-Agent-ID", actorAgent)
	req.Header.Set("X-Task-ID", actorTask)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := queuedTaskCountFor(t, issue.ID, targetAgent); got != 0 {
		t.Fatalf("new assignee received %d generic queued runs", got)
	}
	if got := taskStatus(t, actorTask); got != "running" {
		t.Fatalf("old active task must survive transfer, got status %q", got)
	}
}

// TestBatchBacklogToTodoByAgentDoesNotTriggerAssignee mirrors the Wave 1B.2
// boundary on BatchUpdateIssues.
func TestBatchBacklogToTodoByAgentDoesNotTriggerAssignee(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	parentAgent := createHandlerTestAgent(t, "Batch Parent Agent", nil)
	childAgent := createHandlerTestAgent(t, "Batch Child Agent", nil)
	parentTask := createHandlerTestTaskForAgent(t, parentAgent)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Batch backlog child",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   childAgent,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	// Drive the batch endpoint with the same agent identity headers.
	w = httptest.NewRecorder()
	req = newRequest("PATCH", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
		"issue_ids": []string{created.ID},
		"updates":   map[string]any{"status": "todo"},
	})
	req.Header.Set("X-Agent-ID", parentAgent)
	req.Header.Set("X-Task-ID", parentTask)
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("BatchUpdateIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var childTasks int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
		created.ID, childAgent,
	).Scan(&childTasks); err != nil {
		t.Fatalf("failed to count child tasks: %v", err)
	}
	if childTasks != 0 {
		t.Fatalf("batch generic Issue update enqueued %d child tasks", childTasks)
	}
}

func TestDaemonRegisterMissingWorkspaceReturns404(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/daemon/register", bytes.NewBufferString(`{
		"workspace_id":"00000000-0000-0000-0000-000000000001",
		"daemon_id":"local-daemon",
		"device_name":"test-machine",
		"runtimes":[{"name":"Local Codex","type":"codex","version":"1.0.0","status":"online"}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)

	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("DaemonRegister: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "workspace not found") {
		t.Fatalf("DaemonRegister: expected workspace not found error, got %s", w.Body.String())
	}
}

// TestNestedMemberReplyUnderMemberSkipsAssigneeFallback verifies that a nested
// reply whose direct parent is human-owned neither routes to a sibling agent
// reply nor falls back to the issue assignee. A sibling agent comment alone
// does not establish a conversation owner for the member-authored root.
func TestNestedMemberReplyUnderMemberSkipsAssigneeFallback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	assigneeAgent := createHandlerTestAgent(t, "Nested Participation Assignee", nil)

	var number int
	if err := testPool.QueryRow(ctx, `
		UPDATE workspace
		SET issue_counter = GREATEST(issue_counter, (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)) + 1
		WHERE id = $1 RETURNING issue_counter
	`, testWorkspaceID).Scan(&number); err != nil {
		t.Fatalf("next issue number: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_type, creator_id, title, assignee_type, assignee_id, number)
		VALUES ($1, 'member', $2, $3, 'agent', $4, $5)
		RETURNING id
	`, testWorkspaceID, testUserID, "nested participation regression", assigneeAgent, number).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	countAssigneeQueued := func() int {
		t.Helper()
		var n int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM agent_task_queue
			WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'
		`, issueID, assigneeAgent).Scan(&n); err != nil {
			t.Fatalf("count queued tasks: %v", err)
		}
		return n
	}
	postMemberComment := func(body map[string]any) CommentResponse {
		t.Helper()
		w := httptest.NewRecorder()
		r := newRequest("POST", "/api/issues/"+issueID+"/comments", body)
		r = withURLParam(r, "id", issueID)
		testHandler.CreateComment(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var resp CommentResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode comment response: %v", err)
		}
		return resp
	}

	var rootID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, 'this cache question is for humans')
		RETURNING id
	`, testWorkspaceID, issueID, testUserID).Scan(&rootID); err != nil {
		t.Fatalf("insert root comment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content, parent_id)
		VALUES ($1, $2, 'agent', $3, 'expiration policy is the issue', $4)
	`, testWorkspaceID, issueID, assigneeAgent, rootID); err != nil {
		t.Fatalf("insert assignee reply: %v", err)
	}
	var humanParentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content, parent_id)
		VALUES ($1, $2, 'member', $3, 'I have seen this too', $4)
		RETURNING id
	`, testWorkspaceID, issueID, testUserID, rootID).Scan(&humanParentID); err != nil {
		t.Fatalf("insert human direct parent: %v", err)
	}

	nested := postMemberComment(map[string]any{
		"content":   "what should the expiration be?",
		"parent_id": humanParentID,
	})
	if nested.ParentID == nil || *nested.ParentID != humanParentID {
		t.Fatalf("stored nested reply parent_id should keep direct parent %s, got %v", humanParentID, nested.ParentID)
	}
	if got := countAssigneeQueued(); got != 0 {
		t.Fatalf("plain nested human reply queued assignee tasks = %d, want 0", got)
	}
}

func TestCreateSkillSkipsSkillMdFile(t *testing.T) {
	if testPool == nil {
		t.Skip("no database available")
	}

	req := newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/skills", CreateSkillRequest{
		Name:    "test-skill-create-skip-skillmd",
		Content: "# SKILL.md content",
		Files: []CreateSkillFileRequest{
			{Path: "README.md", Content: "readme"},
			{Path: "SKILL.md", Content: "should be skipped"},
			{Path: "helper.go", Content: "package main"},
		},
	})
	rec := httptest.NewRecorder()
	testHandler.CreateSkill(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp SkillWithFilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	// Should only have README.md and helper.go, not SKILL.md
	if len(resp.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(resp.Files))
	}
	for _, f := range resp.Files {
		if strings.EqualFold(f.Path, "SKILL.md") {
			t.Fatalf("SKILL.md should not be in response files")
		}
	}

	// Verify DB state directly
	ctx := context.Background()
	rows, err := testPool.Query(ctx, "SELECT path FROM skill_file WHERE skill_id = $1", resp.ID)
	if err != nil {
		t.Fatalf("query skill_file: %v", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan path: %v", err)
		}
		paths = append(paths, p)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 rows in skill_file, got %d", len(paths))
	}
	for _, p := range paths {
		if strings.EqualFold(p, "SKILL.md") {
			t.Fatalf("SKILL.md should not be stored in skill_file")
		}
	}
}

func TestUpdateSkillSkipsSkillMdFile(t *testing.T) {
	if testPool == nil {
		t.Skip("no database available")
	}

	// Create a skill first
	req := newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/skills", CreateSkillRequest{
		Name:    "test-skill-update-skip-skillmd",
		Content: "# SKILL.md content",
		Files: []CreateSkillFileRequest{
			{Path: "README.md", Content: "readme"},
		},
	})
	rec := httptest.NewRecorder()
	testHandler.CreateSkill(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create skill: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var createResp SkillWithFilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	// Update with SKILL.md in files
	updateReq := newRequest(http.MethodPut, "/api/skills/"+createResp.ID, UpdateSkillRequest{
		Name:    strPtr("updated-name"),
		Content: strPtr("updated content"),
		Files: []CreateSkillFileRequest{
			{Path: "README.md", Content: "updated readme"},
			{Path: "SKILL.md", Content: "should be skipped"},
			{Path: "new.go", Content: "package main"},
		},
	})
	updateReq = withURLParam(updateReq, "id", createResp.ID)
	updateRec := httptest.NewRecorder()
	testHandler.UpdateSkill(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("update skill: expected 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	var updateResp SkillWithFilesResponse
	if err := json.Unmarshal(updateRec.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("unmarshal update response: %v", err)
	}

	if len(updateResp.Files) != 2 {
		t.Fatalf("expected 2 files after update, got %d", len(updateResp.Files))
	}
	for _, f := range updateResp.Files {
		if strings.EqualFold(f.Path, "SKILL.md") {
			t.Fatalf("SKILL.md should not be in updated response files")
		}
	}

	// Verify DB state
	ctx := context.Background()
	rows, err := testPool.Query(ctx, "SELECT path FROM skill_file WHERE skill_id = $1", createResp.ID)
	if err != nil {
		t.Fatalf("query skill_file: %v", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan path: %v", err)
		}
		paths = append(paths, p)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 rows in skill_file after update, got %d", len(paths))
	}
	for _, p := range paths {
		if strings.EqualFold(p, "SKILL.md") {
			t.Fatalf("SKILL.md should not be stored in skill_file after update")
		}
	}
}

func strPtr(s string) *string {
	return &s
}

func TestUpsertSkillFileRejectsSkillMd(t *testing.T) {
	if testPool == nil {
		t.Skip("no database available")
	}

	// Create a skill first
	req := newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/skills", CreateSkillRequest{
		Name:    "test-skill-upsert-reject-skillmd",
		Content: "# SKILL.md content",
	})
	rec := httptest.NewRecorder()
	testHandler.CreateSkill(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create skill: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var createResp SkillWithFilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	// Try to upsert SKILL.md
	upsertReq := newRequest(http.MethodPut, "/api/skills/"+createResp.ID+"/files", CreateSkillFileRequest{
		Path:    "SKILL.md",
		Content: "should be rejected",
	})
	upsertReq = withURLParam(upsertReq, "id", createResp.ID)
	upsertRec := httptest.NewRecorder()
	testHandler.UpsertSkillFile(upsertRec, upsertReq)

	if upsertRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", upsertRec.Code, upsertRec.Body.String())
	}
	if !strings.Contains(upsertRec.Body.String(), "SKILL.md is reserved") {
		t.Fatalf("expected error message about reserved SKILL.md, got: %s", upsertRec.Body.String())
	}
}
