package orchestration

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestRequestPlanCreatesOneMissionScopedRunAndReplays(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var planningReady bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_constraint WHERE conname = 'orchestration_run_scope_purpose_check'
	)`).Scan(&planningReady); err != nil {
		t.Fatal(err)
	}
	if !planningReady {
		t.Skip("planning scope migrations are not applied")
	}

	suffix := uuid.NewString()
	var ownerID, workspaceID, runtimeID, agentID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Planning owner', $1) RETURNING id`, "planning-"+suffix+"@liexiu.test").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ('Planning test', $1, '', 'PLN') RETURNING id`, "planning-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id) VALUES ($1, 'Planning runtime', 'cloud', 'test', 'online', 'test', '{}', now(), 'private', $2) RETURNING id`, workspaceID, ownerID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id) VALUES ($1, 'Planning agent', '', 'cloud', '{}', $2, 'private', 1, $3) RETURNING id`, workspaceID, runtimeID, ownerID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupPlanningFixture(t, pool, workspaceID, ownerID) })

	queries := db.New(pool)
	repository := NewRepository(queries, pool)
	gateway := &recordingPlanGateway{}
	service := NewService(queries, repository, gateway, DefaultPlanHardLimits())
	plannerBinding := rolePolicyBindingFor(t, seedRolePolicyBindings(t, ctx, repository, workspaceID, ownerID, DutyPlanner), DutyPlanner)
	created, err := service.CreateMission(ctx, CreateMissionCommand{
		WorkspaceID: workspaceID, CommandID: newTestUUID(), ActorID: ownerID,
		Title: "Plan this mission", Limits: PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	commandID := newTestUUID()
	command := RequestPlanCommand{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID, CommandID: commandID,
		ActorID: ownerID, ExpectedRevision: 1,
		RolePolicyBinding: plannerBinding,
		Input:             PlanProposalInput{Objective: "Produce a reviewable plan", DeliveryCriteria: []string{"Plan is valid"}},
	}
	const concurrentRequests = 8
	results := make(chan RequestPlanResult, concurrentRequests)
	requestErrors := make(chan error, concurrentRequests)
	var requests sync.WaitGroup
	for range concurrentRequests {
		requests.Add(1)
		go func() {
			defer requests.Done()
			value, requestErr := service.RequestPlan(ctx, command)
			if requestErr != nil {
				requestErrors <- requestErr
				return
			}
			results <- value
		}()
	}
	requests.Wait()
	close(results)
	close(requestErrors)
	for requestErr := range requestErrors {
		var routingErr *RoutingUnavailableError
		if errors.As(requestErr, &routingErr) {
			t.Errorf("concurrent request: %v routing=%#v", requestErr, routingErr.Routing)
			continue
		}
		t.Errorf("concurrent request: %v", requestErr)
	}
	if t.Failed() {
		t.FailNow()
	}
	var result RequestPlanResult
	idempotentResults := 0
	for value := range results {
		if result.Run.ID.Valid && value.Run.ID != result.Run.ID {
			t.Fatalf("concurrent command produced runs %v and %v", result.Run.ID, value.Run.ID)
		}
		result = value
		if value.Idempotent {
			idempotentResults++
		}
	}
	if result.Mission.Status != string(MissionStatusDraft) || result.Mission.Revision != 2 || result.Assignment.Role != string(DutyPlanner) || result.Assignment.TaskNodeID.Valid || result.Run.Purpose != "plan" || result.Run.TaskNodeID.Valid {
		t.Fatalf("unexpected planning result: %#v", result)
	}
	if len(result.RolePolicySnapshots) != 1 || result.RolePolicySnapshots[0].Duty != DutyPlanner || result.RolePolicySnapshots[0].RoleProfileKey != plannerBinding.ProfileKey || result.RolePolicySnapshots[0].Config.Instructions != "v1" {
		t.Fatalf("unexpected planner RolePolicy snapshot: %#v", result.RolePolicySnapshots)
	}
	if idempotentResults != concurrentRequests-1 || gateway.callCount() != concurrentRequests || gateway.lastRequest().RunID != result.Run.ID {
		t.Fatalf("idempotent=%d gateway calls=%d request=%#v", idempotentResults, gateway.callCount(), gateway.lastRequest())
	}
	recovery, err := NewRepository(queries, pool).ReconcileRun(ctx, ReconcileRunParams{WorkspaceID: workspaceID, RunID: result.Run.ID, ObservedAt: result.Run.CreatedAt.Time})
	if err != nil {
		t.Fatal(err)
	}
	if !recovery.EnqueueExecution || recovery.EnqueueActorID != ownerID || recovery.Changed {
		t.Fatalf("unexpected restart recovery: %#v", recovery)
	}
	var assignments, runs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_assignment WHERE workspace_id=$1 AND mission_id=$2 AND task_node_id IS NULL`, workspaceID, created.Mission.IssueID).Scan(&assignments); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_run WHERE workspace_id=$1 AND mission_id=$2 AND task_node_id IS NULL`, workspaceID, created.Mission.IssueID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if assignments != 1 || runs != 1 {
		t.Fatalf("assignments=%d runs=%d, want 1/1", assignments, runs)
	}
	otherMission, err := service.CreateMission(ctx, CreateMissionCommand{
		WorkspaceID: workspaceID, CommandID: newTestUUID(), ActorID: ownerID,
		Title: "Do not reuse a planning command across Missions", Limits: PlanLimits{MaxParallelRuns: 1, MaxTaskAttempts: 1, MaxReworkCycles: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestPlan(ctx, RequestPlanCommand{
		WorkspaceID: workspaceID, MissionID: otherMission.Mission.IssueID, CommandID: commandID, ActorID: ownerID,
		ExpectedRevision: 1, RolePolicyBinding: plannerBinding, Input: command.Input,
	}); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("cross-Mission planning command reuse error=%v, want ErrCommandConflict", err)
	}
	_, err = service.RequestPlan(ctx, RequestPlanCommand{WorkspaceID: workspaceID, MissionID: created.Mission.IssueID, CommandID: newTestUUID(), ActorID: ownerID, ExpectedRevision: 1, RolePolicyBinding: plannerBinding, Input: command.Input})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale request error=%v", err)
	}

	plannerV2 := createNextRolePolicyBinding(t, ctx, repository, workspaceID, ownerID, plannerBinding)
	changedReplay := command
	changedReplay.RolePolicyBinding = plannerV2
	if _, err := service.RequestPlan(ctx, changedReplay); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("same command changed planner binding error=%v, want ErrCommandConflict", err)
	}
	projection, err := service.GetMissionProjection(ctx, workspaceID, created.Mission.IssueID)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.RolePolicySnapshots) != 1 || projection.RolePolicySnapshots[0].RoleProfileVersion != 1 || projection.RolePolicySnapshots[0].Config.Instructions != "v1" {
		t.Fatalf("new global RoleProfile version altered planner snapshot: %#v", projection.RolePolicySnapshots)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_assignment SET status='revoked', ended_at=now() WHERE id=$1`, result.Assignment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_run SET status='cancelled', finished_at=now() WHERE id=$1`, result.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestPlan(ctx, RequestPlanCommand{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID, CommandID: newTestUUID(), ActorID: ownerID,
		ExpectedRevision: 2, RolePolicyBinding: plannerV2, Input: command.Input,
	}); !errors.Is(err, ErrRolePolicyAlreadyFrozen) {
		t.Fatalf("replan changed frozen planner binding error=%v, want ErrRolePolicyAlreadyFrozen", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mission_role_policy_snapshot WHERE workspace_id=$1 AND mission_id=$2`, workspaceID, created.Mission.IssueID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("planner binding conflict changed snapshot count=%d, want 1", runs)
	}
}

type recordingPlanGateway struct {
	mu           sync.Mutex
	enqueueCalls int
	lastEnqueue  EnqueueExecutionRequest
}

func (g *recordingPlanGateway) Enqueue(_ context.Context, request EnqueueExecutionRequest) (EnqueueExecutionResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enqueueCalls++
	g.lastEnqueue = request
	return EnqueueExecutionResult{AgentTaskID: newTestUUID(), Status: "queued", Idempotent: g.enqueueCalls > 1}, nil
}

func (g *recordingPlanGateway) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.enqueueCalls
}

func (g *recordingPlanGateway) lastRequest() EnqueueExecutionRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastEnqueue
}

func (*recordingPlanGateway) Cancel(context.Context, CancelExecutionRequest) (CancelExecutionResult, error) {
	return CancelExecutionResult{}, nil
}

func cleanupPlanningFixture(t *testing.T, pool *pgxpool.Pool, workspaceID, ownerID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`DELETE FROM agent_task_queue WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id=$1)`,
		`DELETE FROM mission_role_policy_snapshot WHERE workspace_id=$1`, `DELETE FROM role_profile WHERE workspace_id=$1`,
		`DELETE FROM orchestration_run WHERE workspace_id=$1`, `DELETE FROM orchestration_assignment WHERE workspace_id=$1`,
		`DELETE FROM orchestration_activity WHERE workspace_id=$1`, `DELETE FROM mission WHERE workspace_id=$1`,
		`DELETE FROM issue WHERE workspace_id=$1`, `DELETE FROM agent WHERE workspace_id=$1`,
		`DELETE FROM agent_runtime WHERE workspace_id=$1`, `DELETE FROM member WHERE workspace_id=$1`, `DELETE FROM workspace WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, workspaceID); err != nil {
			t.Errorf("cleanup %q: %v", statement, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, ownerID); err != nil {
		t.Errorf("cleanup user: %v", err)
	}
}
