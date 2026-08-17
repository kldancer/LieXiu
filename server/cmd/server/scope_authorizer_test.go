package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/realtime"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// fakeScopeQuerier implements scopeAuthQuerier with in-memory maps.
type fakeScopeQuerier struct {
	tasks  map[[16]byte]db.AgentTaskQueue
	issues map[[16]byte]db.Issue
}

func (f *fakeScopeQuerier) GetAgentTask(_ context.Context, id pgtype.UUID) (db.AgentTaskQueue, error) {
	if t, ok := f.tasks[id.Bytes]; ok {
		return t, nil
	}
	return db.AgentTaskQueue{}, pgx.ErrNoRows
}
func (f *fakeScopeQuerier) GetIssue(_ context.Context, id pgtype.UUID) (db.Issue, error) {
	if i, ok := f.issues[id.Bytes]; ok {
		return i, nil
	}
	return db.Issue{}, pgx.ErrNoRows
}

func mustUUID(t *testing.T) (string, pgtype.UUID) {
	t.Helper()
	u, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	return u.String(), pgtype.UUID{Bytes: u, Valid: true}
}

// TestScopeAuthorizer_IssueTaskWorkspaceOnly verifies issue tasks remain
// workspace-scoped (any member who can see the issue may subscribe).
func TestScopeAuthorizer_IssueTaskWorkspaceOnly(t *testing.T) {
	wsStr, wsUUID := mustUUID(t)
	memberStr, _ := mustUUID(t)
	otherWsStr, _ := mustUUID(t)
	taskStr, taskUUID := mustUUID(t)
	_, issueUUID := mustUUID(t)

	q := &fakeScopeQuerier{
		tasks: map[[16]byte]db.AgentTaskQueue{
			taskUUID.Bytes: {
				ID:      taskUUID,
				IssueID: issueUUID,
			},
		},
		issues: map[[16]byte]db.Issue{
			issueUUID.Bytes: {
				ID:          issueUUID,
				WorkspaceID: wsUUID,
			},
		},
	}
	a := newScopeAuthorizer(q)
	ctx := context.Background()

	ok, err := a.AuthorizeScope(ctx, memberStr, wsStr, realtime.ScopeTask, taskStr)
	if err != nil || !ok {
		t.Fatalf("member in workspace should be allowed: ok=%v err=%v", ok, err)
	}

	ok, err = a.AuthorizeScope(ctx, memberStr, otherWsStr, realtime.ScopeTask, taskStr)
	if err != nil || ok {
		t.Fatalf("cross-workspace must be denied: ok=%v err=%v", ok, err)
	}
}

// failingScopeQuerier returns a non-ErrNoRows error from every lookup,
// simulating a transient database failure (pool exhaustion, cancelled
// context, network blip). Such errors must propagate out of AuthorizeScope
// so handleSubscribe reports "lookup_failed" rather than "forbidden".
type failingScopeQuerier struct{}

func (failingScopeQuerier) GetAgentTask(context.Context, pgtype.UUID) (db.AgentTaskQueue, error) {
	return db.AgentTaskQueue{}, errors.New("connection reset by peer")
}
func (failingScopeQuerier) GetIssue(context.Context, pgtype.UUID) (db.Issue, error) {
	return db.Issue{}, errors.New("connection reset by peer")
}

// errOnInnerQuerier succeeds for GetAgentTask (so the task path reaches its
// inner lookup) but fails GetIssue with a non-ErrNoRows error. This isolates
// the inner lookup point from the outer GetAgentTask.
type errOnInnerQuerier struct {
	task db.AgentTaskQueue
}

func (q *errOnInnerQuerier) GetAgentTask(_ context.Context, _ pgtype.UUID) (db.AgentTaskQueue, error) {
	return q.task, nil
}
func (*errOnInnerQuerier) GetIssue(context.Context, pgtype.UUID) (db.Issue, error) {
	return db.Issue{}, errors.New("connection reset by peer")
}

// TestScopeAuthorizer_DoesNotSwallowQueryErrors pins #6037: a real database
// error at any of the four lookup points must be returned to the caller as a
// non-nil error, not silently converted to a (false, nil) "forbidden" denial.
// Only a missing resource (pgx.ErrNoRows) stays a plain denial.
func TestScopeAuthorizer_DoesNotSwallowQueryErrors(t *testing.T) {
	wsStr, _ := mustUUID(t)
	userStr, _ := mustUUID(t)
	taskStr, taskUUID := mustUUID(t)
	_, issueUUID := mustUUID(t)

	a := newScopeAuthorizer(failingScopeQuerier{})
	ctx := context.Background()

	// Point 1: GetAgentTask fails on the task path.
	if _, err := a.AuthorizeScope(ctx, userStr, wsStr, realtime.ScopeTask, taskStr); err == nil {
		t.Fatalf("task-path GetAgentTask error must propagate, got err=nil")
	}
	// Point 2: GetIssue fails (task resolves to an issue task).
	issueAuth := newScopeAuthorizer(&errOnInnerQuerier{
		task: db.AgentTaskQueue{ID: taskUUID, IssueID: issueUUID},
	})
	if _, err := issueAuth.AuthorizeScope(ctx, userStr, wsStr, realtime.ScopeTask, taskStr); err == nil {
		t.Fatalf("task-path GetIssue error must propagate, got err=nil")
	}

}

// TestScopeAuthorizer_MissingResourceIsPlainDenial pins the other half of
// #6037: a missing resource (pgx.ErrNoRows) must remain a (false, nil)
// denial — not-found is reported as "forbidden", matching the HTTP layer's
// 404-not-403 convention.
func TestScopeAuthorizer_MissingResourceIsPlainDenial(t *testing.T) {
	wsStr, _ := mustUUID(t)
	userStr, _ := mustUUID(t)
	missingTask, _ := mustUUID(t)

	a := newScopeAuthorizer(&fakeScopeQuerier{})
	ctx := context.Background()

	if ok, err := a.AuthorizeScope(ctx, userStr, wsStr, realtime.ScopeTask, missingTask); err != nil || ok {
		t.Fatalf("missing task must be a plain denial: ok=%v err=%v", ok, err)
	}
}
