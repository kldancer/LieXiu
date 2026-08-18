package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// TestHumanGateIntegration proves the reviewer separation contract against
// PostgreSQL.  The first pass has only the producer, so the task is blocked
// with a pending Human Gate; resolving it without adding a reviewer creates a
// fresh gate.  Adding a second Agent then lets the owner resolve/retry and
// creates a reviewer Run on the independent Agent.
func TestHumanGateIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var ready bool
	if err := p.QueryRow(ctx, `SELECT to_regclass('orchestration_human_gate') IS NOT NULL`).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Skip("Human Gate migration 349 is not applied")
	}
	f := newRoutingIntegrationFixture(t, ctx, p)
	q := db.New(p)
	r := NewRepository(q, p)
	s := NewService(q, r, &recordingPlanGateway{}, DefaultPlanHardLimits())

	bindings := seedRolePolicyBindings(t, ctx, r, f.workspaceID, f.ownerID, DutyExecutor, DutyReviewer, DutyIntegrator)
	for i := range bindings {
		if bindings[i].Duty != DutyReviewer {
			bindings[i].AgentID = f.agentID
		}
	}
	created, err := s.QuickCreateMission(ctx, QuickCreateMissionCommand{WorkspaceID: f.workspaceID, CommandID: newTestUUID(), ActorID: f.ownerID, Prompt: "human gate routing"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartMission(ctx, StartMissionCommand{WorkspaceID: f.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: f.ownerID, ExpectedRevision: 2, RolePolicyBindings: bindings}); err != nil {
		t.Fatal(err)
	}
	advanced, err := r.AdvanceMission(ctx, AdvanceMissionParams{WorkspaceID: f.workspaceID, MissionID: created.MissionID, CorrelationID: newTestUUID(), ObservedAt: time.Now().UTC(), DispatchWindow: time.Minute, RunTimeout: time.Minute})
	if err != nil || len(advanced.CreatedRuns) != 1 {
		t.Fatalf("initial advance runs=%v err=%v", advanced.CreatedRuns, err)
	}
	workRun := advanced.CreatedRuns[0]
	var node db.TaskNode
	if err := p.QueryRow(ctx, `SELECT issue_id,workspace_id,mission_id,node_key,role,acceptance_criteria,artifact_kinds,priority,status,block_reason,rework_count,revision,created_at,updated_at,budget_estimate_tokens,budget_estimate_cost_usd_ticks FROM task_node WHERE mission_id=$1 AND role='executor' LIMIT 1`, created.MissionID).Scan(&node.IssueID, &node.WorkspaceID, &node.MissionID, &node.NodeKey, &node.Role, &node.AcceptanceCriteria, &node.ArtifactKinds, &node.Priority, &node.Status, &node.BlockReason, &node.ReworkCount, &node.Revision, &node.CreatedAt, &node.UpdatedAt, &node.BudgetEstimateTokens, &node.BudgetEstimateCostUsdTicks); err != nil {
		t.Fatal(err)
	}
	_, blocked := completeHumanGateArtifactTask(t, ctx, p, r, s, f.workspaceID, workRun, ArtifactKindFile, "repo://human-gate")
	gate := pendingGate(t, ctx, p, created.MissionID)
	if gate.Kind != string(HumanGateReviewerUnavailable) || blocked.Mission.Status != string(MissionStatusBlocked) {
		t.Fatalf("gate=%#v mission=%s", gate, blocked.Mission.Status)
	}
	projection, err := s.GetMissionProjection(ctx, f.workspaceID, created.MissionID)
	if err != nil || len(projection.HumanGates) != 1 || projection.HumanGates[0].Status != "pending" {
		t.Fatalf("projection gates=%#v err=%v", projection.HumanGates, err)
	}
	for _, activity := range blocked.Activities {
		if activity.Type != activityTaskBlocked && activity.Type != activityHumanGateRequired {
			continue
		}
		var gatePayload map[string]any
		if err := json.Unmarshal(activity.Payload, &gatePayload); err == nil && strings.Contains(string(activity.Payload), uuidText(f.agentID)) {
			t.Fatalf("human gate evidence leaked producer id: %v", gatePayload)
		}
	}

	_, err = s.RetryTaskNode(ctx, RetryTaskNodeCommand{WorkspaceID: f.workspaceID, MissionID: created.MissionID, TaskNodeID: node.IssueID, CommandID: newTestUUID(), CorrelationID: newTestUUID(), ActorID: f.ownerID, ExpectedRevision: blocked.Mission.Revision, ExpectedTaskRevision: gateTaskRevision(t, ctx, p, node.IssueID), Reason: "must use gate"})
	if !errors.Is(err, ErrHumanGateResolutionRequired) {
		t.Fatalf("generic RetryTaskNode err=%v, want pending Human Gate rejection", err)
	}

	resolveCommandID := newTestUUID()
	resolveCommand := ResolveHumanGateCommand{WorkspaceID: f.workspaceID, MissionID: created.MissionID, GateID: gate.ID, CommandID: resolveCommandID, CorrelationID: newTestUUID(), ActorID: f.ownerID, ExpectedRevision: blocked.Mission.Revision, ExpectedTaskRevision: gateTaskRevision(t, ctx, p, node.IssueID), ExpectedGateRevision: gate.Revision, Resolution: HumanGateResolutionRetry, Reason: "owner retry"}
	resolved, err := s.ResolveHumanGate(ctx, resolveCommand)
	if err != nil {
		t.Fatal(err)
	}
	secondGate := pendingGate(t, ctx, p, created.MissionID)
	if secondGate.ID == gate.ID || resolved.Gate.Status != "resolved" {
		t.Fatalf("resolve did not create a fresh pending gate: old=%v new=%v", gate.ID, secondGate.ID)
	}
	replayed, err := s.ResolveHumanGate(ctx, resolveCommand)
	if err != nil || !replayed.Idempotent || replayed.Gate.ID != gate.ID {
		t.Fatalf("resolve replay=%#v err=%v", replayed, err)
	}

	independentRuntime, independentAgent := addIndependentAgent(t, ctx, p, f)
	resolvedAgain, err := s.ResolveHumanGate(ctx, ResolveHumanGateCommand{WorkspaceID: f.workspaceID, MissionID: created.MissionID, GateID: secondGate.ID, CommandID: newTestUUID(), CorrelationID: newTestUUID(), ActorID: f.ownerID, ExpectedRevision: resolved.Advance.Mission.Revision, ExpectedTaskRevision: gateTaskRevision(t, ctx, p, node.IssueID), ExpectedGateRevision: secondGate.Revision, Resolution: HumanGateResolutionRetry, Reason: "independent reviewer online"})
	if err != nil {
		t.Fatal(err)
	}
	var reviewerAgent, reviewerRuntime pgtype.UUID
	if err := p.QueryRow(ctx, `SELECT agent_id,runtime_id FROM orchestration_assignment WHERE mission_id=$1 AND role='reviewer' ORDER BY sequence DESC LIMIT 1`, created.MissionID).Scan(&reviewerAgent, &reviewerRuntime); err != nil {
		t.Fatal(err)
	}
	if reviewerAgent != independentAgent || reviewerRuntime != independentRuntime || reviewerAgent == f.agentID {
		t.Fatalf("reviewer assignment=%v/%v, want independent %v/%v", reviewerAgent, reviewerRuntime, independentAgent, independentRuntime)
	}
	if len(resolvedAgain.Advance.CreatedRuns) == 0 || resolvedAgain.Advance.CreatedRuns[len(resolvedAgain.Advance.CreatedRuns)-1].Purpose != "review" {
		t.Fatalf("resolve/retry did not create reviewer run: %#v", resolvedAgain.Advance.CreatedRuns)
	}
}

func TestRoutingRejectsPrivateRuntimeOwnedByAnotherUserIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var ready bool
	if err := p.QueryRow(ctx, `SELECT to_regclass('mission_role_policy_snapshot') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		t.Skip("Wave 4B migrations are not applied")
	}
	var foreignOwner pgtype.UUID
	if err := p.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('foreign runtime owner',$1) RETURNING id`, "foreign-runtime-"+uuid.NewString()+"@liexiu.test").Scan(&foreignOwner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := p.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, foreignOwner); cleanupErr != nil {
			t.Errorf("cleanup foreign runtime owner: %v", cleanupErr)
		}
	})
	f := newRoutingIntegrationFixture(t, ctx, p)
	var foreignRuntime, foreignAgent pgtype.UUID
	if err := p.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,device_info,metadata,last_seen_at,visibility,owner_id) VALUES ($1,'foreign private runtime','cloud','test','online','test','{}',now(),'private',$2) RETURNING id`, f.workspaceID, foreignOwner).Scan(&foreignRuntime); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,description,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id,permission_mode) VALUES ($1,'foreign shared agent','','cloud','{}',$2,'private',1,$3,'public_to') RETURNING id`, f.workspaceID, foreignRuntime, foreignOwner).Scan(&foreignAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO agent_invocation_target (agent_id,target_type,target_id,created_by) VALUES ($1,'workspace',$2,$3)`, foreignAgent, f.workspaceID, foreignOwner); err != nil {
		t.Fatal(err)
	}
	q := db.New(p)
	r := NewRepository(q, p)
	s := NewService(q, r, &recordingPlanGateway{}, DefaultPlanHardLimits())
	bindings := seedRolePolicyBindings(t, ctx, r, f.workspaceID, f.ownerID, DutyExecutor, DutyReviewer, DutyIntegrator)
	for i := range bindings {
		bindings[i].AgentID = f.agentID
		if bindings[i].Duty == DutyExecutor {
			bindings[i].AgentID = foreignAgent
		}
	}
	created, err := s.QuickCreateMission(ctx, QuickCreateMissionCommand{WorkspaceID: f.workspaceID, CommandID: newTestUUID(), ActorID: f.ownerID, Prompt: "private runtime must be rejected"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartMission(ctx, StartMissionCommand{WorkspaceID: f.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: f.ownerID, ExpectedRevision: 2, RolePolicyBindings: bindings}); err != nil {
		t.Fatal(err)
	}
	advanced, err := r.AdvanceMission(ctx, AdvanceMissionParams{WorkspaceID: f.workspaceID, MissionID: created.MissionID, CorrelationID: newTestUUID(), ObservedAt: time.Now().UTC(), DispatchWindow: time.Minute, RunTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(advanced.CreatedRuns) != 0 {
		t.Fatalf("private foreign Runtime created runs: %#v", advanced.CreatedRuns)
	}
	foundDenied := false
	for _, activity := range advanced.Activities {
		if activity.Type == activityTaskBlocked && strings.Contains(string(activity.Payload), RoutingReasonRuntimePermissionDenied) {
			foundDenied = true
			if strings.Contains(string(activity.Payload), uuidText(foreignRuntime)) || strings.Contains(string(activity.Payload), uuidText(foreignAgent)) {
				t.Fatalf("routing evidence leaked rejected IDs: %s", activity.Payload)
			}
		}
	}
	if !foundDenied {
		t.Fatalf("AdvanceMission activities=%#v, want %s", advanced.Activities, RoutingReasonRuntimePermissionDenied)
	}
}

func TestReviewReworkLimitCreatesHumanGateIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var ready bool
	if err := p.QueryRow(ctx, `SELECT to_regclass('orchestration_human_gate') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		t.Skip("Human Gate migration 349 is not applied")
	}
	f := newRoutingIntegrationFixture(t, ctx, p)
	_, reviewerAgent := addIndependentAgent(t, ctx, p, f)
	q := db.New(p)
	r := NewRepository(q, p)
	s := NewService(q, r, &recordingPlanGateway{}, DefaultPlanHardLimits())
	bindings := seedRolePolicyBindings(t, ctx, r, f.workspaceID, f.ownerID, DutyExecutor, DutyReviewer, DutyIntegrator)
	for i := range bindings {
		if bindings[i].Duty == DutyExecutor || bindings[i].Duty == DutyIntegrator {
			bindings[i].AgentID = f.agentID
		}
	}
	created, err := s.QuickCreateMission(ctx, QuickCreateMissionCommand{WorkspaceID: f.workspaceID, CommandID: newTestUUID(), ActorID: f.ownerID, Prompt: "rework limit gate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartMission(ctx, StartMissionCommand{WorkspaceID: f.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: f.ownerID, ExpectedRevision: 2, RolePolicyBindings: bindings}); err != nil {
		t.Fatal(err)
	}
	advanced, err := r.AdvanceMission(ctx, AdvanceMissionParams{WorkspaceID: f.workspaceID, MissionID: created.MissionID, CorrelationID: newTestUUID(), ObservedAt: time.Now().UTC(), DispatchWindow: time.Minute, RunTimeout: time.Minute})
	if err != nil || len(advanced.CreatedRuns) != 1 {
		t.Fatalf("create work run: runs=%#v err=%v", advanced.CreatedRuns, err)
	}
	workRun := advanced.CreatedRuns[0]
	reconciledWork, workAdvance := completeHumanGateArtifactTask(t, ctx, p, r, s, f.workspaceID, workRun, ArtifactKindFile, "repo://rework-limit")
	if len(workAdvance.CreatedRuns) != 1 {
		t.Fatalf("record artifact/reviewer advance: runs=%#v", workAdvance.CreatedRuns)
	}
	reviewRun := workAdvance.CreatedRuns[0]
	if reviewRun.Purpose != "review" {
		t.Fatalf("run purpose=%q, want review", reviewRun.Purpose)
	}
	var assignedReviewer pgtype.UUID
	if err := p.QueryRow(ctx, `SELECT agent_id FROM orchestration_assignment WHERE id=$1`, reviewRun.AssignmentID).Scan(&assignedReviewer); err != nil {
		t.Fatal(err)
	}
	if assignedReviewer != reviewerAgent {
		t.Fatalf("reviewer=%v, want independent %v", assignedReviewer, reviewerAgent)
	}
	if _, err := p.Exec(ctx, `UPDATE task_node SET rework_count=1 WHERE issue_id=$1`, workRun.TaskNodeID); err != nil {
		t.Fatal(err)
	}
	verdict, _ := completeHumanGateReviewTask(t, ctx, p, r, s, f.workspaceID, reviewRun, ReviewDecisionChangesRequested, []string{"address the remaining gap"})
	if TaskStatus(verdict.TaskNode.Status) != TaskStatusBlocked {
		t.Fatalf("task status=%s, want blocked", verdict.TaskNode.Status)
	}
	artifactID := reconciledWork.Artifact.ID
	gate := pendingGate(t, ctx, p, created.MissionID)
	if HumanGateKind(gate.Kind) != HumanGateReworkLimitExceeded || gate.ArtifactID != artifactID || gate.SourceRunID != reviewRun.ID {
		t.Fatalf("rework gate=%#v", gate)
	}
	projection, err := s.GetMissionProjection(ctx, f.workspaceID, created.MissionID)
	if err != nil || len(projection.HumanGates) != 1 || projection.HumanGates[0].Kind != HumanGateReworkLimitExceeded {
		t.Fatalf("projection gates=%#v err=%v", projection.HumanGates, err)
	}
}

func completeHumanGateArtifactTask(t *testing.T, ctx context.Context, p *pgxpool.Pool, r *Repository, s *Service, workspaceID pgtype.UUID, run db.OrchestrationRun, kind ArtifactKind, uri string) (ReconcileRunResult, AdvanceMissionResult) {
	t.Helper()
	ensureHumanGateAgentTask(t, ctx, p, run.ID)
	receipt, err := json.Marshal(map[string]any{"schema_version": 1, "artifact": map[string]any{"kind": kind, "uri": uri, "content_hash": "", "summary": "human gate integration", "metadata": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{"output": string(receipt)})
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	if _, err := p.Exec(ctx, `UPDATE agent_task_queue SET status='completed', started_at=COALESCE(started_at,$2::timestamptz-interval '1 second'), completed_at=$2, result=$3 WHERE orchestration_run_id=$1`, run.ID, completedAt, envelope); err != nil {
		t.Fatal(err)
	}
	reconciled, err := r.ReconcileRun(ctx, ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: completedAt})
	if err != nil || reconciled.Artifact == nil {
		t.Fatalf("reconcile artifact: run=%#v task=%#v artifact=%#v err=%v", reconciled.Run, reconciled.TaskNode, reconciled.Artifact, err)
	}
	advanced, err := s.AdvanceMission(ctx, AdvanceMissionCommand{WorkspaceID: workspaceID, MissionID: run.MissionID, CorrelationID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	return reconciled, advanced
}

func completeHumanGateReviewTask(t *testing.T, ctx context.Context, p *pgxpool.Pool, r *Repository, s *Service, workspaceID pgtype.UUID, run db.OrchestrationRun, decision ReviewDecision, changes []string) (ReconcileRunResult, AdvanceMissionResult) {
	t.Helper()
	ensureHumanGateAgentTask(t, ctx, p, run.ID)
	receipt, err := json.Marshal(map[string]any{"schema_version": 1, "decision": decision, "evidence": map[string]any{"human_gate": true}, "requested_changes": changes})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{"output": string(receipt)})
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	if _, err := p.Exec(ctx, `UPDATE agent_task_queue SET status='completed', started_at=COALESCE(started_at,$2::timestamptz-interval '1 second'), completed_at=$2, result=$3 WHERE orchestration_run_id=$1`, run.ID, completedAt, envelope); err != nil {
		t.Fatal(err)
	}
	reconciled, err := r.ReconcileRun(ctx, ReconcileRunParams{WorkspaceID: workspaceID, RunID: run.ID, ObservedAt: completedAt})
	if err != nil || reconciled.ReviewVerdict == nil {
		t.Fatalf("reconcile review: result=%#v err=%v", reconciled.ReviewVerdict, err)
	}
	advanced, err := s.AdvanceMission(ctx, AdvanceMissionCommand{WorkspaceID: workspaceID, MissionID: run.MissionID, CorrelationID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	return reconciled, advanced
}

func ensureHumanGateAgentTask(t *testing.T, ctx context.Context, p *pgxpool.Pool, runID pgtype.UUID) {
	t.Helper()
	if _, err := p.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, orchestration_run_id)
		SELECT assignment.agent_id, assignment.runtime_id, run.task_node_id, 'queued', run.id
		FROM orchestration_run run
		JOIN orchestration_assignment assignment ON assignment.id=run.assignment_id
		WHERE run.id=$1
		  AND NOT EXISTS (SELECT 1 FROM agent_task_queue task WHERE task.orchestration_run_id=run.id)
	`, runID); err != nil {
		t.Fatal(err)
	}
}

func pendingGate(t *testing.T, ctx context.Context, p *pgxpool.Pool, missionID pgtype.UUID) db.OrchestrationHumanGate {
	t.Helper()
	var g db.OrchestrationHumanGate
	err := p.QueryRow(ctx, `SELECT id,workspace_id,mission_id,task_node_id,artifact_id,source_run_id,kind,status,reason,context,revision,created_at,resolved_by,resolution,resolution_reason,resolved_at FROM orchestration_human_gate WHERE mission_id=$1 AND status='pending' ORDER BY created_at DESC LIMIT 1`, missionID).Scan(&g.ID, &g.WorkspaceID, &g.MissionID, &g.TaskNodeID, &g.ArtifactID, &g.SourceRunID, &g.Kind, &g.Status, &g.Reason, &g.Context, &g.Revision, &g.CreatedAt, &g.ResolvedBy, &g.Resolution, &g.ResolutionReason, &g.ResolvedAt)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func gateTaskRevision(t *testing.T, ctx context.Context, p *pgxpool.Pool, taskID pgtype.UUID) int64 {
	t.Helper()
	var revision int64
	if err := p.QueryRow(ctx, `SELECT revision FROM task_node WHERE issue_id=$1`, taskID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func addIndependentAgent(t *testing.T, ctx context.Context, p *pgxpool.Pool, f routingIntegrationFixture) (pgtype.UUID, pgtype.UUID) {
	t.Helper()
	var runtimeID, agentID pgtype.UUID
	suffix := uuid.NewString()
	if err := p.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,device_info,metadata,last_seen_at,visibility,owner_id) VALUES ($1,'independent runtime','cloud','test','online','test','{}',now(),'private',$2) RETURNING id`, f.workspaceID, f.ownerID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,description,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id) VALUES ($1,$2,'','cloud','{}',$3,'private',1,$4) RETURNING id`, f.workspaceID, "independent-"+suffix, runtimeID, f.ownerID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	return runtimeID, agentID
}
