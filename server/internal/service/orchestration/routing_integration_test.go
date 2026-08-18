package orchestration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// TestRoutingIntegration exercises the production query path with an isolated
// workspace.  The pure selector owns the exhaustive reason vocabulary; this
// test keeps the database contract focused on frozen snapshots, assignment
// identity, and privacy-safe blocked evidence.
func TestRoutingIntegration(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `SELECT to_regclass('mission_role_policy_snapshot') IS NOT NULL AND to_regclass('agent_runtime') IS NOT NULL`).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Skip("Wave 4B routing migrations are not applied")
	}

	fixture := newRoutingIntegrationFixture(t, ctx, pool)
	queries := db.New(pool)
	repository := NewRepository(queries, pool)
	service := NewService(queries, repository, &recordingPlanGateway{}, DefaultPlanHardLimits())

	t.Run("planner snapshot and activity evidence", func(t *testing.T) {
		binding := fixture.createBinding(t, ctx, repository, DutyPlanner, 37)
		binding.AgentID = fixture.agentID
		created, err := service.CreateMission(ctx, CreateMissionCommand{
			WorkspaceID: fixture.workspaceID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
			Title: "routing planner", Limits: PlanLimits{MaxParallelRuns: 1, MaxTaskAttempts: 1, MaxReworkCycles: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.RequestPlan(ctx, RequestPlanCommand{
			WorkspaceID: fixture.workspaceID, MissionID: created.Mission.IssueID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
			ExpectedRevision: 1, RolePolicyBinding: binding,
			Input: PlanProposalInput{Objective: "routing evidence", DeliveryCriteria: []string{"evidence"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Assignment.AgentID != fixture.agentID || result.Assignment.RuntimeID != fixture.runtimeID {
			t.Fatalf("assignment=%v/%v, want bound agent/runtime %v/%v", result.Assignment.AgentID, result.Assignment.RuntimeID, fixture.agentID, fixture.runtimeID)
		}
		if result.Run.TimeoutSeconds != 37 {
			t.Fatalf("run timeout=%d, want frozen snapshot timeout 37", result.Run.TimeoutSeconds)
		}
		var payload map[string]any
		if err := json.Unmarshal(result.Activity.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		routing, ok := payload["routing"].(map[string]any)
		if !ok {
			t.Fatalf("activity routing payload=%v", payload["routing"])
		}
		if routing["snapshot_hash"] != result.RolePolicySnapshots[0].ContentHash || routing["evidence_hash"] == "" {
			t.Fatalf("routing evidence=%v, snapshot=%s", routing, result.RolePolicySnapshots[0].ContentHash)
		}
		selected, ok := routing["selected"].(map[string]any)
		if !ok || selected["agent_id"] != uuidText(fixture.agentID) || selected["runtime_id"] != uuidText(fixture.runtimeID) {
			t.Fatalf("routing selected=%v", routing["selected"])
		}
		if _, err := repository.CreateRoleProfileVersion(ctx, CreateRoleProfileVersionParams{
			WorkspaceID: fixture.workspaceID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
			ProfileKey: binding.ProfileKey, Duty: DutyPlanner, Name: "changed", Config: testRolePolicyConfig("changed"),
		}); err != nil {
			t.Fatal(err)
		}
		var frozenInstructions string
		if err := pool.QueryRow(ctx, `SELECT config->>'instructions' FROM mission_role_policy_snapshot WHERE mission_id=$1`, created.Mission.IssueID).Scan(&frozenInstructions); err != nil {
			t.Fatal(err)
		}
		if frozenInstructions != "v1" {
			t.Fatalf("frozen snapshot changed after profile update: %q", frozenInstructions)
		}
	})

	t.Run("offline candidates produce blocked privacy-safe activity", func(t *testing.T) {
		bindings := seedRolePolicyBindings(t, ctx, repository, fixture.workspaceID, fixture.ownerID, DutyExecutor, DutyReviewer, DutyIntegrator)
		for i := range bindings {
			bindings[i].AgentID = fixture.agentID
		}
		created, err := service.QuickCreateMission(ctx, QuickCreateMissionCommand{WorkspaceID: fixture.workspaceID, CommandID: newTestUUID(), ActorID: fixture.ownerID, Prompt: "offline routing"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.StartMission(ctx, StartMissionCommand{WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID, ExpectedRevision: 2, RolePolicyBindings: bindings}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE agent_runtime SET status='offline' WHERE id=$1`, fixture.runtimeID); err != nil {
			t.Fatal(err)
		}
		advanced, err := repository.AdvanceMission(ctx, AdvanceMissionParams{WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CorrelationID: newTestUUID(), ObservedAt: time.Now().UTC(), DispatchWindow: time.Minute, RunTimeout: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		var blocked db.OrchestrationActivity
		for _, activity := range advanced.Activities {
			if activity.Type == activityTaskBlocked && strings.Contains(string(activity.Payload), `"routing"`) {
				blocked = activity
				break
			}
		}
		if !blocked.ID.Valid {
			t.Fatalf("AdvanceMission activities=%v, want routing blocked activity", advanced.Activities)
		}
		var payload map[string]any
		if err := json.Unmarshal(blocked.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		routing, ok := payload["routing"].(map[string]any)
		if !ok || routing["selected"] != nil || routing["evidence_hash"] == "" {
			t.Fatalf("blocked routing=%v", payload["routing"])
		}
		if strings.Contains(string(blocked.Payload), uuidText(fixture.agentID)) || strings.Contains(string(blocked.Payload), uuidText(fixture.runtimeID)) {
			t.Fatalf("blocked routing leaked raw rejected IDs: %s", blocked.Payload)
		}
		foundOffline := false
		for _, item := range routing["evaluations"].([]any) {
			for _, reason := range item.(map[string]any)["reason_codes"].([]any) {
				if reason == RoutingReasonRuntimeOffline {
					foundOffline = true
				}
			}
		}
		if !foundOffline {
			t.Fatalf("blocked routing has no runtime_offline reason: %v", routing)
		}
	})
}

type routingIntegrationFixture struct {
	workspaceID, ownerID, runtimeID, agentID pgtype.UUID
}

func newRoutingIntegrationFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) routingIntegrationFixture {
	t.Helper()
	suffix := uuid.NewString()
	var f routingIntegrationFixture
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('routing owner',$1) RETURNING id`, "routing-"+suffix+"@liexiu.test").Scan(&f.ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name,slug,description,issue_prefix) VALUES ('routing workspace',$1,'','RTG') RETURNING id`, "routing-"+suffix).Scan(&f.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, f.workspaceID, f.ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,device_info,metadata,last_seen_at,visibility,owner_id) VALUES ($1,'routing runtime','cloud','test','online','test','{}',now(),'private',$2) RETURNING id`, f.workspaceID, f.ownerID).Scan(&f.runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,description,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id) VALUES ($1,'routing agent','','cloud','{}',$2,'private',1,$3) RETURNING id`, f.workspaceID, f.runtimeID, f.ownerID).Scan(&f.agentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, statement := range []string{`DELETE FROM agent_task_queue WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id=$1)`, `DELETE FROM agent_invocation_target WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id=$1)`, `DELETE FROM orchestration_human_gate WHERE workspace_id=$1`, `DELETE FROM mission_role_policy_snapshot WHERE workspace_id=$1`, `DELETE FROM role_profile WHERE workspace_id=$1`, `DELETE FROM orchestration_run WHERE workspace_id=$1`, `DELETE FROM orchestration_assignment WHERE workspace_id=$1`, `DELETE FROM orchestration_activity WHERE workspace_id=$1`, `DELETE FROM task_node WHERE workspace_id=$1`, `DELETE FROM mission WHERE workspace_id=$1`, `DELETE FROM issue WHERE workspace_id=$1`, `DELETE FROM agent WHERE workspace_id=$1`, `DELETE FROM agent_runtime WHERE workspace_id=$1`, `DELETE FROM member WHERE workspace_id=$1`, `DELETE FROM workspace WHERE id=$1`} {
			if _, err := pool.Exec(context.Background(), statement, f.workspaceID); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
			}
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, f.ownerID); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
	return f
}

func (f routingIntegrationFixture) createBinding(t *testing.T, ctx context.Context, repository *Repository, duty Duty, timeout int) RolePolicyBinding {
	t.Helper()
	created, err := repository.CreateRoleProfileVersion(ctx, CreateRoleProfileVersionParams{WorkspaceID: f.workspaceID, CommandID: newTestUUID(), ActorID: f.ownerID, ProfileKey: "routing-" + duty.String() + "-" + uuid.NewString()[:8], Duty: duty, Name: "routing", Config: func() RoleProfileConfig { c := testRolePolicyConfig("v1"); c.TimeoutSeconds = timeout; return c }()})
	if err != nil {
		t.Fatal(err)
	}
	return RolePolicyBinding{Duty: duty, ProfileKey: created.Profile.ProfileKey, Version: created.Profile.Version}
}
