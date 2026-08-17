package orchestration

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestRepositoryAdvanceBudgetAdmissionSerializesReservations(t *testing.T) {
	fixture := newBudgetIntegrationFixture(t)
	ctx := context.Background()
	maxTokens := int64(100)
	missionID := fixture.createStartedMission(t, &BudgetPolicy{
		MaxTokens: &maxTokens,
		Gate:      BudgetGateFailClosed,
	}, []BudgetEstimate{{Tokens: 60}, {Tokens: 60}})

	type outcome struct {
		result AdvanceMissionResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := fixture.repository.AdvanceMission(ctx, budgetAdvanceParams(fixture.workspaceID, missionID))
			results <- outcome{result: result, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent AdvanceMission: %v", result.err)
		}
	}

	mission := fixture.loadMission(t, missionID)
	if mission.Status != string(MissionStatusBlocked) || mission.BudgetGateStatus != BudgetGateStatusExceeded {
		t.Fatalf("budget-blocked mission = status=%q gate=%q, want blocked/exceeded", mission.Status, mission.BudgetGateStatus)
	}
	if got := countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_run WHERE mission_id = $1`, missionID); got != 1 {
		t.Fatalf("concurrent admission created %d runs, want exactly one reservation", got)
	}
	if got := countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_activity WHERE mission_id = $1 AND type = 'budget.exceeded'`, missionID); got != 1 {
		t.Fatalf("concurrent admission created %d budget activities, want one deduplicated gate activity", got)
	}
	usage, err := fixture.queries.GetMissionBudgetUsage(ctx, db.GetMissionBudgetUsageParams{WorkspaceID: fixture.workspaceID, MissionID: missionID})
	if err != nil {
		t.Fatalf("load budget usage: %v", err)
	}
	if usage.ReservedTokens != 60 || usage.ConsumedTokens != 0 {
		t.Fatalf("budget usage after concurrent admission = consumed=%d reserved=%d, want 0/60", usage.ConsumedTokens, usage.ReservedTokens)
	}
}

func TestApproveMissionBudgetRestoresDispatchAndIsIdempotent(t *testing.T) {
	fixture := newBudgetIntegrationFixture(t)
	ctx := context.Background()
	maxTokens := int64(50)
	missionID := fixture.createStartedMission(t, &BudgetPolicy{
		MaxTokens: &maxTokens,
		Gate:      BudgetGateOwnerApproval,
	}, []BudgetEstimate{{Tokens: 60}})

	blocked, err := fixture.repository.AdvanceMission(ctx, budgetAdvanceParams(fixture.workspaceID, missionID))
	if err != nil {
		t.Fatalf("initial budget admission: %v", err)
	}
	if blocked.Mission.Status != string(MissionStatusBlocked) || blocked.Mission.BudgetGateStatus != BudgetGateStatusApprovalRequired {
		t.Fatalf("initial budget gate = status=%q gate=%q, want blocked/approval_required", blocked.Mission.Status, blocked.Mission.BudgetGateStatus)
	}
	if len(blocked.CreatedRuns) != 0 || countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_run WHERE mission_id = $1`, missionID) != 0 {
		t.Fatalf("owner approval gate created a run before approval: result=%d persisted=%d", len(blocked.CreatedRuns), countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_run WHERE mission_id = $1`, missionID))
	}

	commandID := budgetTestUUID()
	approved, err := fixture.service.ApproveMissionBudget(ctx, ApproveMissionBudgetCommand{
		WorkspaceID:      fixture.workspaceID,
		MissionID:        missionID,
		CommandID:        commandID,
		ActorID:          fixture.ownerID,
		ExpectedRevision: blocked.Mission.Revision,
		GrantTokens:      20,
		Reason:           "owner approved the remaining execution headroom",
	})
	if err != nil {
		t.Fatalf("ApproveMissionBudget: %v", err)
	}
	if approved.Idempotent {
		t.Fatal("first ApproveMissionBudget call was marked idempotent")
	}
	if len(approved.Advance.CreatedRuns) != 1 {
		t.Fatalf("approved budget dispatched %d runs, want one", len(approved.Advance.CreatedRuns))
	}

	mission := fixture.loadMission(t, missionID)
	if mission.Status != string(MissionStatusRunning) || mission.BudgetGateStatus != BudgetGateStatusApproved {
		t.Fatalf("approved mission = status=%q gate=%q, want running/approved", mission.Status, mission.BudgetGateStatus)
	}
	if mission.BudgetGrantTokens != 20 || mission.BudgetApprovedBy != fixture.ownerID || !mission.BudgetApprovedAt.Valid {
		t.Fatalf("approved mission grant metadata = tokens=%d approved_by=%v approved_at=%v", mission.BudgetGrantTokens, mission.BudgetApprovedBy, mission.BudgetApprovedAt)
	}
	if got := countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_run WHERE mission_id = $1`, missionID); got != 1 {
		t.Fatalf("approved dispatch persisted %d runs, want one", got)
	}

	replayed, err := fixture.service.ApproveMissionBudget(ctx, ApproveMissionBudgetCommand{
		WorkspaceID:      fixture.workspaceID,
		MissionID:        missionID,
		CommandID:        commandID,
		ActorID:          fixture.ownerID,
		ExpectedRevision: blocked.Mission.Revision,
		GrantTokens:      20,
	})
	if err != nil {
		t.Fatalf("replay ApproveMissionBudget: %v", err)
	}
	if !replayed.Idempotent || replayed.Activity.ID != approved.Activity.ID {
		t.Fatalf("replayed approval = idempotent=%t activity=%s, want same activity %s", replayed.Idempotent, uuidText(replayed.Activity.ID), uuidText(approved.Activity.ID))
	}
	if got := countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_run WHERE mission_id = $1`, missionID); got != 1 {
		t.Fatalf("replayed approval created %d runs, want one", got)
	}
	if got := countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_activity WHERE mission_id = $1 AND type = 'budget.approved'`, missionID); got != 1 {
		t.Fatalf("replayed approval created %d approval activities, want one", got)
	}
}

func TestFailClosedBudgetCannotBeApproved(t *testing.T) {
	fixture := newBudgetIntegrationFixture(t)
	ctx := context.Background()
	maxTokens := int64(50)
	missionID := fixture.createStartedMission(t, &BudgetPolicy{
		MaxTokens: &maxTokens,
		Gate:      BudgetGateFailClosed,
	}, []BudgetEstimate{{Tokens: 60}})

	blocked, err := fixture.repository.AdvanceMission(ctx, budgetAdvanceParams(fixture.workspaceID, missionID))
	if err != nil {
		t.Fatalf("initial fail-closed admission: %v", err)
	}
	if blocked.Mission.BudgetGateStatus != BudgetGateStatusExceeded {
		t.Fatalf("fail-closed gate = %q, want exceeded", blocked.Mission.BudgetGateStatus)
	}

	_, err = fixture.service.ApproveMissionBudget(ctx, ApproveMissionBudgetCommand{
		WorkspaceID:      fixture.workspaceID,
		MissionID:        missionID,
		CommandID:        budgetTestUUID(),
		ActorID:          fixture.ownerID,
		ExpectedRevision: blocked.Mission.Revision,
		GrantTokens:      20,
	})
	if !errors.Is(err, ErrBudgetApprovalNotRequired) {
		t.Fatalf("fail-closed approval error = %v, want ErrBudgetApprovalNotRequired", err)
	}
	mission := fixture.loadMission(t, missionID)
	if mission.BudgetGrantTokens != 0 || mission.Status != string(MissionStatusBlocked) || mission.BudgetGateStatus != BudgetGateStatusExceeded {
		t.Fatalf("fail-closed mission changed after rejected approval: status=%q gate=%q grant=%d", mission.Status, mission.BudgetGateStatus, mission.BudgetGrantTokens)
	}
	if got := countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_run WHERE mission_id = $1`, missionID); got != 0 {
		t.Fatalf("fail-closed approval created %d runs, want zero", got)
	}
	if got := countMissionRows(t, ctx, fixture.pool, `SELECT count(*) FROM orchestration_activity WHERE mission_id = $1 AND type = 'budget.approved'`, missionID); got != 0 {
		t.Fatalf("rejected fail-closed approval created %d approval activities, want zero", got)
	}
}

type budgetIntegrationFixture struct {
	pool        *pgxpool.Pool
	queries     *db.Queries
	repository  *Repository
	service     *Service
	ownerID     pgtype.UUID
	workspaceID pgtype.UUID
}

type budgetExecutionGateway struct{}

func (*budgetExecutionGateway) Enqueue(context.Context, EnqueueExecutionRequest) (EnqueueExecutionResult, error) {
	return EnqueueExecutionResult{}, nil
}

func (*budgetExecutionGateway) Cancel(context.Context, CancelExecutionRequest) (CancelExecutionResult, error) {
	return CancelExecutionResult{}, nil
}

func newBudgetIntegrationFixture(t *testing.T) *budgetIntegrationFixture {
	t.Helper()
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping budget integration test")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	var schemaReady bool
	if err := pool.QueryRow(ctx, `
		SELECT to_regclass('mission') IS NOT NULL
		   AND to_regclass('orchestration_activity') IS NOT NULL
		   AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'mission' AND column_name = 'budget_gate_status')
		   AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'task_node' AND column_name = 'budget_estimate_tokens')
	`).Scan(&schemaReady); err != nil {
		t.Fatalf("check budget schema: %v", err)
	}
	if !schemaReady {
		t.Skip("budget gate migration is not applied")
	}

	suffix := uuid.NewString()
	fixture := &budgetIntegrationFixture{pool: pool}
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Budget gate owner', $1) RETURNING id`, "budget-owner-"+suffix+"@liexiu.test").Scan(&fixture.ownerID); err != nil {
		t.Fatalf("create budget owner: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ('Budget gate test', $1, '', 'BGT') RETURNING id`, "budget-gate-"+suffix).Scan(&fixture.workspaceID); err != nil {
		t.Fatalf("create budget workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, fixture.workspaceID, fixture.ownerID); err != nil {
		t.Fatalf("create budget owner membership: %v", err)
	}
	for index := 1; index <= 2; index++ {
		var runtimeID pgtype.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id)
			VALUES ($1, $2, 'cloud', 'test', 'online', 'budget-test', '{}', now(), 'private', $3)
			RETURNING id
		`, fixture.workspaceID, "budget-runtime-"+string(rune('0'+index)), fixture.ownerID).Scan(&runtimeID); err != nil {
			t.Fatalf("create budget runtime %d: %v", index, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
			VALUES ($1, $2, '', 'cloud', '{}', $3, 'private', 8, $4)
		`, fixture.workspaceID, "budget-agent-"+string(rune('0'+index)), runtimeID, fixture.ownerID); err != nil {
			t.Fatalf("create budget agent %d: %v", index, err)
		}
	}

	fixture.queries = db.New(pool)
	fixture.repository = NewRepository(fixture.queries, pool)
	fixture.service = NewService(fixture.queries, fixture.repository, &budgetExecutionGateway{}, DefaultPlanHardLimits())
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, statement := range []string{
			`DELETE FROM review_verdict WHERE workspace_id = $1`,
			`DELETE FROM artifact WHERE workspace_id = $1`,
			`DELETE FROM orchestration_run WHERE workspace_id = $1`,
			`DELETE FROM orchestration_assignment WHERE workspace_id = $1`,
			`DELETE FROM orchestration_activity WHERE workspace_id = $1`,
			`DELETE FROM task_node WHERE workspace_id = $1`,
			`DELETE FROM mission WHERE workspace_id = $1`,
			`DELETE FROM workspace WHERE id = $1`,
		} {
			if _, cleanupErr := pool.Exec(cleanupCtx, statement, fixture.workspaceID); cleanupErr != nil {
				t.Errorf("cleanup %q: %v", statement, cleanupErr)
			}
		}
		if _, cleanupErr := pool.Exec(cleanupCtx, `DELETE FROM "user" WHERE id = $1`, fixture.ownerID); cleanupErr != nil {
			t.Errorf("cleanup owner: %v", cleanupErr)
		}
	})
	return fixture
}

func (f *budgetIntegrationFixture) createStartedMission(t *testing.T, policy *BudgetPolicy, roots []BudgetEstimate) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	limits := PlanLimits{MaxParallelRuns: 3, MaxTaskAttempts: 2, MaxReworkCycles: 1, Budget: policy}
	created, err := f.service.CreateMission(ctx, CreateMissionCommand{
		WorkspaceID: f.workspaceID, CommandID: budgetTestUUID(), ActorID: f.ownerID,
		Title: "Budget gate mission", Limits: limits,
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	missionID := created.Mission.IssueID
	planned, err := f.service.SubmitPlan(ctx, SubmitPlanCommand{
		WorkspaceID: f.workspaceID, MissionID: missionID, CommandID: budgetTestUUID(),
		ActorID: f.ownerID, ExpectedRevision: created.Mission.Revision,
		Plan: budgetIntegrationPlan(missionID, limits, roots),
	})
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}
	started, err := f.service.StartMission(ctx, StartMissionCommand{
		WorkspaceID: f.workspaceID, MissionID: missionID, CommandID: budgetTestUUID(),
		ActorID: f.ownerID, ExpectedRevision: planned.Mission.Revision,
	})
	if err != nil {
		t.Fatalf("StartMission: %v", err)
	}
	if started.Mission.Status != string(MissionStatusRunning) {
		t.Fatalf("started mission status = %q, want running", started.Mission.Status)
	}
	return missionID
}

func budgetIntegrationPlan(missionID pgtype.UUID, limits PlanLimits, roots []BudgetEstimate) Plan {
	nodes := make([]PlanNode, 0, len(roots)+1)
	dependencies := make([]string, 0, len(roots))
	for index, estimate := range roots {
		key := string(rune('A' + index))
		nodes = append(nodes, PlanNode{
			Key: key, Title: "Budget root " + key, Description: "Exercise budget admission",
			Role: RoleExecutor, AcceptanceCriteria: []string{"run is admitted within budget"},
			ArtifactKinds: []ArtifactKind{ArtifactKindCommit}, BudgetEstimate: estimate,
		})
		dependencies = append(dependencies, key)
	}
	nodes = append(nodes, PlanNode{
		Key: "integrate", Title: "Budget integration", Description: "Complete the budget test plan",
		Role: RoleIntegrator, AcceptanceCriteria: []string{"final delivery exists"},
		ArtifactKinds: []ArtifactKind{ArtifactKindFinalDelivery}, DependsOn: dependencies,
		BudgetEstimate: BudgetEstimate{Tokens: 1},
	})
	return Plan{
		SchemaVersion: PlanSchemaVersion, MissionID: uuidText(missionID), PlanKey: "budget-integration",
		Limits: limits, Nodes: nodes,
	}
}

func budgetAdvanceParams(workspaceID, missionID pgtype.UUID) AdvanceMissionParams {
	return AdvanceMissionParams{
		WorkspaceID:    workspaceID,
		MissionID:      missionID,
		CorrelationID:  budgetTestUUID(),
		ObservedAt:     time.Now().UTC(),
		DispatchWindow: time.Minute,
		RunTimeout:     time.Minute,
	}
}

func (f *budgetIntegrationFixture) loadMission(t *testing.T, missionID pgtype.UUID) db.Mission {
	t.Helper()
	mission, err := f.queries.GetMissionInWorkspace(context.Background(), db.GetMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: f.workspaceID})
	if err != nil {
		t.Fatalf("load mission: %v", err)
	}
	return mission
}

func countMissionRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, missionID pgtype.UUID) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, query, missionID).Scan(&count); err != nil {
		t.Fatalf("count mission rows: %v", err)
	}
	return count
}

func budgetTestUUID() pgtype.UUID {
	value := uuid.New()
	return pgtype.UUID{Bytes: value, Valid: true}
}
