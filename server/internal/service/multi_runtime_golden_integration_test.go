package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// TestMultiRuntimeGoldenIntegration runs the complete vendor-neutral control
// plane through PostgreSQL and TaskExecutionGateway. Provider completion uses
// the existing reconciliation seam, so this default Gate cannot invoke a CLI.
func TestMultiRuntimeGoldenIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var ready bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('orchestration_human_gate') IS NOT NULL`).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Skip("orchestration migrations through 349 are not applied")
	}

	fixture := createMultiRuntimeGoldenFixture(t, ctx, pool)
	queries := db.New(pool)
	repository := orchestration.NewRepository(queries, pool)
	gateway := NewTaskExecutionGateway(NewTaskService(queries, pool, nil, events.New()))
	service := orchestration.NewService(queries, repository, gateway, orchestration.DefaultPlanHardLimits())
	limits := orchestration.PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1}
	input := orchestration.PlanProposalInput{Objective: "Complete a multi-runtime delivery", DeliveryCriteria: []string{"A and B are reviewed before C is integrated"}}

	plannerBinding := createGoldenRoleBinding(t, ctx, repository, fixture, orchestration.DutyPlanner,
		[]pgtype.UUID{fixture.plannerIntegratorRuntime}, []pgtype.UUID{fixture.plannerIntegratorRuntime}, []string{"codex"}, fixture.plannerIntegratorAgent)
	executorBinding := createGoldenRoleBinding(t, ctx, repository, fixture, orchestration.DutyExecutor,
		[]pgtype.UUID{fixture.executorDshRuntime, fixture.executorCodexRuntime}, []pgtype.UUID{fixture.executorDshRuntime, fixture.executorCodexRuntime}, []string{"dsh", "codex"}, pgtype.UUID{})
	reviewerBinding := createGoldenRoleBinding(t, ctx, repository, fixture, orchestration.DutyReviewer,
		[]pgtype.UUID{fixture.reviewerDshRuntime, fixture.reviewerCodexRuntime}, []pgtype.UUID{fixture.reviewerDshRuntime, fixture.reviewerCodexRuntime}, []string{"dsh", "codex"}, pgtype.UUID{})
	integratorBinding := createGoldenRoleBinding(t, ctx, repository, fixture, orchestration.DutyIntegrator,
		[]pgtype.UUID{fixture.plannerIntegratorRuntime}, []pgtype.UUID{fixture.plannerIntegratorRuntime}, []string{"codex"}, fixture.plannerIntegratorAgent)

	created, err := service.CreateMission(ctx, orchestration.CreateMissionCommand{
		WorkspaceID: fixture.workspaceID, CommandID: goldenUUID(), ActorID: fixture.userID,
		Title: "multi-runtime golden", Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	missionID := created.Mission.IssueID
	planning, err := service.RequestPlan(ctx, orchestration.RequestPlanCommand{
		WorkspaceID: fixture.workspaceID, MissionID: missionID, CommandID: goldenUUID(), ActorID: fixture.userID,
		ExpectedRevision: 1, RolePolicyBinding: plannerBinding, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertGoldenAssignment(t, ctx, pool, planning.Assignment.ID, fixture.plannerIntegratorAgent, fixture.plannerIntegratorRuntime, "codex")
	proposal, err := orchestration.EncodePlanProposal(goldenProposal(missionID, input, limits))
	if err != nil {
		t.Fatal(err)
	}
	planningCompletedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed', completed_at=$2, result=$3 WHERE id=$1`, planning.Execution.AgentTaskID, planningCompletedAt, mustJSON(t, map[string]any{"output": string(proposal)})); err != nil {
		t.Fatal(err)
	}
	planningResult, err := repository.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: fixture.workspaceID, RunID: planning.Run.ID, ObservedAt: planningCompletedAt})
	if err != nil {
		t.Fatal(err)
	}
	if planningResult.PlanProposalArtifact == nil {
		t.Fatal("planning completion did not produce a PlanProposal artifact")
	}
	approved, err := service.ApprovePlanProposal(ctx, orchestration.SubmitPlanProposalCommand{
		WorkspaceID: fixture.workspaceID, MissionID: missionID, ProposalArtifactID: planningResult.PlanProposalArtifact.ID,
		CommandID: goldenUUID(), ActorID: fixture.userID, ExpectedRevision: planning.Mission.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartMission(ctx, orchestration.StartMissionCommand{
		WorkspaceID: fixture.workspaceID, MissionID: missionID, CommandID: goldenUUID(), ActorID: fixture.userID,
		ExpectedRevision:   approved.Mission.Revision,
		RolePolicyBindings: []orchestration.RolePolicyBinding{executorBinding, reviewerBinding, integratorBinding},
	}); err != nil {
		t.Fatal(err)
	}

	nodes := loadNodesByKey(t, ctx, queries, fixture.workspaceID, missionID)
	initial, err := service.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: fixture.workspaceID, MissionID: missionID, CorrelationID: goldenUUID()})
	if err != nil || len(initial.CreatedRuns) != 2 {
		t.Fatalf("initial A/B dispatch runs=%d err=%v", len(initial.CreatedRuns), err)
	}
	assertGoldenProviders(t, ctx, pool, initial.CreatedRuns, "codex", "dsh")
	a1 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["A"].IssueID, "execute")
	b1 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["B"].IssueID, "execute")
	a1Artifact := finishGoldenArtifactTask(t, ctx, pool, repository, service, fixture.workspaceID, a1, orchestration.ArtifactKindCommit, "repo://golden/A")
	aReview1 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["A"].IssueID, "review")
	aProducer := assignmentRuntimeProvider(t, ctx, pool, a1.AssignmentID)
	aReviewer := assignmentRuntimeProvider(t, ctx, pool, aReview1.AssignmentID)
	if aReviewer.provider != "dsh" || aReviewer.agent == aProducer.agent {
		t.Fatalf("preferred independent reviewer=%#v producer=%#v", aReviewer, aProducer)
	}
	finishGoldenReviewTask(t, ctx, pool, repository, service, fixture.workspaceID, aReview1, orchestration.ReviewDecisionChangesRequested)

	if _, err := pool.Exec(ctx, `UPDATE agent_runtime SET status='offline' WHERE id=$1`, fixture.reviewerDshRuntime); err != nil {
		t.Fatal(err)
	}
	// B is completed only after dsh goes offline. This makes the fallback a
	// consequence of the new routing fact rather than an already-created Run.
	b1Artifact := finishGoldenArtifactTask(t, ctx, pool, repository, service, fixture.workspaceID, b1, orchestration.ArtifactKindCommit, "repo://golden/B")
	bReview := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["B"].IssueID, "review")
	bProducer := assignmentRuntimeProvider(t, ctx, pool, b1.AssignmentID)
	bReviewer := assignmentRuntimeProvider(t, ctx, pool, bReview.AssignmentID)
	if bReviewer.provider != "codex" || bReviewer.runtime != fixture.reviewerCodexRuntime || bReviewer.agent == bProducer.agent {
		t.Fatalf("offline fallback reviewer=%#v producer=%#v", bReviewer, bProducer)
	}
	finishGoldenReviewTask(t, ctx, pool, repository, service, fixture.workspaceID, bReview, orchestration.ReviewDecisionApproved)

	if _, err := pool.Exec(ctx, `UPDATE agent_runtime SET status='online',last_seen_at=now() WHERE id=$1`, fixture.reviewerDshRuntime); err != nil {
		t.Fatal(err)
	}
	a2 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["A"].IssueID, "execute")
	a2Artifact := finishGoldenArtifactTask(t, ctx, pool, repository, service, fixture.workspaceID, a2, orchestration.ArtifactKindCommit, "repo://golden/A-v2")
	aReview2 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["A"].IssueID, "review")
	a2Producer := assignmentRuntimeProvider(t, ctx, pool, a2.AssignmentID)
	a2Reviewer := assignmentRuntimeProvider(t, ctx, pool, aReview2.AssignmentID)
	if a2Reviewer.provider != "dsh" || a2Reviewer.runtime != fixture.reviewerDshRuntime || a2Reviewer.agent == a2Producer.agent {
		t.Fatalf("recovered preferred reviewer=%#v producer=%#v", a2Reviewer, a2Producer)
	}
	finishGoldenReviewTask(t, ctx, pool, repository, service, fixture.workspaceID, aReview2, orchestration.ReviewDecisionApproved)

	cRun := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["C"].IssueID, "integrate")
	assertIntegratorInput(t, cRun, []pgtype.UUID{a2Artifact.ID, b1Artifact.ID}, a1Artifact.ID)
	assertGoldenAssignment(t, ctx, pool, cRun.AssignmentID, fixture.plannerIntegratorAgent, fixture.plannerIntegratorRuntime, "codex")
	finishGoldenArtifactTask(t, ctx, pool, repository, service, fixture.workspaceID, cRun, orchestration.ArtifactKindFinalDelivery, "repo://golden/final")
	cReview := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["C"].IssueID, "review")
	cProducer := assignmentRuntimeProvider(t, ctx, pool, cRun.AssignmentID)
	cReviewer := assignmentRuntimeProvider(t, ctx, pool, cReview.AssignmentID)
	if cReviewer.provider != "dsh" || cReviewer.agent == cProducer.agent {
		t.Fatalf("final independent reviewer=%#v producer=%#v", cReviewer, cProducer)
	}
	final := finishGoldenReviewTask(t, ctx, pool, repository, service, fixture.workspaceID, cReview, orchestration.ReviewDecisionApproved)
	projection, err := service.GetMissionProjection(ctx, fixture.workspaceID, missionID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Mission.Status != string(orchestration.MissionStatusCompleted) || projection.Mission.Status != orchestration.MissionStatusCompleted || projection.Mission.Progress.Completed != 3 || projection.Mission.Progress.Percent != 100 || len(projection.HumanGates) != 0 {
		t.Fatalf("final mission=%#v projection=%#v gates=%#v", final.Mission, projection.Mission, projection.HumanGates)
	}
}

type multiRuntimeGoldenFixture struct {
	userID, workspaceID                              pgtype.UUID
	plannerIntegratorAgent, plannerIntegratorRuntime pgtype.UUID
	executorDshAgent, executorDshRuntime             pgtype.UUID
	executorCodexAgent, executorCodexRuntime         pgtype.UUID
	reviewerDshAgent, reviewerDshRuntime             pgtype.UUID
	reviewerCodexAgent, reviewerCodexRuntime         pgtype.UUID
}

func createMultiRuntimeGoldenFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) multiRuntimeGoldenFixture {
	t.Helper()
	suffix := uuid.NewString()
	var fixture multiRuntimeGoldenFixture
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('golden owner',$1) RETURNING id`, "golden-"+suffix+"@liexiu.test").Scan(&fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name,slug,description,issue_prefix) VALUES ('golden workspace',$1,'','GLD') RETURNING id`, "golden-"+suffix).Scan(&fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	type runtimeSpec struct {
		name, provider string
		agent, runtime *pgtype.UUID
	}
	for _, spec := range []runtimeSpec{
		{"planner-integrator", "codex", &fixture.plannerIntegratorAgent, &fixture.plannerIntegratorRuntime},
		{"executor-dsh", "dsh", &fixture.executorDshAgent, &fixture.executorDshRuntime},
		{"executor-codex", "codex", &fixture.executorCodexAgent, &fixture.executorCodexRuntime},
		{"reviewer-dsh", "dsh", &fixture.reviewerDshAgent, &fixture.reviewerDshRuntime},
		{"reviewer-codex", "codex", &fixture.reviewerCodexAgent, &fixture.reviewerCodexRuntime},
	} {
		if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime(workspace_id,name,runtime_mode,provider,status,device_info,metadata,last_seen_at,visibility,owner_id) VALUES($1,$2,'cloud',$3,'online','test','{}',now(),'private',$4) RETURNING id`, fixture.workspaceID, spec.name, spec.provider, fixture.userID).Scan(spec.runtime); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO agent(workspace_id,name,description,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id) VALUES($1,$2,'','cloud','{}',$3,'private',1,$4) RETURNING id`, fixture.workspaceID, spec.name, *spec.runtime, fixture.userID).Scan(spec.agent); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { cleanupMultiRuntimeGoldenFixture(t, pool, fixture) })
	return fixture
}

func createGoldenRoleBinding(t *testing.T, ctx context.Context, repository *orchestration.Repository, fixture multiRuntimeGoldenFixture, duty orchestration.Duty, allowed, preferred []pgtype.UUID, providers []string, agentID pgtype.UUID) orchestration.RolePolicyBinding {
	t.Helper()
	toStrings := func(values []pgtype.UUID) []string {
		result := make([]string, len(values))
		for index := range values {
			result[index] = uuid.UUID(values[index].Bytes).String()
		}
		return result
	}
	created, err := repository.CreateRoleProfileVersion(ctx, orchestration.CreateRoleProfileVersionParams{
		WorkspaceID: fixture.workspaceID, CommandID: goldenUUID(), ActorID: fixture.userID,
		ProfileKey: "golden-" + duty.String(), Duty: duty, Name: "Golden " + duty.String(),
		Config: orchestration.RoleProfileConfig{
			Instructions: "Follow the provider-neutral golden contract", RequiredCapabilities: []string{},
			Runtime: orchestration.RoleRuntimePreferences{AllowedRuntimeIDs: toStrings(allowed), PreferredRuntimeIDs: toStrings(preferred), Providers: providers, Models: []string{}},
			Tools:   orchestration.RoleToolPermissions{AllowedTools: []string{}, AllowedPaths: []string{}},
			Budget:  orchestration.RoleBudgetLimits{MaxReworkCycles: 1, MaxTechnicalRetries: 1}, TimeoutSeconds: 300, MaxConcurrency: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return orchestration.RolePolicyBinding{Duty: duty, ProfileKey: created.Profile.ProfileKey, Version: created.Profile.Version, AgentID: agentID}
}

func goldenProposal(missionID pgtype.UUID, input orchestration.PlanProposalInput, limits orchestration.PlanLimits) orchestration.PlanProposal {
	return orchestration.PlanProposal{
		SchemaVersion: orchestration.PlanProposalSchemaVersion, MissionID: uuid.UUID(missionID.Bytes).String(), ProposalKey: "multi-runtime-golden", Input: input, Limits: limits,
		Nodes: []orchestration.PlanProposalNode{
			{Key: "A", Title: "A", Description: "Produce A", Duty: orchestration.DutyExecutor, AcceptanceCriteria: []string{"A accepted"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit}},
			{Key: "B", Title: "B", Description: "Produce B", Duty: orchestration.DutyExecutor, AcceptanceCriteria: []string{"B accepted"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit}},
			{Key: "C", Title: "C", Description: "Integrate A and B", Duty: orchestration.DutyIntegrator, AcceptanceCriteria: []string{"delivery accepted"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindFinalDelivery}, DependsOn: []string{"A", "B"}},
		},
	}
}

func cleanupMultiRuntimeGoldenFixture(t *testing.T, pool *pgxpool.Pool, fixture multiRuntimeGoldenFixture) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`DELETE FROM agent_task_queue WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id=$1)`,
		`DELETE FROM orchestration_human_gate WHERE workspace_id=$1`, `DELETE FROM mission_role_policy_snapshot WHERE workspace_id=$1`, `DELETE FROM role_profile WHERE workspace_id=$1`,
		`DELETE FROM review_verdict WHERE workspace_id=$1`, `DELETE FROM artifact WHERE workspace_id=$1`, `DELETE FROM orchestration_run WHERE workspace_id=$1`,
		`DELETE FROM orchestration_assignment WHERE workspace_id=$1`, `DELETE FROM orchestration_activity WHERE workspace_id=$1`, `DELETE FROM task_node WHERE workspace_id=$1`,
		`DELETE FROM mission WHERE workspace_id=$1`, `DELETE FROM issue WHERE workspace_id=$1`, `DELETE FROM agent WHERE workspace_id=$1`,
		`DELETE FROM agent_runtime WHERE workspace_id=$1`, `DELETE FROM member WHERE workspace_id=$1`, `DELETE FROM workspace WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, fixture.workspaceID); err != nil {
			t.Errorf("cleanup %q: %v", statement, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, fixture.userID); err != nil {
		t.Errorf("cleanup user: %v", err)
	}
}

func goldenUUID() pgtype.UUID { id := uuid.New(); return pgtype.UUID{Bytes: id, Valid: true} }

func finishGoldenArtifactTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *orchestration.Repository, service *orchestration.Service, workspaceID pgtype.UUID, run db.OrchestrationRun, kind orchestration.ArtifactKind, uri string) db.Artifact {
	t.Helper()
	receipt := mustJSON(t, map[string]any{
		"schema_version": 1,
		"artifact": map[string]any{
			"kind": kind, "uri": uri, "content_hash": "", "summary": "golden " + string(kind),
			"metadata": map[string]any{"golden": true},
		},
	})
	completedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed', started_at=COALESCE(started_at,$2::timestamptz-interval '1 second'), completed_at=$2, result=$3 WHERE orchestration_run_id=$1`, run.ID, completedAt, mustJSON(t, map[string]any{"output": string(receipt)})); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: completedAt})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact == nil {
		t.Fatal("completed work output did not produce an Artifact")
	}
	if _, err := service.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: workspaceID, MissionID: run.MissionID, CorrelationID: run.ID}); err != nil {
		t.Fatal(err)
	}
	return *result.Artifact
}

func finishGoldenReviewTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *orchestration.Repository, service *orchestration.Service, workspaceID pgtype.UUID, run db.OrchestrationRun, decision orchestration.ReviewDecision) orchestration.AdvanceMissionResult {
	t.Helper()
	requestedChanges := []string{}
	if decision == orchestration.ReviewDecisionChangesRequested {
		requestedChanges = []string{"rework with regression evidence"}
	}
	receipt := mustJSON(t, map[string]any{
		"schema_version": 1, "decision": decision,
		"evidence": map[string]any{"golden": true}, "requested_changes": requestedChanges,
	})
	completedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed', started_at=COALESCE(started_at,$2::timestamptz-interval '1 second'), completed_at=$2, result=$3 WHERE orchestration_run_id=$1`, run.ID, completedAt, mustJSON(t, map[string]any{"output": string(receipt)})); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: completedAt})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReviewVerdict == nil || result.ReviewVerdict.Decision != string(decision) {
		t.Fatalf("completed review output did not produce decision %q: %#v", decision, result.ReviewVerdict)
	}
	advanced, err := service.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: workspaceID, MissionID: run.MissionID, CorrelationID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	return advanced
}

type goldenAssignmentIdentity struct {
	agent, runtime pgtype.UUID
	provider       string
}

func assignmentRuntimeProvider(t *testing.T, ctx context.Context, pool *pgxpool.Pool, assignmentID pgtype.UUID) goldenAssignmentIdentity {
	t.Helper()
	var result goldenAssignmentIdentity
	if err := pool.QueryRow(ctx, `SELECT assignment.agent_id,assignment.runtime_id,runtime.provider FROM orchestration_assignment assignment JOIN agent_runtime runtime ON runtime.id=assignment.runtime_id WHERE assignment.id=$1`, assignmentID).Scan(&result.agent, &result.runtime, &result.provider); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertGoldenAssignment(t *testing.T, ctx context.Context, pool *pgxpool.Pool, assignmentID, agentID, runtimeID pgtype.UUID, provider string) {
	t.Helper()
	got := assignmentRuntimeProvider(t, ctx, pool, assignmentID)
	if got.agent != agentID || got.runtime != runtimeID || got.provider != provider {
		t.Fatalf("assignment identity=%#v want agent=%v runtime=%v provider=%s", got, agentID, runtimeID, provider)
	}
}

func assertGoldenProviders(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runs []db.OrchestrationRun, providers ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, run := range runs {
		seen[assignmentRuntimeProvider(t, ctx, pool, run.AssignmentID).provider] = true
	}
	for _, provider := range providers {
		if !seen[provider] {
			t.Fatalf("parallel runs did not cover provider %q: %v", provider, seen)
		}
	}
}
