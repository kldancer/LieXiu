package service

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestOrchestrationWalkingSkeletonRetryReworkAndIntegration(t *testing.T) {
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
	fixture := createWalkingSkeletonFixture(t, ctx, pool)
	t.Cleanup(func() { cleanupWalkingSkeletonFixture(t, pool, fixture) })
	queries := db.New(pool)
	repository := orchestration.NewRepository(queries, pool)
	gateway := NewTaskExecutionGateway(NewTaskService(queries, pool, nil, events.New()))
	orchestrator := orchestration.NewService(queries, repository, gateway, orchestration.DefaultPlanHardLimits())
	startBindings := seedServiceRolePolicyBindings(t, ctx, repository, fixture.workspaceID, fixture.userID, orchestration.DutyExecutor, orchestration.DutyReviewer, orchestration.DutyIntegrator)

	created, err := orchestrator.CreateMission(ctx, orchestration.CreateMissionCommand{WorkspaceID: fixture.workspaceID, CommandID: walkingUUID(), ActorID: fixture.userID, Title: "A/B to C walking skeleton", Limits: orchestration.PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1}})
	if err != nil {
		t.Fatal(err)
	}
	missionID := created.Mission.IssueID
	plan := walkingSkeletonPlan(missionID)
	if _, err := orchestrator.SubmitPlan(ctx, orchestration.SubmitPlanCommand{WorkspaceID: fixture.workspaceID, MissionID: missionID, CommandID: walkingUUID(), ActorID: fixture.userID, ExpectedRevision: 1, Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.StartMission(ctx, orchestration.StartMissionCommand{WorkspaceID: fixture.workspaceID, MissionID: missionID, CommandID: walkingUUID(), ActorID: fixture.userID, ExpectedRevision: 2, RolePolicyBindings: startBindings}); err != nil {
		t.Fatal(err)
	}
	nodes := loadNodesByKey(t, ctx, queries, fixture.workspaceID, missionID)
	initial, err := orchestrator.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: fixture.workspaceID, MissionID: missionID, CorrelationID: walkingUUID()})
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.CreatedRuns) != 2 {
		t.Fatalf("initial runs=%d, want 2", len(initial.CreatedRuns))
	}
	var replayWorkers sync.WaitGroup
	replayErrors := make(chan error, 8)
	for range 8 {
		replayWorkers.Add(1)
		go func() {
			defer replayWorkers.Done()
			_, replayErr := orchestrator.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: fixture.workspaceID, MissionID: missionID, CorrelationID: initial.Activities[0].CorrelationID})
			replayErrors <- replayErr
		}()
	}
	replayWorkers.Wait()
	close(replayErrors)
	for replayErr := range replayErrors {
		if replayErr != nil {
			t.Fatalf("concurrent Advance replay: %v", replayErr)
		}
	}
	assertInitialDispatchUnique(t, ctx, pool, missionID)
	assertNoRunsForNode(t, ctx, queries, fixture.workspaceID, missionID, nodes["C"].IssueID)

	a1 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["A"].IssueID, "execute")
	b1 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["B"].IssueID, "execute")
	artifactA1 := finishWalkingArtifactTask(t, ctx, pool, repository, orchestrator, fixture.workspaceID, a1, orchestration.ArtifactKindCommit, "repo://A/v1")
	finishExecutionTask(t, ctx, pool, repository, fixture.workspaceID, b1, "failed", "runtime_offline")
	retry, err := orchestrator.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: fixture.workspaceID, MissionID: missionID, CorrelationID: walkingUUID()})
	if err != nil {
		t.Fatal(err)
	}
	if len(retry.CreatedRuns) != 1 || retry.CreatedRuns[0].Attempt != 2 || retry.CreatedRuns[0].RetryOfID != b1.ID || retry.CreatedRuns[0].AssignmentID != b1.AssignmentID {
		t.Fatalf("technical retry lost lineage: %#v", retry.CreatedRuns)
	}
	b2 := retry.CreatedRuns[0]
	artifactB := finishWalkingArtifactTask(t, ctx, pool, repository, orchestrator, fixture.workspaceID, b2, orchestration.ArtifactKindCommit, "repo://B/v1")
	reviewA1 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["A"].IssueID, "review")
	reviewB := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["B"].IssueID, "review")
	finishWalkingReviewTask(t, ctx, pool, repository, orchestrator, fixture.workspaceID, reviewB, orchestration.ReviewDecisionApproved, nil)
	rework := finishWalkingReviewTask(t, ctx, pool, repository, orchestrator, fixture.workspaceID, reviewA1, orchestration.ReviewDecisionChangesRequested, []string{"add regression evidence"})
	if rework.TaskNode.Status != string(orchestration.TaskStatusRework) || rework.TaskNode.ReworkCount != 1 {
		t.Fatalf("unexpected rework state: %#v", rework.TaskNode)
	}
	assertNoRunsForNode(t, ctx, queries, fixture.workspaceID, missionID, nodes["C"].IssueID)

	a2 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["A"].IssueID, "execute")
	if a2.ID == a1.ID || a2.Attempt != 1 || a2.AssignmentID == a1.AssignmentID || a2.RetryOfID.Valid {
		t.Fatalf("business rework reused technical retry lineage: %#v", a2)
	}
	artifactA2 := finishWalkingArtifactTask(t, ctx, pool, repository, orchestrator, fixture.workspaceID, a2, orchestration.ArtifactKindCommit, "repo://A/v2")
	if artifactA2.Version != 2 {
		t.Fatalf("A artifact version=%d, want 2", artifactA2.Version)
	}
	reviewA2 := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["A"].IssueID, "review")
	approvedA := finishWalkingReviewTask(t, ctx, pool, repository, orchestrator, fixture.workspaceID, reviewA2, orchestration.ReviewDecisionApproved, nil)
	if approvedA.TaskNode.Status != string(orchestration.TaskStatusCompleted) {
		t.Fatalf("A not completed: %#v", approvedA.TaskNode)
	}
	cRun := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["C"].IssueID, "integrate")
	assertIntegratorInput(t, cRun, []pgtype.UUID{artifactA2.ID, artifactB.ID}, artifactA1.ID)
	artifactC := finishWalkingArtifactTask(t, ctx, pool, repository, orchestrator, fixture.workspaceID, cRun, orchestration.ArtifactKindFinalDelivery, "repo://delivery/final")
	if artifactC.Kind != string(orchestration.ArtifactKindFinalDelivery) {
		t.Fatalf("C artifact kind=%q, want final_delivery", artifactC.Kind)
	}
	reviewC := latestRunForNodePurpose(t, ctx, queries, fixture.workspaceID, missionID, nodes["C"].IssueID, "review")
	final := finishWalkingReviewTask(t, ctx, pool, repository, orchestrator, fixture.workspaceID, reviewC, orchestration.ReviewDecisionApproved, nil)
	if final.Advance.Mission.Status != string(orchestration.MissionStatusCompleted) {
		t.Fatalf("mission status=%s, want completed", final.Advance.Mission.Status)
	}
	projection, err := orchestrator.GetMissionProjection(ctx, fixture.workspaceID, missionID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Mission.Status != orchestration.MissionStatusCompleted || projection.Mission.Progress.Completed != 3 || projection.Mission.Progress.Percent != 100 {
		t.Fatalf("completed projection lost mission progress: %#v", projection.Mission)
	}
	var projectedA *orchestration.TaskNodeProjection
	for index := range projection.Nodes {
		if projection.Nodes[index].Key == "A" {
			projectedA = &projection.Nodes[index]
			break
		}
	}
	if projectedA == nil || projectedA.LatestArtifact == nil || projectedA.LatestArtifact.Version != 2 || projectedA.LatestVerdict == nil || projectedA.LatestVerdict.Decision != orchestration.ReviewDecisionApproved {
		t.Fatalf("projection lost approved rework result: %#v", projectedA)
	}
	detail, err := orchestrator.GetRunDetail(ctx, fixture.workspaceID, missionID, a2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Lineage.Assignments) != 4 || len(detail.Lineage.Runs) != 4 || len(detail.Artifacts) != 2 || len(detail.Reviews) != 2 {
		t.Fatalf("run detail lost rework lineage: %#v", detail)
	}
	page, err := orchestrator.ListMissionActivities(ctx, fixture.workspaceID, missionID, projection.Mission.LastSequence-1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.ResetRequired || len(page.Items) != 1 || page.Items[0].Sequence != projection.Mission.LastSequence {
		t.Fatalf("activity tail cannot recover snapshot sequence: %#v", page)
	}
	assertWalkingSkeletonHistory(t, ctx, pool, missionID, nodes["A"].IssueID)
}

type walkingSkeletonFixture struct{ userID, workspaceID pgtype.UUID }

func createWalkingSkeletonFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) walkingSkeletonFixture {
	t.Helper()
	suffix := uuid.NewString()
	var f walkingSkeletonFixture
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('Walking skeleton',$1) RETURNING id`, "walking-"+suffix+"@liexiu.test").Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name,slug,description,issue_prefix) VALUES ('Walking skeleton',$1,'','WSK') RETURNING id`, "walking-"+suffix).Scan(&f.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, f.workspaceID, f.userID); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		var runtimeID pgtype.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,device_info,metadata,last_seen_at,visibility,owner_id) VALUES ($1,$2,'cloud','fake','online','test','{}',now(),'private',$3) RETURNING id`, f.workspaceID, "fake-runtime-"+string(rune('0'+index)), f.userID).Scan(&runtimeID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO agent (workspace_id,name,description,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id) VALUES ($1,$2,'','cloud','{}',$3,'private',1,$4)`, f.workspaceID, "fake-agent-"+string(rune('0'+index)), runtimeID, f.userID); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func cleanupWalkingSkeletonFixture(t *testing.T, pool *pgxpool.Pool, f walkingSkeletonFixture) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{`DELETE FROM mission_role_policy_snapshot WHERE workspace_id=$1`, `DELETE FROM role_profile WHERE workspace_id=$1`, `DELETE FROM review_verdict WHERE workspace_id=$1`, `DELETE FROM artifact WHERE workspace_id=$1`, `DELETE FROM orchestration_run WHERE workspace_id=$1`, `DELETE FROM orchestration_assignment WHERE workspace_id=$1`, `DELETE FROM orchestration_activity WHERE workspace_id=$1`, `DELETE FROM task_node WHERE workspace_id=$1`, `DELETE FROM mission WHERE workspace_id=$1`, `DELETE FROM workspace WHERE id=$1`} {
		if _, err := pool.Exec(ctx, statement, f.workspaceID); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, f.userID); err != nil {
		t.Errorf("cleanup user: %v", err)
	}
}
func walkingUUID() pgtype.UUID { id := uuid.New(); return pgtype.UUID{Bytes: id, Valid: true} }
func walkingSkeletonPlan(missionID pgtype.UUID) orchestration.Plan {
	id, _ := uuid.FromBytes(missionID.Bytes[:])
	return orchestration.Plan{SchemaVersion: 1, MissionID: id.String(), PlanKey: "walking-skeleton", Limits: orchestration.PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1}, Nodes: []orchestration.PlanNode{{Key: "A", Title: "A", Description: "A", Duty: orchestration.DutyExecutor, AcceptanceCriteria: []string{"A accepted"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit}}, {Key: "B", Title: "B", Description: "B", Duty: orchestration.DutyExecutor, AcceptanceCriteria: []string{"B accepted"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit}}, {Key: "C", Title: "C", Description: "C", Duty: orchestration.DutyIntegrator, AcceptanceCriteria: []string{"delivery accepted"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindFinalDelivery}, DependsOn: []string{"A", "B"}}}}
}
func loadNodesByKey(t *testing.T, ctx context.Context, q *db.Queries, workspaceID, missionID pgtype.UUID) map[string]db.TaskNode {
	t.Helper()
	rows, err := q.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]db.TaskNode{}
	for _, row := range rows {
		result[row.NodeKey] = row
	}
	return result
}
func latestRunForNodePurpose(t *testing.T, ctx context.Context, q *db.Queries, workspaceID, missionID, nodeID pgtype.UUID, purpose string) db.OrchestrationRun {
	t.Helper()
	rows, err := q.ListOrchestrationRunsByMission(ctx, db.ListOrchestrationRunsByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		t.Fatal(err)
	}
	var result db.OrchestrationRun
	found := false
	for _, row := range rows {
		if row.TaskNodeID == nodeID && row.Purpose == purpose {
			result = row
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s run for node", purpose)
	}
	return result
}
func assertNoRunsForNode(t *testing.T, ctx context.Context, q *db.Queries, workspaceID, missionID, nodeID pgtype.UUID) {
	t.Helper()
	rows, err := q.ListOrchestrationRunsByMission(ctx, db.ListOrchestrationRunsByMissionParams{WorkspaceID: workspaceID, MissionID: missionID})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.TaskNodeID == nodeID {
			t.Fatalf("unexpected run for blocked dependency: %#v", row)
		}
	}
}
func finishExecutionTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *orchestration.Repository, workspaceID pgtype.UUID, run db.OrchestrationRun, status, failure string) {
	t.Helper()
	if status == "completed" {
		t.Fatal("completed orchestration tasks require a structured output receipt")
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status=$2, started_at=COALESCE(started_at,now()-interval '1 second'), completed_at=now(), failure_reason=NULLIF($3,''), error=NULLIF($3,'') WHERE orchestration_run_id=$1`, run.ID, status, failure); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func finishWalkingArtifactTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *orchestration.Repository, orchestrator *orchestration.Service, workspaceID pgtype.UUID, run db.OrchestrationRun, kind orchestration.ArtifactKind, uri string) db.Artifact {
	t.Helper()
	receipt, err := json.Marshal(map[string]any{"schema_version": 1, "artifact": map[string]any{"kind": kind, "uri": uri, "content_hash": "", "summary": "walking skeleton", "metadata": map[string]any{"walking_skeleton": true}}})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{"output": string(receipt)})
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed', started_at=COALESCE(started_at,$2::timestamptz-interval '1 second'), completed_at=$2, result=$3 WHERE orchestration_run_id=$1`, run.ID, completedAt, envelope); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: completedAt})
	if err != nil || result.Artifact == nil {
		t.Fatalf("reconcile artifact: run=%#v task=%#v artifact=%#v err=%v", result.Run, result.TaskNode, result.Artifact, err)
	}
	replayed, err := repository.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: completedAt.Add(time.Second)})
	if err != nil || replayed.Artifact == nil || replayed.Artifact.ID != result.Artifact.ID || replayed.Changed {
		t.Fatalf("artifact replay changed result: first=%#v replay=%#v error=%v", result.Artifact, replayed.Artifact, err)
	}
	if _, err := orchestrator.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: workspaceID, MissionID: run.MissionID, CorrelationID: run.ID}); err != nil {
		t.Fatal(err)
	}
	return *result.Artifact
}

type walkingReviewResult struct {
	TaskNode db.TaskNode
	Advance  orchestration.AdvanceMissionResult
}

func finishWalkingReviewTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *orchestration.Repository, orchestrator *orchestration.Service, workspaceID pgtype.UUID, run db.OrchestrationRun, decision orchestration.ReviewDecision, changes []string) walkingReviewResult {
	t.Helper()
	receipt, err := json.Marshal(map[string]any{"schema_version": 1, "decision": decision, "evidence": map[string]any{"walking_skeleton": true}, "requested_changes": changes})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{"output": string(receipt)})
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed', started_at=COALESCE(started_at,$2::timestamptz-interval '1 second'), completed_at=$2, result=$3 WHERE orchestration_run_id=$1`, run.ID, completedAt, envelope); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: completedAt})
	if err != nil || result.ReviewVerdict == nil || result.ReviewVerdict.Decision != string(decision) {
		t.Fatalf("reconcile review: verdict=%#v err=%v", result.ReviewVerdict, err)
	}
	replayed, err := repository.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: completedAt.Add(time.Second)})
	if err != nil || replayed.ReviewVerdict == nil || replayed.ReviewVerdict.ID != result.ReviewVerdict.ID || replayed.Changed {
		t.Fatalf("review replay changed result: first=%#v replay=%#v error=%v", result.ReviewVerdict, replayed.ReviewVerdict, err)
	}
	advanced, err := orchestrator.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: workspaceID, MissionID: run.MissionID, CorrelationID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	return walkingReviewResult{TaskNode: result.TaskNode, Advance: advanced}
}
func assertWalkingSkeletonHistory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, missionID, aID pgtype.UUID) {
	t.Helper()
	var workAssignments, reviewAssignments, artifacts, verdicts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE role='executor'),count(*) FILTER (WHERE role='reviewer') FROM orchestration_assignment WHERE mission_id=$1 AND task_node_id=$2`, missionID, aID).Scan(&workAssignments, &reviewAssignments); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM artifact WHERE mission_id=$1 AND task_node_id=$2`, missionID, aID).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM review_verdict WHERE mission_id=$1 AND task_node_id=$2`, missionID, aID).Scan(&verdicts); err != nil {
		t.Fatal(err)
	}
	if workAssignments != 2 || reviewAssignments != 2 || artifacts != 2 || verdicts != 2 {
		t.Fatalf("history work=%d review=%d artifacts=%d verdicts=%d", workAssignments, reviewAssignments, artifacts, verdicts)
	}
	var count int
	var min, max int64
	if err := pool.QueryRow(ctx, `SELECT count(*),min(sequence),max(sequence) FROM orchestration_activity WHERE mission_id=$1`, missionID).Scan(&count, &min, &max); err != nil {
		t.Fatal(err)
	}
	if int64(count) != max-min+1 {
		t.Fatalf("activity sequence has gaps count=%d min=%d max=%d", count, min, max)
	}
}

func assertInitialDispatchUnique(t *testing.T, ctx context.Context, pool *pgxpool.Pool, missionID pgtype.UUID) {
	t.Helper()
	var runs, mappings, assignments int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_run WHERE mission_id=$1 AND purpose='execute'`, missionID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue task JOIN orchestration_run run ON run.id=task.orchestration_run_id WHERE run.mission_id=$1`, missionID).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_assignment WHERE mission_id=$1 AND role='executor' AND status='active'`, missionID).Scan(&assignments); err != nil {
		t.Fatal(err)
	}
	if runs != 2 || mappings != 2 || assignments != 2 {
		t.Fatalf("initial dispatch duplicated: runs=%d mappings=%d assignments=%d", runs, mappings, assignments)
	}
}

func assertIntegratorInput(t *testing.T, run db.OrchestrationRun, expected []pgtype.UUID, forbidden pgtype.UUID) {
	t.Helper()
	var input struct {
		Artifacts []struct {
			ArtifactID string `json:"artifact_id"`
		} `json:"dependency_artifacts"`
	}
	if err := json.Unmarshal(run.Input, &input); err != nil {
		t.Fatal(err)
	}
	actual := map[string]bool{}
	for _, artifact := range input.Artifacts {
		actual[artifact.ArtifactID] = true
	}
	for _, id := range expected {
		value := uuid.UUID(id.Bytes).String()
		if !actual[value] {
			t.Fatalf("integrator input is missing approved artifact %s: %s", value, run.Input)
		}
	}
	if actual[uuid.UUID(forbidden.Bytes).String()] {
		t.Fatalf("integrator input contains changes-requested artifact: %s", run.Input)
	}
}
