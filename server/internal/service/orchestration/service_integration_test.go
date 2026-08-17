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

func TestServiceOwnerValidationAndFourCommandLifecycle(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping orchestration service integration test")
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
	var ownerID, memberID, workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Command owner', $1) RETURNING id`, "command-owner-"+suffix+"@liexiu.test").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Command member', $1) RETURNING id`, "command-member-"+suffix+"@liexiu.test").Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ('Command service test', $1, '', 'CMD') RETURNING id`, "command-service-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'member')`, workspaceID, ownerID, memberID); err != nil {
		t.Fatal(err)
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
		for _, userID := range []pgtype.UUID{ownerID, memberID} {
			if _, cleanupErr := pool.Exec(cleanupCtx, `DELETE FROM "user" WHERE id = $1`, userID); cleanupErr != nil {
				t.Errorf("cleanup user: %v", cleanupErr)
			}
		}
	})

	queries := db.New(pool)
	service := NewService(queries, NewRepository(queries, pool), nil, DefaultPlanHardLimits())
	limits := PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1}

	quickCommandID := newTestUUID()
	quick, err := service.QuickCreateMission(ctx, QuickCreateMissionCommand{
		WorkspaceID: workspaceID,
		CommandID:   quickCommandID,
		ActorID:     ownerID,
		Prompt:      "Create a planned Mission without dispatching an AgentTask",
	})
	if err != nil {
		t.Fatalf("QuickCreateMission: %v", err)
	}
	if quick.Status != MissionStatusReady || quick.Revision != 2 || quick.Replayed {
		t.Fatalf("unexpected quick-create result: %#v", quick)
	}
	replayedQuick, err := service.QuickCreateMission(ctx, QuickCreateMissionCommand{
		WorkspaceID: workspaceID,
		CommandID:   quickCommandID,
		ActorID:     ownerID,
		Prompt:      "a reused command id must not change the persisted prompt",
	})
	if err != nil {
		t.Fatalf("replay QuickCreateMission: %v", err)
	}
	if !replayedQuick.Replayed || replayedQuick.MissionID != quick.MissionID || replayedQuick.Status != MissionStatusReady {
		t.Fatalf("QuickCreateMission replay did not return the original ready Mission: %#v", replayedQuick)
	}
	var quickNodes, quickAgentTasks int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM task_node WHERE workspace_id = $1 AND mission_id = $2`, workspaceID, quick.MissionID).Scan(&quickNodes); err != nil {
		t.Fatalf("count quick-create task nodes: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_task_queue task
		JOIN orchestration_run run ON run.id = task.orchestration_run_id
		WHERE run.workspace_id = $1
	`, workspaceID).Scan(&quickAgentTasks); err != nil {
		t.Fatalf("count quick-create AgentTasks: %v", err)
	}
	if quickNodes != 2 || quickAgentTasks != 0 {
		t.Fatalf("quick-create materialization: nodes=%d agent_tasks=%d, want 2 and 0", quickNodes, quickAgentTasks)
	}
	var blockedNodeID pgtype.UUID
	var blockedNodeRevision int64
	if err := pool.QueryRow(ctx, `SELECT issue_id, revision FROM task_node WHERE workspace_id=$1 AND mission_id=$2 AND node_key='execute'`, workspaceID, quick.MissionID).Scan(&blockedNodeID, &blockedNodeRevision); err != nil {
		t.Fatalf("load quick-create executor node: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mission SET status='blocked' WHERE issue_id=$1 AND workspace_id=$2`, quick.MissionID, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE task_node SET status='blocked', block_reason='runtime offline' WHERE issue_id=$1 AND workspace_id=$2`, blockedNodeID, workspaceID); err != nil {
		t.Fatal(err)
	}
	retryCommandID := newTestUUID()
	retriedNode, err := service.RetryTaskNode(ctx, RetryTaskNodeCommand{
		WorkspaceID: workspaceID, MissionID: quick.MissionID, TaskNodeID: blockedNodeID,
		CommandID: retryCommandID, ActorID: ownerID,
		ExpectedRevision: 2, ExpectedTaskRevision: blockedNodeRevision,
		Reason: "runtime recovered",
	})
	if err != nil {
		t.Fatalf("RetryTaskNode: %v", err)
	}
	if retriedNode.TaskNode.Status != string(TaskStatusPending) || retriedNode.Mission.Revision != 3 || retriedNode.Idempotent {
		t.Fatalf("unexpected RetryTaskNode transaction result: %#v", retriedNode)
	}
	if len(retriedNode.Advance.CreatedRuns) != 0 {
		t.Fatalf("RetryTaskNode created a run without an available agent: %#v", retriedNode.Advance.CreatedRuns)
	}
	replayedRetry, err := service.RetryTaskNode(ctx, RetryTaskNodeCommand{
		WorkspaceID: workspaceID, MissionID: quick.MissionID, TaskNodeID: blockedNodeID,
		CommandID: retryCommandID, ActorID: ownerID,
		ExpectedRevision: 2, ExpectedTaskRevision: blockedNodeRevision,
		Reason: "changed replay payload is ignored",
	})
	if err != nil || !replayedRetry.Idempotent || replayedRetry.Mission.IssueID != quick.MissionID {
		t.Fatalf("RetryTaskNode replay changed result: result=%#v error=%v", replayedRetry, err)
	}

	_, err = service.CreateMission(ctx, CreateMissionCommand{
		WorkspaceID: workspaceID, CommandID: newTestUUID(), ActorID: memberID,
		Title: "Member must not create a mission", Limits: limits,
	})
	if !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("member CreateMission error = %v, want ErrOwnerRequired", err)
	}

	_, err = service.CreateMission(ctx, CreateMissionCommand{
		WorkspaceID: workspaceID, CommandID: newTestUUID(), ActorID: ownerID,
		Title: " ", Limits: PlanLimits{MaxParallelRuns: 99},
	})
	var validationErr CommandValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Errors) < 3 {
		t.Fatalf("invalid CreateMission error = %#v, want multiple validation errors", err)
	}

	created, err := service.CreateMission(ctx, CreateMissionCommand{
		WorkspaceID: workspaceID, CommandID: newTestUUID(), ActorID: ownerID,
		Title: "Four command lifecycle", Description: pgtype.Text{String: "service integration", Valid: true},
		Limits: limits,
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	invalidPlan := validRepositoryTestPlan(uuid.NewString())
	_, err = service.SubmitPlan(ctx, SubmitPlanCommand{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: newTestUUID(), ActorID: ownerID, ExpectedRevision: 1,
		Plan: invalidPlan,
	})
	if !errors.As(err, &validationErr) {
		t.Fatalf("invalid SubmitPlan error = %v, want CommandValidationError", err)
	}
	mission, err := queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{
		IssueID: created.Mission.IssueID, WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mission.Status != string(MissionStatusDraft) || mission.Revision != 1 {
		t.Fatalf("invalid plan changed mission: status=%s revision=%d", mission.Status, mission.Revision)
	}

	plan := validRepositoryTestPlan(uuidText(created.Mission.IssueID))
	planned, err := service.SubmitPlan(ctx, SubmitPlanCommand{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: newTestUUID(), ActorID: ownerID, ExpectedRevision: 1,
		Plan: plan,
	})
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}
	if planned.Mission.Revision != 2 {
		t.Fatalf("planned revision = %d, want 2", planned.Mission.Revision)
	}

	_, err = service.StartMission(ctx, StartMissionCommand{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: newTestUUID(), ActorID: ownerID, ExpectedRevision: 1,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale StartMission error = %v, want ErrRevisionConflict", err)
	}
	started, err := service.StartMission(ctx, StartMissionCommand{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: newTestUUID(), ActorID: ownerID, ExpectedRevision: 2,
	})
	if err != nil {
		t.Fatalf("StartMission: %v", err)
	}
	if started.Mission.Status != string(MissionStatusRunning) {
		t.Fatalf("started status = %s, want running", started.Mission.Status)
	}

	_, err = service.CancelMission(ctx, CancelMissionCommand{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: newTestUUID(), ActorID: memberID, ExpectedRevision: 3,
	})
	if !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("member CancelMission error = %v, want ErrOwnerRequired", err)
	}
	cancelled, err := service.CancelMission(ctx, CancelMissionCommand{
		WorkspaceID: workspaceID, MissionID: created.Mission.IssueID,
		CommandID: newTestUUID(), ActorID: ownerID, ExpectedRevision: 3,
		Reason: "  stop requested by owner  ",
	})
	if err != nil {
		t.Fatalf("CancelMission: %v", err)
	}
	if cancelled.Mission.Status != string(MissionStatusCancelled) || cancelled.Mission.Revision != 4 {
		t.Fatalf("cancelled mission: status=%s revision=%d", cancelled.Mission.Status, cancelled.Mission.Revision)
	}
}
