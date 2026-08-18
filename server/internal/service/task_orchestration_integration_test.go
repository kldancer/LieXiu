package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

func TestTaskExecutionGatewayEnqueueIsRunIdempotent(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping orchestration execution gateway integration test")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)
	var schemaReady bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('orchestration_run') IS NOT NULL`).Scan(&schemaReady); err != nil {
		t.Fatalf("check orchestration schema: %v", err)
	}
	if !schemaReady {
		t.Skip("orchestration migrations are not applied")
	}

	fixture := createExecutionGatewayFixture(t, ctx, pool)
	t.Cleanup(func() { cleanupExecutionGatewayFixture(t, pool, fixture) })
	bus := events.New()
	var queuedEvents atomic.Int32
	bus.Subscribe(protocol.EventTaskQueued, func(events.Event) { queuedEvents.Add(1) })
	taskService := NewTaskService(db.New(pool), pool, nil, bus)
	gateway := NewTaskExecutionGateway(taskService)

	request := orchestration.EnqueueExecutionRequest{
		WorkspaceID: fixture.workspaceID,
		RunID:       fixture.runID,
		ActorID:     fixture.userID,
	}
	type outcome struct {
		result orchestration.EnqueueExecutionResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 8)
	for range 8 {
		go func() {
			<-start
			result, enqueueErr := gateway.Enqueue(ctx, request)
			outcomes <- outcome{result: result, err: enqueueErr}
		}()
	}
	close(start)
	var taskID pgtype.UUID
	idempotentCount := 0
	for range 8 {
		current := <-outcomes
		if current.err != nil {
			t.Fatalf("concurrent Enqueue: %v", current.err)
		}
		if !taskID.Valid {
			taskID = current.result.AgentTaskID
		} else if current.result.AgentTaskID != taskID {
			t.Fatalf("run mapped to multiple tasks: %v and %v", taskID, current.result.AgentTaskID)
		}
		if current.result.Idempotent {
			idempotentCount++
		}
	}
	if idempotentCount != 7 {
		t.Fatalf("idempotent enqueue results = %d, want 7", idempotentCount)
	}
	if queuedEvents.Load() != 1 {
		t.Fatalf("task queued events = %d, want 1", queuedEvents.Load())
	}

	var mappingCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE orchestration_run_id = $1`, fixture.runID).Scan(&mappingCount); err != nil {
		t.Fatal(err)
	}
	if mappingCount != 1 {
		t.Fatalf("run mapping count = %d, want 1", mappingCount)
	}
	task, err := db.New(pool).GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatalf("load bridged task: %v", err)
	}
	if task.AgentID != fixture.agentID || task.RuntimeID != fixture.runtimeID || task.IssueID != fixture.taskNodeID {
		t.Fatalf("bridged task does not match frozen assignment: %#v", task)
	}
	if task.OrchestrationRunID != fixture.runID || !task.ForceFreshSession {
		t.Fatalf("bridged task is missing run identity or fresh-session fence: %#v", task)
	}
	if !task.TriggerEvidenceKind.Valid || task.TriggerEvidenceKind.String != "orchestration_run" || task.TriggerEvidenceRefID != fixture.runID {
		t.Fatalf("bridged task has wrong trigger evidence: %#v", task)
	}

	replayed, err := gateway.Enqueue(ctx, request)
	if err != nil {
		t.Fatalf("replay Enqueue after committed result: %v", err)
	}
	if !replayed.Idempotent || replayed.AgentTaskID != taskID || queuedEvents.Load() != 1 {
		t.Fatalf("committed replay changed observable result: %#v events=%d", replayed, queuedEvents.Load())
	}
	_, err = gateway.Enqueue(ctx, orchestration.EnqueueExecutionRequest{
		WorkspaceID: orchestrationTestUUID(), RunID: fixture.runID, ActorID: fixture.userID,
	})
	if !errors.Is(err, ErrOrchestrationRunNotDispatchable) {
		t.Fatalf("cross-workspace Enqueue error = %v, want ErrOrchestrationRunNotDispatchable", err)
	}
	_, err = gateway.Enqueue(ctx, orchestration.EnqueueExecutionRequest{
		WorkspaceID: fixture.workspaceID, RunID: fixture.runID,
	})
	if !errors.Is(err, ErrOrchestrationRunNotDispatchable) {
		t.Fatalf("invalid actor Enqueue error = %v, want ErrOrchestrationRunNotDispatchable", err)
	}
	_, err = gateway.Cancel(ctx, orchestration.CancelExecutionRequest{AgentTaskID: orchestrationTestUUID()})
	if !errors.Is(err, ErrNotOrchestrationTask) {
		t.Fatalf("unmapped Cancel error = %v, want ErrNotOrchestrationTask", err)
	}

	orchestrator := orchestration.NewService(
		db.New(pool), orchestration.NewRepository(db.New(pool), pool), gateway,
		orchestration.DefaultPlanHardLimits(),
	)
	cancelCommandID := orchestrationTestUUID()
	cancelledMission, err := orchestrator.CancelMission(ctx, orchestration.CancelMissionCommand{
		WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID,
		CommandID: cancelCommandID, ActorID: fixture.userID, ExpectedRevision: 1,
		Reason: "mission cancelled",
	})
	if err != nil {
		t.Fatalf("CancelMission through execution gateway: %v", err)
	}
	if cancelledMission.Mission.Status != string(orchestration.MissionStatusCancelled) || len(cancelledMission.ActiveRuns) != 1 {
		t.Fatalf("unexpected cancelled mission result: %#v", cancelledMission)
	}
	cancelledTask, err := db.New(pool).GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatalf("load cancelled execution task: %v", err)
	}
	if cancelledTask.Status != "cancelled" {
		t.Fatalf("cancelled execution task status = %q, want cancelled", cancelledTask.Status)
	}
	replayedCancel, err := orchestrator.CancelMission(ctx, orchestration.CancelMissionCommand{
		WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID,
		CommandID: cancelCommandID, ActorID: fixture.userID, ExpectedRevision: 1,
		Reason: "mission cancelled",
	})
	if err != nil {
		t.Fatalf("replay CancelMission: %v", err)
	}
	if !replayedCancel.Idempotent || replayedCancel.Mission.Status != string(orchestration.MissionStatusCancelled) {
		t.Fatalf("cancel replay changed result: %#v", replayedCancel)
	}
}

func TestTaskExecutionGatewayEnqueuesMissionScopedPlanningRun(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='orchestration_run_scope_purpose_check')`).Scan(&planningReady); err != nil {
		t.Fatal(err)
	}
	if !planningReady {
		t.Skip("planning scope migrations are not applied")
	}
	fixture := createExecutionGatewayFixture(t, ctx, pool)
	t.Cleanup(func() { cleanupExecutionGatewayFixture(t, pool, fixture) })
	if _, err := pool.Exec(ctx, `UPDATE mission SET status='draft' WHERE issue_id=$1`, fixture.missionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_assignment SET task_node_id=NULL, role='planner' WHERE id=$1`, fixture.assignmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_run SET task_node_id=NULL, purpose='plan', input='{"schema_version":1,"mission_id":"test"}' WHERE id=$1`, fixture.runID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM task_node WHERE issue_id=$1`, fixture.taskNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, fixture.taskNodeID); err != nil {
		t.Fatal(err)
	}
	gateway := NewTaskExecutionGateway(NewTaskService(db.New(pool), pool, nil, events.New()))
	result, err := gateway.Enqueue(ctx, orchestration.EnqueueExecutionRequest{WorkspaceID: fixture.workspaceID, RunID: fixture.runID, ActorID: fixture.userID})
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.New(pool).GetAgentTask(ctx, result.AgentTaskID)
	if err != nil {
		t.Fatal(err)
	}
	var taskContext map[string]any
	if err := json.Unmarshal(task.Context, &taskContext); err != nil {
		t.Fatalf("decode planning context: %v", err)
	}
	if task.IssueID != fixture.missionID || taskContext["mission_id"] != "test" || taskContext["schema_version"] != float64(1) {
		t.Fatalf("planning AgentTask target/context=%v %#v", task.IssueID, taskContext)
	}
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='running', started_at=$2 WHERE id=$1`, task.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_run SET status='running', started_at=$2, timeout_seconds=1 WHERE id=$1`, fixture.runID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	repository := orchestration.NewRepository(db.New(pool), pool)
	reconciler := orchestration.NewRunReconciler(repository, gateway, orchestration.RunReconcilerOptions{Now: func() time.Time { return now }})
	processed, err := reconciler.ReconcileBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("reconciled runs=%d, want 1", processed)
	}
	run, err := db.New(pool).GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{RunID: fixture.runID, WorkspaceID: fixture.workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != string(orchestration.RunStatusFailed) || !run.FailureKind.Valid || run.FailureKind.String != "timeout" {
		t.Fatalf("planning timeout run=%#v", run)
	}
	cancelledTask, err := db.New(pool).GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelledTask.Status != "cancelled" {
		t.Fatalf("timed out planning task status=%q", cancelledTask.Status)
	}
}

func TestOwnerCancellationStopsMissionScopedPlanningExecution(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='orchestration_run_scope_purpose_check')`).Scan(&planningReady); err != nil {
		t.Fatal(err)
	}
	if !planningReady {
		t.Skip("planning scope migrations are not applied")
	}

	fixture := createExecutionGatewayFixture(t, ctx, pool)
	t.Cleanup(func() { cleanupExecutionGatewayFixture(t, pool, fixture) })
	if _, err := pool.Exec(ctx, `UPDATE mission SET status='draft' WHERE issue_id=$1`, fixture.missionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_assignment SET task_node_id=NULL, role='planner' WHERE id=$1`, fixture.assignmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_run SET task_node_id=NULL, purpose='plan', input='{"schema_version":1,"mission_id":"test"}' WHERE id=$1`, fixture.runID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM task_node WHERE issue_id=$1`, fixture.taskNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, fixture.taskNodeID); err != nil {
		t.Fatal(err)
	}

	queries := db.New(pool)
	gateway := NewTaskExecutionGateway(NewTaskService(queries, pool, nil, events.New()))
	enqueued, err := gateway.Enqueue(ctx, orchestration.EnqueueExecutionRequest{
		WorkspaceID: fixture.workspaceID, RunID: fixture.runID, ActorID: fixture.userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='running', started_at=now() WHERE id=$1`, enqueued.AgentTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_run SET status='running', started_at=now() WHERE id=$1`, fixture.runID); err != nil {
		t.Fatal(err)
	}

	repository := orchestration.NewRepository(queries, pool)
	service := orchestration.NewService(queries, repository, gateway, orchestration.DefaultPlanHardLimits())
	command := orchestration.CancelMissionCommand{
		WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID,
		CommandID: orchestrationTestUUID(), ActorID: fixture.userID, ExpectedRevision: 1,
		Reason: "owner cancelled planning",
	}
	cancelled, err := service.CancelMission(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Mission.Status != string(orchestration.MissionStatusCancelled) || len(cancelled.ActiveRuns) != 1 {
		t.Fatalf("cancelled planning mission=%#v", cancelled)
	}
	task, err := queries.GetAgentTask(ctx, enqueued.AgentTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "cancelled" {
		t.Fatalf("planning task status=%q, want cancelled", task.Status)
	}

	reconciler := orchestration.NewRunReconciler(repository, gateway, orchestration.RunReconcilerOptions{})
	if processed, err := reconciler.ReconcileBatch(ctx); err != nil || processed != 1 {
		t.Fatalf("reconcile cancelled planning run: processed=%d err=%v", processed, err)
	}
	run, err := queries.GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{
		RunID: fixture.runID, WorkspaceID: fixture.workspaceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != string(orchestration.RunStatusCancelled) {
		t.Fatalf("planning run status=%q, want cancelled", run.Status)
	}
	replayed, err := service.CancelMission(ctx, command)
	if err != nil || !replayed.Idempotent {
		t.Fatalf("cancel replay=%#v err=%v", replayed, err)
	}
}

func TestPlanningCompletionPersistsOnlyValidProposalArtifact(t *testing.T) {
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
	limits := orchestration.PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1}
	input := orchestration.PlanProposalInput{Objective: "Deliver the mission", DeliveryCriteria: []string{"A reviewable plan exists"}}

	for _, test := range []struct {
		name   string
		valid  bool
		action string
	}{
		{name: "valid proposal is immutable artifact", valid: true, action: "approve"},
		{name: "tampered proposal hash cannot materialize DAG", valid: true, action: "tamper"},
		{name: "owner edit creates a new immutable version", valid: true, action: "edit"},
		{name: "owner edit cannot change frozen planning input", valid: true, action: "edit_frozen"},
		{name: "owner reject revokes planner and permits replan", valid: true, action: "reject"},
		{name: "invalid proposal fails run without artifact", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := createExecutionGatewayFixture(t, ctx, pool)
			t.Cleanup(func() { cleanupExecutionGatewayFixture(t, pool, fixture) })
			spec, err := orchestration.EncodePlanningRunSpec(orchestration.PlanningRunSpec{
				SchemaVersion: orchestration.PlanningRunSpecSchemaVersion, MissionID: uuidTextForTest(fixture.missionID),
				ProposalArtifactKind: orchestration.ArtifactKindPlanProposal, ProposalSchemaVersion: orchestration.PlanProposalSchemaVersion,
				Input: input, Limits: limits,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE mission SET status='draft', limits=$2 WHERE issue_id=$1`, fixture.missionID, mustJSON(t, limits)); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE orchestration_assignment SET task_node_id=NULL, role='planner' WHERE id=$1`, fixture.assignmentID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE orchestration_run SET task_node_id=NULL, purpose='plan', input=$2 WHERE id=$1`, fixture.runID, spec); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `DELETE FROM task_node WHERE issue_id=$1`, fixture.taskNodeID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, fixture.taskNodeID); err != nil {
				t.Fatal(err)
			}
			gateway := NewTaskExecutionGateway(NewTaskService(db.New(pool), pool, nil, events.New()))
			enqueued, err := gateway.Enqueue(ctx, orchestration.EnqueueExecutionRequest{WorkspaceID: fixture.workspaceID, RunID: fixture.runID, ActorID: fixture.userID})
			if err != nil {
				t.Fatal(err)
			}
			output := `{"schema_version":1}`
			if test.valid {
				proposal, encodeErr := orchestration.EncodePlanProposal(orchestration.PlanProposal{
					SchemaVersion: orchestration.PlanProposalSchemaVersion, MissionID: uuidTextForTest(fixture.missionID), ProposalKey: "proposal-1",
					Input: input, Limits: limits,
					Nodes: []orchestration.PlanProposalNode{
						{
							Key: "execute", Title: "Execute", Description: "Produce the result", Duty: orchestration.DutyExecutor,
							AcceptanceCriteria: []string{"Result is verifiable"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit},
						},
						{
							Key: "integrate", Title: "Integrate", Description: "Deliver the result", Duty: orchestration.DutyIntegrator,
							AcceptanceCriteria: []string{"Delivery is verifiable"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindFinalDelivery},
							DependsOn: []string{"execute"},
						},
					},
				})
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				output = string(proposal)
			}
			completedAt := time.Now().UTC()
			if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed', completed_at=$2, result=$3 WHERE id=$1`, enqueued.AgentTaskID, completedAt, mustJSON(t, map[string]any{"output": output})); err != nil {
				t.Fatal(err)
			}
			result, err := orchestration.NewRepository(db.New(pool), pool).ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: fixture.workspaceID, RunID: fixture.runID, ObservedAt: completedAt})
			if err != nil {
				t.Fatal(err)
			}
			var artifactCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM artifact WHERE run_id=$1 AND kind='plan_proposal'`, fixture.runID).Scan(&artifactCount); err != nil {
				t.Fatal(err)
			}
			if test.valid {
				if result.Run.Status != string(orchestration.RunStatusSucceeded) || result.PlanProposalArtifact == nil || artifactCount != 1 {
					t.Fatalf("valid proposal result=%#v artifacts=%d", result, artifactCount)
				}
				decoded, decodeErrs := orchestration.DecodeAndValidatePlanProposal(result.PlanProposalArtifact.Metadata, uuidTextForTest(fixture.missionID), limits)
				if len(decodeErrs) > 0 || decoded.ProposalKey != "proposal-1" || !result.PlanProposalArtifact.ContentHash.Valid {
					t.Fatalf("stored proposal=%#v errors=%v artifact=%#v", decoded, decodeErrs, result.PlanProposalArtifact)
				}
				exerciseOwnerPlanGate(t, ctx, pool, fixture, gateway, decoded, *result.PlanProposalArtifact, test.action)
			} else if result.Run.Status != string(orchestration.RunStatusFailed) || !result.Run.FailureKind.Valid || result.Run.FailureKind.String != "invalid_plan_proposal" || result.PlanProposalArtifact != nil || artifactCount != 0 {
				t.Fatalf("invalid proposal result=%#v artifacts=%d", result, artifactCount)
			}
		})
	}
}

func exerciseOwnerPlanGate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture executionGatewayFixture, gateway *TaskExecutionGateway, proposal orchestration.PlanProposal, artifact db.Artifact, action string) {
	t.Helper()
	orchestrator := orchestration.NewService(db.New(pool), orchestration.NewRepository(db.New(pool), pool), gateway, orchestration.DefaultPlanHardLimits())
	switch action {
	case "tamper":
		if _, err := pool.Exec(ctx, `UPDATE artifact SET content_hash='sha256:tampered' WHERE id=$1`, artifact.ID); err != nil {
			t.Fatal(err)
		}
		_, err := orchestrator.ApprovePlanProposal(ctx, orchestration.SubmitPlanProposalCommand{WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID, ProposalArtifactID: artifact.ID, CommandID: orchestrationTestUUID(), ActorID: fixture.userID, ExpectedRevision: 1})
		if err == nil {
			t.Fatal("tampered proposal unexpectedly materialized a plan")
		}
		assertDraftPlanGateRows(t, ctx, pool, fixture, 0)
	case "edit":
		proposal.ProposalKey = "proposal-owner-edit"
		proposal.Nodes[0].Title = "Owner edited execution"
		command := orchestration.EditPlanProposalCommand{WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID, ProposalArtifactID: artifact.ID, CommandID: orchestrationTestUUID(), ActorID: fixture.userID, ExpectedRevision: 1, Proposal: proposal}
		edited, err := orchestrator.EditPlanProposal(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		if edited.Mission.Revision != 2 || edited.Artifact.Version != artifact.Version+1 || edited.Artifact.ID == artifact.ID {
			t.Fatalf("edited proposal=%#v", edited)
		}
		replayed, err := orchestrator.EditPlanProposal(ctx, command)
		if err != nil || !replayed.Idempotent || replayed.Artifact.ID != edited.Artifact.ID {
			t.Fatalf("edit replay=%#v err=%v", replayed, err)
		}
		projection, err := orchestrator.GetMissionProjection(ctx, fixture.workspaceID, fixture.missionID)
		if err != nil || len(projection.Planning.Proposals) != 2 || projection.Planning.Proposals[0].Decision != "superseded" || projection.Planning.Proposals[1].Decision != "pending" {
			t.Fatalf("edited projection=%#v err=%v", projection.Planning, err)
		}
		submitted, err := orchestrator.ApprovePlanProposal(ctx, orchestration.SubmitPlanProposalCommand{WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID, ProposalArtifactID: edited.Artifact.ID, CommandID: orchestrationTestUUID(), ActorID: fixture.userID, ExpectedRevision: 2})
		if err != nil || submitted.Mission.Status != string(orchestration.MissionStatusReady) || len(submitted.TaskNodes) != 2 {
			t.Fatalf("approve edited=%#v err=%v", submitted, err)
		}
	case "edit_frozen":
		proposal.Input.Objective = "A different mission objective"
		_, err := orchestrator.EditPlanProposal(ctx, orchestration.EditPlanProposalCommand{
			WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID, ProposalArtifactID: artifact.ID,
			CommandID: orchestrationTestUUID(), ActorID: fixture.userID, ExpectedRevision: 1, Proposal: proposal,
		})
		var validationErr orchestration.CommandValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("frozen input edit error=%v, want CommandValidationError", err)
		}
		var artifactCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM artifact WHERE mission_id=$1 AND kind='plan_proposal'`, fixture.missionID).Scan(&artifactCount); err != nil {
			t.Fatal(err)
		}
		if artifactCount != 1 {
			t.Fatalf("frozen input edit created %d proposal artifacts, want 1", artifactCount)
		}
		assertDraftPlanGateRows(t, ctx, pool, fixture, 0)
	case "reject":
		command := orchestration.RejectPlanProposalCommand{WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID, ProposalArtifactID: artifact.ID, CommandID: orchestrationTestUUID(), ActorID: fixture.userID, ExpectedRevision: 1, Reason: "Owner requests a different decomposition"}
		rejected, err := orchestrator.RejectPlanProposal(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		if rejected.Mission.Revision != 2 || rejected.Assignment.Status != string(orchestration.AssignmentStatusRevoked) {
			t.Fatalf("rejected proposal=%#v", rejected)
		}
		replayed, err := orchestrator.RejectPlanProposal(ctx, command)
		if err != nil || !replayed.Idempotent {
			t.Fatalf("reject replay=%#v err=%v", replayed, err)
		}
		projection, err := orchestrator.GetMissionProjection(ctx, fixture.workspaceID, fixture.missionID)
		if err != nil || len(projection.Planning.Proposals) != 1 || projection.Planning.Proposals[0].Decision != "rejected" || projection.Planning.Proposals[0].DecisionReason == "" {
			t.Fatalf("rejected projection=%#v err=%v", projection.Planning, err)
		}
		plannerBindings := seedServiceRolePolicyBindings(t, ctx, orchestration.NewRepository(db.New(pool), pool), fixture.workspaceID, fixture.userID, orchestration.DutyPlanner)
		replanned, err := orchestrator.RequestPlan(ctx, orchestration.RequestPlanCommand{WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID, CommandID: orchestrationTestUUID(), ActorID: fixture.userID, ExpectedRevision: 2, RolePolicyBinding: plannerBindings[0], Input: proposal.Input})
		if err != nil || replanned.Mission.Revision != 3 || replanned.Assignment.Sequence != 2 || replanned.Assignment.ID == fixture.assignmentID {
			t.Fatalf("replanned=%#v err=%v", replanned, err)
		}
	case "approve":
		command := orchestration.SubmitPlanProposalCommand{WorkspaceID: fixture.workspaceID, MissionID: fixture.missionID, ProposalArtifactID: artifact.ID, CommandID: orchestrationTestUUID(), ActorID: fixture.userID, ExpectedRevision: 1}
		submitted, err := orchestrator.ApprovePlanProposal(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		if submitted.Mission.Status != string(orchestration.MissionStatusReady) || len(submitted.TaskNodes) != 2 || len(submitted.Dependencies) != 1 {
			t.Fatalf("submitted proposal=%#v", submitted)
		}
		var payload struct {
			SourceArtifactID string `json:"source_artifact_id"`
			PlanSource       string `json:"plan_source"`
		}
		if err := json.Unmarshal(submitted.Activity.Payload, &payload); err != nil || payload.SourceArtifactID != uuidTextForTest(artifact.ID) || payload.PlanSource != string(orchestration.PlanSourceProposal) {
			t.Fatalf("plan accepted lineage=%#v err=%v", payload, err)
		}
		replayed, err := orchestrator.ApprovePlanProposal(ctx, command)
		if err != nil || !replayed.Idempotent {
			t.Fatalf("approve replay=%#v err=%v", replayed, err)
		}
		projection, err := orchestrator.GetMissionProjection(ctx, fixture.workspaceID, fixture.missionID)
		if err != nil || projection.Planning.Source != orchestration.PlanSourceProposal {
			t.Fatalf("approved projection source=%q err=%v", projection.Planning.Source, err)
		}
	default:
		t.Fatalf("unknown plan gate action %q", action)
	}
}

func assertDraftPlanGateRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture executionGatewayFixture, wantNodes int) {
	t.Helper()
	var nodes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM task_node WHERE mission_id=$1`, fixture.missionID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	mission, err := db.New(pool).GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{IssueID: fixture.missionID, WorkspaceID: fixture.workspaceID})
	if err != nil || mission.Status != string(orchestration.MissionStatusDraft) || nodes != wantNodes {
		t.Fatalf("plan gate leaked state: mission=%#v nodes=%d err=%v", mission, nodes, err)
	}
}

type executionGatewayFixture struct {
	userID       pgtype.UUID
	workspaceID  pgtype.UUID
	runtimeID    pgtype.UUID
	agentID      pgtype.UUID
	missionID    pgtype.UUID
	taskNodeID   pgtype.UUID
	assignmentID pgtype.UUID
	runID        pgtype.UUID
}

func createExecutionGatewayFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) executionGatewayFixture {
	t.Helper()
	suffix := uuid.NewString()
	var fixture executionGatewayFixture
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Execution gateway test', $1) RETURNING id`, "execution-gateway-"+suffix+"@liexiu.test").Scan(&fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix, issue_counter) VALUES ('Execution gateway test', $1, '', 'EGW', 2) RETURNING id`, "execution-gateway-"+suffix).Scan(&fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, name, runtime_mode, provider, status, device_info,
			metadata, last_seen_at, visibility, owner_id
		) VALUES ($1, 'Execution gateway runtime', 'cloud', 'test', 'online', 'test', '{}', now(), 'private', $2)
		RETURNING id
	`, fixture.workspaceID, fixture.userID).Scan(&fixture.runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		) VALUES ($1, 'Execution gateway agent', '', 'cloud', '{}', $2, 'private', 1, $3)
		RETURNING id
	`, fixture.workspaceID, fixture.runtimeID, fixture.userID).Scan(&fixture.agentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, position, number)
		VALUES ($1, 'Execution gateway mission', 'in_progress', 'none', 'member', $2, 0, 1)
		RETURNING id
	`, fixture.workspaceID, fixture.userID).Scan(&fixture.missionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, description, status, priority, creator_type, creator_id, parent_issue_id, position, number)
		VALUES ($1, 'Execution gateway task', 'Execute the assigned run', 'todo', 'none', 'member', $2, $3, 0, 2)
		RETURNING id
	`, fixture.workspaceID, fixture.userID, fixture.missionID).Scan(&fixture.taskNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mission (issue_id, workspace_id, status, created_by) VALUES ($1, $2, 'running', $3)`, fixture.missionID, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_node (
			issue_id, workspace_id, mission_id, node_key, role,
			acceptance_criteria, artifact_kinds, priority, status
		) VALUES ($1, $2, $3, 'A', 'executor', '["artifact exists"]', '["commit"]', 10, 'assigned')
	`, fixture.taskNodeID, fixture.workspaceID, fixture.missionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orchestration_assignment (
			workspace_id, mission_id, task_node_id, role, agent_id,
			runtime_id, status, sequence, created_by
		) VALUES ($1, $2, $3, 'executor', $4, $5, 'active', 1, $6)
		RETURNING id
	`, fixture.workspaceID, fixture.missionID, fixture.taskNodeID, fixture.agentID, fixture.runtimeID, fixture.userID).Scan(&fixture.assignmentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orchestration_run (
			workspace_id, mission_id, task_node_id, assignment_id, purpose,
			attempt, status, input, dispatch_deadline_at, timeout_seconds
		) VALUES ($1, $2, $3, $4, 'execute', 1, 'queued', '{}', now() + interval '5 minutes', 300)
		RETURNING id
	`, fixture.workspaceID, fixture.missionID, fixture.taskNodeID, fixture.assignmentID).Scan(&fixture.runID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func cleanupExecutionGatewayFixture(t *testing.T, pool *pgxpool.Pool, fixture executionGatewayFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM agent_task_queue WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id=$1)`, fixture.workspaceID); err != nil {
		t.Errorf("cleanup agent task: %v", err)
	}
	for _, statement := range []string{
		`DELETE FROM mission_role_policy_snapshot WHERE workspace_id = $1`,
		`DELETE FROM role_profile WHERE workspace_id = $1`,
		`DELETE FROM artifact WHERE workspace_id = $1`,
		`DELETE FROM orchestration_run WHERE workspace_id = $1`,
		`DELETE FROM orchestration_assignment WHERE workspace_id = $1`,
		`DELETE FROM orchestration_activity WHERE workspace_id = $1`,
		`DELETE FROM task_node WHERE workspace_id = $1`,
		`DELETE FROM mission WHERE workspace_id = $1`,
		`DELETE FROM workspace WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, statement, fixture.workspaceID); err != nil {
			t.Errorf("cleanup %q: %v", statement, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, fixture.userID); err != nil {
		t.Errorf("cleanup user: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func uuidTextForTest(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}

func orchestrationTestUUID() pgtype.UUID {
	value := uuid.New()
	return pgtype.UUID{Bytes: value, Valid: true}
}
