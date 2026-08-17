package orchestration

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestRepositoryMissionPlanTransactionAndIdempotency(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping orchestration repository integration test")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	var schemaReady bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('orchestration_activity') IS NOT NULL`).Scan(&schemaReady); err != nil {
		t.Fatalf("check orchestration schema: %v", err)
	}
	if !schemaReady {
		t.Skip("orchestration migrations are not applied")
	}

	suffix := uuid.NewString()
	var userID, workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Orchestration repository test', $1)
		RETURNING id
	`, "orchestration-repository-"+suffix+"@liexiu.test").Scan(&userID); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Orchestration repository test', $1, '', 'ORT')
		RETURNING id
	`, "orchestration-repository-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatalf("create test workspace: %v", err)
	}
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
			if _, cleanupErr := pool.Exec(cleanupCtx, statement, workspaceID); cleanupErr != nil {
				t.Errorf("cleanup %q: %v", statement, cleanupErr)
			}
		}
		if _, cleanupErr := pool.Exec(cleanupCtx, `DELETE FROM "user" WHERE id = $1`, userID); cleanupErr != nil {
			t.Errorf("cleanup user: %v", cleanupErr)
		}
	})

	repository := NewRepository(db.New(pool), pool)
	createCommandID := newTestUUID()
	created, err := repository.CreateMission(ctx, CreateMissionParams{
		WorkspaceID: workspaceID,
		CommandID:   createCommandID,
		ActorID:     userID,
		Title:       "Repository walking skeleton",
		Description: pgtype.Text{String: "Persist a deterministic mission plan", Valid: true},
		Limits: PlanLimits{
			MaxParallelRuns: 2,
			MaxTaskAttempts: 2,
			MaxReworkCycles: 1,
		},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	if created.Mission.Status != string(MissionStatusDraft) || created.Mission.Revision != 1 {
		t.Fatalf("unexpected created mission: status=%s revision=%d", created.Mission.Status, created.Mission.Revision)
	}
	if created.Activity.Sequence != 1 || created.Activity.Type != activityMissionCreated {
		t.Fatalf("unexpected create activity: type=%s sequence=%d", created.Activity.Type, created.Activity.Sequence)
	}

	replayedCreate, err := repository.CreateMission(ctx, CreateMissionParams{
		WorkspaceID: workspaceID,
		CommandID:   createCommandID,
		ActorID:     userID,
		Title:       "This replay must not create a second issue",
		Limits:      DefaultPlanHardLimits(),
	})
	if err != nil {
		t.Fatalf("replay CreateMission: %v", err)
	}
	if !replayedCreate.Idempotent || replayedCreate.Mission.IssueID != created.Mission.IssueID {
		t.Fatalf("CreateMission replay did not return the original mission")
	}

	planCommandID := newTestUUID()
	plan := validRepositoryTestPlan(uuidText(created.Mission.IssueID))
	planned, err := repository.SubmitPlan(ctx, SubmitPlanParams{
		WorkspaceID:      workspaceID,
		MissionID:        created.Mission.IssueID,
		CommandID:        planCommandID,
		ActorType:        "user",
		ActorID:          userID,
		ExpectedRevision: 1,
		Plan:             plan,
	})
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}
	if planned.Mission.Status != string(MissionStatusReady) || planned.Mission.Revision != 2 {
		t.Fatalf("unexpected planned mission: status=%s revision=%d", planned.Mission.Status, planned.Mission.Revision)
	}
	if len(planned.TaskNodes) != 2 || len(planned.Dependencies) != 1 {
		t.Fatalf("unexpected plan materialization: nodes=%d dependencies=%d", len(planned.TaskNodes), len(planned.Dependencies))
	}
	if planned.Activity.Sequence != 2 || planned.Activity.Type != activityMissionPlanAccepted {
		t.Fatalf("unexpected plan activity: type=%s sequence=%d", planned.Activity.Type, planned.Activity.Sequence)
	}

	replayedPlan, err := repository.SubmitPlan(ctx, SubmitPlanParams{
		WorkspaceID:      workspaceID,
		MissionID:        created.Mission.IssueID,
		CommandID:        planCommandID,
		ActorType:        "user",
		ActorID:          userID,
		ExpectedRevision: 1,
		Plan:             plan,
	})
	if err != nil {
		t.Fatalf("replay SubmitPlan: %v", err)
	}
	if !replayedPlan.Idempotent || len(replayedPlan.TaskNodes) != 2 || len(replayedPlan.Dependencies) != 1 {
		t.Fatalf("SubmitPlan replay did not return the original materialization")
	}

	var rootStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, created.Mission.IssueID).Scan(&rootStatus); err != nil {
		t.Fatalf("load root compatibility status: %v", err)
	}
	if rootStatus != "todo" {
		t.Fatalf("root issue compatibility status = %q, want todo", rootStatus)
	}
	assertOrchestrationRelationshipsComplete(t, ctx, pool, workspaceID, created.Mission.IssueID)

	startCommandID := newTestUUID()
	started, err := repository.StartMission(ctx, StartMissionParams{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: startCommandID, ActorID: userID, ExpectedRevision: 2,
	})
	if err != nil {
		t.Fatalf("StartMission: %v", err)
	}
	if started.Mission.Status != string(MissionStatusRunning) || started.Mission.Revision != 3 {
		t.Fatalf("unexpected started mission: status=%s revision=%d", started.Mission.Status, started.Mission.Revision)
	}
	if len(started.Activities) != 2 || started.Activities[0].Type != activityMissionStarted || started.Activities[1].Type != activityTaskReady {
		t.Fatalf("unexpected StartMission activities: %#v", started.Activities)
	}
	if started.Activities[0].Sequence != 3 || started.Activities[1].Sequence != 4 {
		t.Fatalf("StartMission activity sequences = %d,%d, want 3,4", started.Activities[0].Sequence, started.Activities[1].Sequence)
	}
	startedStatuses := taskStatusesByKey(started.TaskNodes)
	if startedStatuses["A"] != TaskStatusReady || startedStatuses["B"] != TaskStatusPending {
		t.Fatalf("unexpected task statuses after start: %#v", startedStatuses)
	}
	replayedStart, err := repository.StartMission(ctx, StartMissionParams{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: startCommandID, ActorID: userID, ExpectedRevision: 2,
	})
	if err != nil {
		t.Fatalf("replay StartMission: %v", err)
	}
	if !replayedStart.Idempotent || len(replayedStart.Activities) != 2 {
		t.Fatalf("StartMission replay did not return the original result")
	}
	if _, err := pool.Exec(ctx, `UPDATE task_node SET status = 'completed' WHERE mission_id = $1 AND node_key = 'B'`, created.Mission.IssueID); err != nil {
		t.Fatalf("prepare completed task history: %v", err)
	}

	cancelCommandID := newTestUUID()
	cancelled, err := repository.CancelMission(ctx, CancelMissionParams{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: cancelCommandID, ActorID: userID, ExpectedRevision: 3,
		Reason: "owner stopped the mission",
	})
	if err != nil {
		t.Fatalf("CancelMission: %v", err)
	}
	if cancelled.Mission.Status != string(MissionStatusCancelled) || cancelled.Mission.Revision != 4 {
		t.Fatalf("unexpected cancelled mission: status=%s revision=%d", cancelled.Mission.Status, cancelled.Mission.Revision)
	}
	if len(cancelled.ActiveRuns) != 0 || len(cancelled.Activities) != 2 {
		t.Fatalf("unexpected CancelMission result: active_runs=%d activities=%d", len(cancelled.ActiveRuns), len(cancelled.Activities))
	}
	for index, activity := range cancelled.Activities {
		if activity.Sequence != int64(index+5) {
			t.Fatalf("CancelMission activity %d sequence = %d, want %d", index, activity.Sequence, index+5)
		}
	}
	cancelledStatuses := taskStatusesByKey(cancelled.TaskNodes)
	if cancelledStatuses["A"] != TaskStatusCancelled || cancelledStatuses["B"] != TaskStatusCompleted {
		t.Fatalf("unexpected task statuses after cancel: %#v", cancelledStatuses)
	}
	replayedCancel, err := repository.CancelMission(ctx, CancelMissionParams{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: cancelCommandID, ActorID: userID, ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatalf("replay CancelMission: %v", err)
	}
	if !replayedCancel.Idempotent || len(replayedCancel.Activities) != 2 {
		t.Fatalf("CancelMission replay did not return the original result")
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, created.Mission.IssueID).Scan(&rootStatus); err != nil {
		t.Fatalf("reload root compatibility status: %v", err)
	}
	if rootStatus != "cancelled" {
		t.Fatalf("root issue compatibility status after cancel = %q, want cancelled", rootStatus)
	}
	assertMissionActivitySequences(t, ctx, pool, created.Mission.IssueID, []int64{1, 2, 3, 4, 5, 6})

	_, err = repository.SubmitPlan(ctx, SubmitPlanParams{
		WorkspaceID: workspaceID,
		MissionID:   created.Mission.IssueID,
		CommandID:   createCommandID,
		ActorType:   "user",
		ActorID:     userID,
		Plan:        plan,
	})
	if !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("command id reuse error = %v, want ErrCommandConflict", err)
	}

	concurrentMission, err := repository.CreateMission(ctx, CreateMissionParams{
		WorkspaceID: workspaceID,
		CommandID:   newTestUUID(),
		ActorID:     userID,
		Title:       "Concurrent replay mission",
		Limits:      DefaultPlanHardLimits(),
	})
	if err != nil {
		t.Fatalf("create concurrent replay mission: %v", err)
	}
	concurrentPlan := validRepositoryTestPlan(uuidText(concurrentMission.Mission.IssueID))
	concurrentCommandID := newTestUUID()
	type concurrentResult struct {
		result SubmitPlanResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan concurrentResult, 2)
	for range 2 {
		go func() {
			<-start
			result, submitErr := repository.SubmitPlan(ctx, SubmitPlanParams{
				WorkspaceID: workspaceID, MissionID: concurrentMission.Mission.IssueID,
				CommandID: concurrentCommandID, ActorType: "user", ActorID: userID,
				ExpectedRevision: 1, Plan: concurrentPlan,
			})
			results <- concurrentResult{result: result, err: submitErr}
		}()
	}
	close(start)
	idempotentResults := 0
	for range 2 {
		outcome := <-results
		if outcome.err != nil {
			t.Fatalf("concurrent SubmitPlan: %v", outcome.err)
		}
		if outcome.result.Idempotent {
			idempotentResults++
		}
	}
	if idempotentResults != 1 {
		t.Fatalf("concurrent SubmitPlan idempotent results = %d, want 1", idempotentResults)
	}
	var concurrentNodes, concurrentActivities int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM task_node WHERE mission_id = $1`, concurrentMission.Mission.IssueID).Scan(&concurrentNodes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_activity WHERE mission_id = $1`, concurrentMission.Mission.IssueID).Scan(&concurrentActivities); err != nil {
		t.Fatal(err)
	}
	if concurrentNodes != 2 || concurrentActivities != 2 {
		t.Fatalf("concurrent SubmitPlan duplicated rows: nodes=%d activities=%d", concurrentNodes, concurrentActivities)
	}
}

func TestRepositorySubmitPlanRollsBackIncompleteRelationships(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping orchestration repository integration test")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	var schemaReady bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('mission') IS NOT NULL`).Scan(&schemaReady); err != nil || !schemaReady {
		t.Skip("orchestration migrations are not applied")
	}
	suffix := uuid.NewString()
	var userID, workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Orchestration rollback test', $1) RETURNING id`, "orchestration-rollback-"+suffix+"@liexiu.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ('Orchestration rollback test', $1, '', 'ORB') RETURNING id`, "orchestration-rollback-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		pool.Exec(cleanupCtx, `DELETE FROM orchestration_activity WHERE workspace_id = $1`, workspaceID) //nolint:errcheck
		pool.Exec(cleanupCtx, `DELETE FROM task_node WHERE workspace_id = $1`, workspaceID)              //nolint:errcheck
		pool.Exec(cleanupCtx, `DELETE FROM mission WHERE workspace_id = $1`, workspaceID)                //nolint:errcheck
		pool.Exec(cleanupCtx, `DELETE FROM workspace WHERE id = $1`, workspaceID)                        //nolint:errcheck
		pool.Exec(cleanupCtx, `DELETE FROM "user" WHERE id = $1`, userID)                                //nolint:errcheck
	})

	repository := NewRepository(db.New(pool), pool)
	created, err := repository.CreateMission(ctx, CreateMissionParams{
		WorkspaceID: workspaceID,
		CommandID:   newTestUUID(),
		ActorID:     userID,
		Title:       "Rollback mission",
		Limits:      DefaultPlanHardLimits(),
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	invalidPlan := Plan{
		SchemaVersion: PlanSchemaVersion,
		MissionID:     uuidText(created.Mission.IssueID),
		PlanKey:       "rollback-plan",
		Limits:        DefaultPlanHardLimits(),
		Nodes: []PlanNode{{
			Key: "A", Title: "Incomplete node", Description: "References a missing node",
			Role: RoleExecutor, AcceptanceCriteria: []string{"must roll back"},
			ArtifactKinds: []ArtifactKind{ArtifactKindCommit}, DependsOn: []string{"missing"},
		}},
	}
	_, err = repository.SubmitPlan(ctx, SubmitPlanParams{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: newTestUUID(), ActorType: "user", ActorID: userID,
		ExpectedRevision: 1, Plan: invalidPlan,
	})
	if err == nil {
		t.Fatal("SubmitPlan unexpectedly accepted an incomplete relationship")
	}

	mission, err := db.New(pool).GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{
		IssueID: created.Mission.IssueID, WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatalf("reload mission: %v", err)
	}
	if mission.Status != string(MissionStatusDraft) || mission.Revision != 1 {
		t.Fatalf("failed SubmitPlan leaked mission state: status=%s revision=%d", mission.Status, mission.Revision)
	}
	var childIssues, taskNodes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE parent_issue_id = $1`, created.Mission.IssueID).Scan(&childIssues); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM task_node WHERE mission_id = $1`, created.Mission.IssueID).Scan(&taskNodes); err != nil {
		t.Fatal(err)
	}
	if childIssues != 0 || taskNodes != 0 {
		t.Fatalf("failed SubmitPlan leaked rows: child_issues=%d task_nodes=%d", childIssues, taskNodes)
	}
}

func validRepositoryTestPlan(missionID string) Plan {
	return Plan{
		SchemaVersion: PlanSchemaVersion,
		MissionID:     missionID,
		PlanKey:       "repository-walking-skeleton",
		Limits: PlanLimits{
			MaxParallelRuns: 2,
			MaxTaskAttempts: 2,
			MaxReworkCycles: 1,
		},
		Nodes: []PlanNode{
			{
				Key: "A", Title: "Produce an artifact", Description: "Produce a reviewable artifact",
				Role: RoleExecutor, AcceptanceCriteria: []string{"artifact exists"},
				ArtifactKinds: []ArtifactKind{ArtifactKindCommit},
			},
			{
				Key: "B", Title: "Integrate the artifact", Description: "Create the final delivery",
				Role: RoleIntegrator, AcceptanceCriteria: []string{"delivery exists"},
				ArtifactKinds: []ArtifactKind{ArtifactKindFinalDelivery}, DependsOn: []string{"A"},
			},
		},
	}
}

func assertOrchestrationRelationshipsComplete(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, missionID pgtype.UUID) {
	t.Helper()
	checks := []struct {
		name  string
		query string
	}{
		{"mission issue", `SELECT count(*) FROM mission m LEFT JOIN issue i ON i.id = m.issue_id AND i.workspace_id = m.workspace_id WHERE m.workspace_id = $1 AND m.issue_id = $2 AND i.id IS NULL`},
		{"task issue and mission", `SELECT count(*) FROM task_node n LEFT JOIN issue i ON i.id = n.issue_id AND i.workspace_id = n.workspace_id LEFT JOIN mission m ON m.issue_id = n.mission_id AND m.workspace_id = n.workspace_id WHERE n.workspace_id = $1 AND n.mission_id = $2 AND (i.id IS NULL OR m.issue_id IS NULL)`},
		{"task dependency endpoints", `SELECT count(*) FROM issue_dependency d JOIN task_node n ON n.issue_id = d.issue_id LEFT JOIN task_node predecessor ON predecessor.issue_id = d.depends_on_issue_id AND predecessor.mission_id = n.mission_id WHERE n.workspace_id = $1 AND n.mission_id = $2 AND d.type = 'blocked_by' AND predecessor.issue_id IS NULL`},
		{"activity mission", `SELECT count(*) FROM orchestration_activity a LEFT JOIN mission m ON m.issue_id = a.mission_id AND m.workspace_id = a.workspace_id WHERE a.workspace_id = $1 AND a.mission_id = $2 AND m.issue_id IS NULL`},
	}
	for _, check := range checks {
		var count int
		if err := pool.QueryRow(ctx, check.query, workspaceID, missionID).Scan(&count); err != nil {
			t.Fatalf("check %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s has %d incomplete relationships", check.name, count)
		}
	}
	var issueCount, activityCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE workspace_id = $1`, workspaceID).Scan(&issueCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_activity WHERE workspace_id = $1`, workspaceID).Scan(&activityCount); err != nil {
		t.Fatal(err)
	}
	if issueCount != 3 || activityCount != 2 {
		t.Fatalf("idempotency counts: issues=%d activities=%d, want 3 and 2", issueCount, activityCount)
	}
}

func newTestUUID() pgtype.UUID {
	value := uuid.New()
	return pgtype.UUID{Bytes: value, Valid: true}
}

func taskStatusesByKey(nodes []db.TaskNode) map[string]TaskStatus {
	statuses := make(map[string]TaskStatus, len(nodes))
	for _, node := range nodes {
		statuses[node.NodeKey] = TaskStatus(node.Status)
	}
	return statuses
}

func assertMissionActivitySequences(t *testing.T, ctx context.Context, pool *pgxpool.Pool, missionID pgtype.UUID, expected []int64) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT sequence FROM orchestration_activity WHERE mission_id = $1 ORDER BY sequence`, missionID)
	if err != nil {
		t.Fatalf("list mission activity sequences: %v", err)
	}
	defer rows.Close()
	var actual []int64
	for rows.Next() {
		var sequence int64
		if err := rows.Scan(&sequence); err != nil {
			t.Fatalf("scan mission activity sequence: %v", err)
		}
		actual = append(actual, sequence)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate mission activity sequences: %v", err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("mission activity sequence count = %d, want %d: %#v", len(actual), len(expected), actual)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("mission activity sequences = %#v, want %#v", actual, expected)
		}
	}
}
