package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/handler"
	"github.com/kailonyang/liexiu/server/internal/service"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestQuickCreateHTTPCreatesReadyMissionWithoutAgentTask(t *testing.T) {
	legacyResponse := authRequest(t, http.MethodPost, "/api/issues/quick-create", map[string]any{
		"command_id": uuid.NewString(),
		"prompt":     "legacy dispatch fields must be rejected",
		"agent_id":   uuid.NewString(),
	})
	legacyResponse.Body.Close()
	if legacyResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("legacy quick-create field status=%d, want 400", legacyResponse.StatusCode)
	}

	commandID := uuid.NewString()
	requestBody := map[string]any{
		"command_id": commandID,
		"prompt":     "Build the visual multi-agent project board",
	}
	response := authRequest(t, http.MethodPost, "/api/issues/quick-create", requestBody)
	if response.StatusCode != http.StatusCreated {
		response.Body.Close()
		t.Fatalf("quick-create status=%d, want 201", response.StatusCode)
	}
	var created struct {
		MissionID string                      `json:"mission_id"`
		Status    orchestration.MissionStatus `json:"status"`
		Revision  int64                       `json:"revision"`
		Replayed  bool                        `json:"replayed"`
	}
	readJSON(t, response, &created)
	if created.Status != orchestration.MissionStatusReady || created.Revision != 2 || created.Replayed {
		t.Fatalf("unexpected quick-create response: %#v", created)
	}
	missionID := missionTestUUID(t, created.MissionID)
	workspaceID := missionTestUUID(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMissionHTTPFixture(t, missionID, workspaceID) })

	var nodes, runs, tasks int
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM task_node WHERE mission_id=$1 AND workspace_id=$2`, missionID, workspaceID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM orchestration_run WHERE mission_id=$1 AND workspace_id=$2`, missionID, workspaceID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM agent_task_queue
		WHERE issue_id=$1 OR issue_id IN (
			SELECT issue_id FROM task_node WHERE mission_id=$1 AND workspace_id=$2
		)
	`, missionID, workspaceID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if nodes != 2 || runs != 0 || tasks != 0 {
		t.Fatalf("quick-create materialization: nodes=%d runs=%d agent_tasks=%d, want 2/0/0", nodes, runs, tasks)
	}

	response = authRequest(t, http.MethodPost, "/api/issues/quick-create", map[string]any{
		"command_id": commandID,
		"prompt":     "this replay must not create another Mission",
	})
	if response.StatusCode != http.StatusCreated {
		response.Body.Close()
		t.Fatalf("quick-create replay status=%d, want 201", response.StatusCode)
	}
	var replayed struct {
		MissionID string `json:"mission_id"`
		Replayed  bool   `json:"replayed"`
	}
	readJSON(t, response, &replayed)
	if !replayed.Replayed || replayed.MissionID != created.MissionID {
		t.Fatalf("quick-create replay=%#v, want original Mission", replayed)
	}
}

func TestMissionProjectionHTTPRecoversFromActivityCursorGap(t *testing.T) {
	ctx := context.Background()
	workspaceID := missionTestUUID(t, testWorkspaceID)
	userID := missionTestUUID(t, testUserID)
	queries := db.New(testPool)
	repository := orchestration.NewRepository(queries, testPool)
	execution := service.NewTaskExecutionGateway(service.NewTaskService(queries, testPool, nil, events.New()))
	orchestrator := orchestration.NewService(queries, repository, execution, orchestration.DefaultPlanHardLimits())
	startBindings := seedMissionRolePolicyBindings(t, ctx, repository, workspaceID, userID, orchestration.DutyExecutor, orchestration.DutyReviewer, orchestration.DutyIntegrator)
	limits := orchestration.PlanLimits{MaxParallelRuns: 1, MaxTaskAttempts: 2, MaxReworkCycles: 1}

	created, err := orchestrator.CreateMission(ctx, orchestration.CreateMissionCommand{
		WorkspaceID: workspaceID, CommandID: missionNewUUID(), ActorID: userID,
		Title: "Projection HTTP integration", Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	missionID := created.Mission.IssueID
	t.Cleanup(func() { cleanupMissionHTTPFixture(t, missionID, workspaceID) })
	plan := orchestration.Plan{
		SchemaVersion: orchestration.PlanSchemaVersion, MissionID: missionUUIDText(missionID), PlanKey: "projection-http",
		Limits: limits,
		Nodes: []orchestration.PlanNode{{
			Key: "A", Title: "Projection node", Description: "Read model source", Duty: orchestration.DutyExecutor,
			AcceptanceCriteria: []string{"projection is stable"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit},
		}, {
			Key: "C", Title: "Projection integrator", Description: "Projection delivery", Duty: orchestration.DutyIntegrator,
			AcceptanceCriteria: []string{"delivery is visible"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindFinalDelivery},
			DependsOn: []string{"A"},
		}},
	}
	if _, err := orchestrator.SubmitPlan(ctx, orchestration.SubmitPlanCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CommandID: missionNewUUID(), ActorID: userID,
		ExpectedRevision: 1, Plan: plan,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.StartMission(ctx, orchestration.StartMissionCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CommandID: missionNewUUID(), ActorID: userID, ExpectedRevision: 2,
		RolePolicyBindings: startBindings,
	}); err != nil {
		t.Fatal(err)
	}
	advanced, err := orchestrator.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CorrelationID: missionNewUUID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(advanced.CreatedRuns) != 1 {
		t.Fatalf("created runs=%d, want 1", len(advanced.CreatedRuns))
	}
	runID := advanced.CreatedRuns[0].ID

	missionPath := "/api/missions/" + missionUUIDText(missionID)
	unauthenticated, requestErr := http.Get(testServer.URL + missionPath)
	if requestErr != nil {
		t.Fatal(requestErr)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mission status=%d, want 401", unauthenticated.StatusCode)
	}
	response := authRequest(t, http.MethodGet, missionPath, nil)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("mission projection status=%d, want 200", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		response.Body.Close()
		t.Fatalf("cache control=%q, want no-store", response.Header.Get("Cache-Control"))
	}
	var snapshot orchestration.MissionProjection
	readJSON(t, response, &snapshot)
	if snapshot.Mission.ID != missionUUIDText(missionID) || snapshot.Mission.CurrentPhase != "executing" {
		t.Fatalf("unexpected mission projection: %#v", snapshot.Mission)
	}
	if len(snapshot.Nodes) != 2 || snapshot.Nodes[0].LatestRun == nil || snapshot.Nodes[0].LatestRun.ID != missionUUIDText(runID) {
		t.Fatalf("projection lost latest run: %#v", snapshot.Nodes)
	}
	if len(snapshot.Team) != 1 || len(snapshot.Team[0].CurrentNodeIDs) != 1 {
		t.Fatalf("projection lost active team assignment: %#v", snapshot.Team)
	}

	response = authRequest(t, http.MethodGet, missionPath+"/activities?after_sequence=1&limit=1", nil)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("activity page status=%d, want 200", response.StatusCode)
	}
	var firstPage orchestration.ActivityPage
	readJSON(t, response, &firstPage)
	if firstPage.ResetRequired || len(firstPage.Items) != 1 || firstPage.Items[0].Sequence != 2 || !firstPage.HasMore {
		t.Fatalf("missed activity was not recovered in order: %#v", firstPage)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM orchestration_activity WHERE workspace_id=$1 AND mission_id=$2 AND sequence=2`, workspaceID, missionID); err != nil {
		t.Fatalf("delete activity to create cursor gap: %v", err)
	}
	response = authRequest(t, http.MethodGet, missionPath+"/activities?after_sequence=1&limit=1", nil)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("cursor gap status=%d, want 200 reset response", response.StatusCode)
	}
	var gapPage orchestration.ActivityPage
	readJSON(t, response, &gapPage)
	if !gapPage.ResetRequired || len(gapPage.Items) != 0 || gapPage.LastSequence != snapshot.Mission.LastSequence {
		t.Fatalf("cursor gap did not request projection reload: %#v", gapPage)
	}
	response = authRequest(t, http.MethodGet, missionPath, nil)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("projection reload status=%d, want 200", response.StatusCode)
	}
	var reloaded orchestration.MissionProjection
	readJSON(t, response, &reloaded)
	if reloaded.Mission.ID != snapshot.Mission.ID || reloaded.Mission.LastSequence != snapshot.Mission.LastSequence {
		t.Fatalf("projection reload changed mission identity or sequence watermark: %#v", reloaded.Mission)
	}

	response = authRequest(t, http.MethodGet, fmt.Sprintf("%s/activities?after_sequence=%d", missionPath, snapshot.Mission.LastSequence+1), nil)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("ahead cursor status=%d, want 200", response.StatusCode)
	}
	var resetPage orchestration.ActivityPage
	readJSON(t, response, &resetPage)
	if !resetPage.ResetRequired || resetPage.LastSequence != snapshot.Mission.LastSequence {
		t.Fatalf("ahead cursor did not request snapshot reset: %#v", resetPage)
	}

	response = authRequest(t, http.MethodGet, missionPath+"/runs/"+missionUUIDText(runID), nil)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("run detail status=%d, want 200", response.StatusCode)
	}
	var detail orchestration.RunDetailProjection
	readJSON(t, response, &detail)
	if detail.Run.ID != missionUUIDText(runID) || detail.Execution == nil || detail.Execution.AgentTaskID == "" {
		t.Fatalf("run detail lost execution mapping: %#v", detail)
	}
	if len(detail.Lineage.Assignments) != 1 || len(detail.Lineage.Runs) != 1 {
		t.Fatalf("run detail lost lineage: %#v", detail.Lineage)
	}

	response = authRequest(t, http.MethodGet, missionPath+"/activities?after_sequence=invalid", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid activity cursor status=%d, want 400", response.StatusCode)
	}
	response = authRequest(t, http.MethodGet, "/api/missions/"+uuid.NewString(), nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing mission status=%d, want 404", response.StatusCode)
	}
}

func TestMissionLifecycleHTTPAuthorizesDispatchesAndCancels(t *testing.T) {
	ctx := context.Background()
	workspaceID := missionTestUUID(t, testWorkspaceID)
	ownerID := missionTestUUID(t, testUserID)
	queries := db.New(testPool)
	repository := orchestration.NewRepository(queries, testPool)
	execution := service.NewTaskExecutionGateway(service.NewTaskService(queries, testPool, nil, events.New()))
	orchestrator := orchestration.NewService(queries, repository, execution, orchestration.DefaultPlanHardLimits())
	limits := orchestration.PlanLimits{MaxParallelRuns: 1, MaxTaskAttempts: 2, MaxReworkCycles: 1}

	var memberID string
	memberEmail := "mission-member-" + uuid.NewString() + "@liexiu.test"
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`, "Mission Member", memberEmail).Scan(&memberID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, testWorkspaceID, memberID); err != nil {
		t.Fatalf("create member membership: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, memberID); err != nil {
			t.Errorf("cleanup member membership: %v", err)
		}
		if _, err := testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, memberID); err != nil {
			t.Errorf("cleanup member user: %v", err)
		}
	})
	memberToken, err := generateTestJWT(memberID, memberEmail, "Mission Member")
	if err != nil {
		t.Fatalf("generate member token: %v", err)
	}

	created, err := orchestrator.CreateMission(ctx, orchestration.CreateMissionCommand{
		WorkspaceID: workspaceID, CommandID: missionNewUUID(), ActorID: ownerID,
		Title: "Lifecycle HTTP integration", Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	missionID := created.Mission.IssueID
	t.Cleanup(func() { cleanupMissionHTTPFixture(t, missionID, workspaceID) })
	plan := orchestration.Plan{
		SchemaVersion: orchestration.PlanSchemaVersion, MissionID: missionUUIDText(missionID), PlanKey: "lifecycle-http",
		Limits: limits,
		Nodes: []orchestration.PlanNode{{
			Key: "execute", Title: "Lifecycle node", Description: "Dispatch a lifecycle run", Duty: orchestration.DutyExecutor,
			AcceptanceCriteria: []string{"the run is dispatched"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit},
		}, {
			Key: "integrate", Title: "Lifecycle integration", Description: "Retain an integrator leaf for the plan contract", Duty: orchestration.DutyIntegrator,
			AcceptanceCriteria: []string{"the lifecycle is observable"}, ArtifactKinds: []orchestration.ArtifactKind{orchestration.ArtifactKindFinalDelivery}, DependsOn: []string{"execute"},
		}},
	}
	if _, err := orchestrator.SubmitPlan(ctx, orchestration.SubmitPlanCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CommandID: missionNewUUID(), ActorID: ownerID,
		ExpectedRevision: 1, Plan: plan,
	}); err != nil {
		t.Fatalf("SubmitPlan: %#v", err)
	}
	startBindings := seedMissionRolePolicyBindings(t, ctx, repository, workspaceID, ownerID, orchestration.DutyExecutor, orchestration.DutyReviewer, orchestration.DutyIntegrator)
	startBindingsBody := missionRolePolicyBindingsBody(startBindings)
	missionPath := "/api/missions/" + missionUUIDText(missionID)

	response := authRequest(t, http.MethodPost, missionPath+"/start", map[string]any{
		"command_id": "not-a-uuid", "expected_revision": 2, "role_policy_bindings": startBindingsBody,
	})
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed start command status=%d, want 400", response.StatusCode)
	}
	response = missionAuthRequest(t, memberToken, http.MethodPost, missionPath+"/start", map[string]any{
		"command_id": uuid.NewString(), "expected_revision": 2, "role_policy_bindings": startBindingsBody,
	})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member start status=%d, want 403", response.StatusCode)
	}
	response = authRequest(t, http.MethodPost, missionPath+"/start", map[string]any{
		"command_id": uuid.NewString(), "expected_revision": 1, "role_policy_bindings": startBindingsBody,
	})
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale start status=%d, want 409", response.StatusCode)
	}

	startCommandID := uuid.NewString()
	startBody := map[string]any{"command_id": startCommandID, "expected_revision": 2, "role_policy_bindings": startBindingsBody}
	response = authRequest(t, http.MethodPost, missionPath+"/start", startBody)
	if response.StatusCode != http.StatusAccepted {
		body := readResponseBody(response)
		response.Body.Close()
		t.Fatalf("start status=%d, want 202: %s", response.StatusCode, body)
	}
	var started handler.MissionLifecycleResponse
	readJSON(t, response, &started)
	if started.MissionID != missionUUIDText(missionID) || started.Status != "running" || started.Replayed || len(started.AffectedRuns) != 1 {
		t.Fatalf("unexpected start response: %#v", started)
	}

	var runCount, taskCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM orchestration_run WHERE workspace_id=$1 AND mission_id=$2`, testWorkspaceID, missionUUIDText(missionID)).Scan(&runCount); err != nil {
		t.Fatalf("count started runs: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE orchestration_run_id IN (SELECT id FROM orchestration_run WHERE workspace_id=$1 AND mission_id=$2)`, testWorkspaceID, missionUUIDText(missionID)).Scan(&taskCount); err != nil {
		t.Fatalf("count dispatched agent tasks: %v", err)
	}
	if runCount != 1 || taskCount != 1 {
		t.Fatalf("start did not create one run and AgentTask: runs=%d tasks=%d", runCount, taskCount)
	}

	response = authRequest(t, http.MethodPost, missionPath+"/start", startBody)
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatalf("start replay status=%d, want 202", response.StatusCode)
	}
	var replayedStart handler.MissionLifecycleResponse
	readJSON(t, response, &replayedStart)
	if !replayedStart.Replayed || replayedStart.Revision != started.Revision {
		t.Fatalf("start replay was not idempotent: %#v", replayedStart)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM orchestration_run WHERE workspace_id=$1 AND mission_id=$2`, testWorkspaceID, missionUUIDText(missionID)).Scan(&runCount); err != nil {
		t.Fatalf("recount replayed runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("start replay duplicated runs: %d", runCount)
	}

	cancelCommandID := uuid.NewString()
	cancelBody := map[string]any{
		"command_id": cancelCommandID, "expected_revision": started.Revision, "reason": "owner stopped the run",
	}
	response = authRequest(t, http.MethodPost, missionPath+"/cancel", cancelBody)
	if response.StatusCode != http.StatusAccepted {
		body := readResponseBody(response)
		response.Body.Close()
		t.Fatalf("cancel status=%d, want 202: %s", response.StatusCode, body)
	}
	var cancelled handler.MissionLifecycleResponse
	readJSON(t, response, &cancelled)
	if cancelled.MissionID != missionUUIDText(missionID) || cancelled.Status != "cancelled" || cancelled.Replayed || len(cancelled.AffectedRuns) != 1 {
		t.Fatalf("unexpected cancel response: %#v", cancelled)
	}
	var taskStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE orchestration_run_id=$1`, cancelled.AffectedRuns[0]).Scan(&taskStatus); err != nil {
		t.Fatalf("load cancelled AgentTask: %v", err)
	}
	if taskStatus != "cancelled" {
		t.Fatalf("cancel did not stop active AgentTask: status=%q", taskStatus)
	}

	response = authRequest(t, http.MethodPost, missionPath+"/cancel", cancelBody)
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatalf("cancel replay status=%d, want 202", response.StatusCode)
	}
	var replayedCancel handler.MissionLifecycleResponse
	readJSON(t, response, &replayedCancel)
	if !replayedCancel.Replayed || replayedCancel.Status != cancelled.Status || len(replayedCancel.AffectedRuns) != 1 {
		t.Fatalf("cancel replay was not idempotent: %#v", replayedCancel)
	}
}

func TestRoleProfileHTTPUsesFixedDutyAndImmutableVersion(t *testing.T) {
	profileKey := "http-reviewer-" + strings.ToLower(uuid.NewString()[:8])
	commandID := uuid.NewString()
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM role_profile WHERE workspace_id=$1 AND profile_key=$2`, testWorkspaceID, profileKey); err != nil {
			t.Errorf("cleanup RoleProfile: %v", err)
		}
	})
	request := map[string]any{
		"command_id": commandID, "profile_key": profileKey, "duty": "reviewer",
		"name": "HTTP security reviewer", "description": "A custom profile on the fixed reviewer duty",
		"config": map[string]any{
			"instructions": "Review security-sensitive Go changes", "required_capabilities": []string{"go", "security"},
			"runtime":         map[string]any{"allowed_runtime_ids": []string{}, "preferred_runtime_ids": []string{}, "providers": []string{}, "models": []string{}},
			"tools":           map[string]any{"allowed_tools": []string{"rg"}, "allowed_paths": []string{"server/"}},
			"budget":          map[string]any{"max_rework_cycles": 1, "max_technical_retries": 1},
			"timeout_seconds": 1800, "max_concurrency": 1,
		},
	}
	path := "/api/workspaces/" + testWorkspaceID + "/role-profiles"
	response := authRequest(t, http.MethodPost, path, request)
	if response.StatusCode != http.StatusCreated {
		body := readResponseBody(response)
		response.Body.Close()
		t.Fatalf("create RoleProfile status=%d body=%s", response.StatusCode, body)
	}
	var created orchestration.CreateRoleProfileVersionResult
	readJSON(t, response, &created)
	if created.Idempotent || created.Profile.Version != 1 || created.Profile.Duty != orchestration.DutyReviewer || created.Profile.ProfileKey != profileKey {
		t.Fatalf("unexpected RoleProfile response: %#v", created)
	}

	response = authRequest(t, http.MethodPost, path, request)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("replay RoleProfile status=%d, want 200", response.StatusCode)
	}
	var replayed orchestration.CreateRoleProfileVersionResult
	readJSON(t, response, &replayed)
	if !replayed.Idempotent || replayed.Profile.ID != created.Profile.ID {
		t.Fatalf("RoleProfile replay did not return original version: %#v", replayed)
	}

	response = authRequest(t, http.MethodGet, path, nil)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("list RoleProfiles status=%d, want 200", response.StatusCode)
	}
	var listed struct {
		Profiles []orchestration.RoleProfileVersion `json:"profiles"`
	}
	readJSON(t, response, &listed)
	found := false
	for _, profile := range listed.Profiles {
		if profile.ProfileKey == profileKey {
			found = profile.Duty == orchestration.DutyReviewer && profile.Version == 1
		}
	}
	if !found {
		t.Fatalf("created RoleProfile missing from latest list: %#v", listed.Profiles)
	}

	invalid := request
	invalid["command_id"] = uuid.NewString()
	invalid["duty"] = "security_reviewer"
	response = authRequest(t, http.MethodPost, path, invalid)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("custom duty status=%d, want 400", response.StatusCode)
	}
}

func missionAuthRequest(t *testing.T, token, method, path string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode mission request: %v", err)
	}
	req, err := http.NewRequest(method, testServer.URL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("create mission request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mission request failed: %v", err)
	}
	return response
}

func missionNewUUID() pgtype.UUID {
	value := uuid.New()
	return pgtype.UUID{Bytes: value, Valid: true}
}

func missionTestUUID(t *testing.T, raw string) pgtype.UUID {
	t.Helper()
	value, err := uuid.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return pgtype.UUID{Bytes: value, Valid: true}
}

func missionUUIDText(value pgtype.UUID) string {
	parsed, _ := uuid.FromBytes(value.Bytes[:])
	return parsed.String()
}

func cleanupMissionHTTPFixture(t *testing.T, missionID, workspaceID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	statements := []string{
		`DELETE FROM agent_task_queue WHERE orchestration_run_id IN (SELECT id FROM orchestration_run WHERE mission_id=$1 AND workspace_id=$2)`,
		`DELETE FROM mission_role_policy_snapshot WHERE mission_id=$1 AND workspace_id=$2`,
		`DELETE FROM review_verdict WHERE mission_id=$1 AND workspace_id=$2`,
		`DELETE FROM artifact WHERE mission_id=$1 AND workspace_id=$2`,
		`DELETE FROM orchestration_run WHERE mission_id=$1 AND workspace_id=$2`,
		`DELETE FROM orchestration_assignment WHERE mission_id=$1 AND workspace_id=$2`,
		`DELETE FROM orchestration_activity WHERE mission_id=$1 AND workspace_id=$2`,
		`DELETE FROM issue_dependency WHERE issue_id IN (SELECT issue_id FROM task_node WHERE mission_id=$1 AND workspace_id=$2)`,
		`DELETE FROM task_node WHERE mission_id=$1 AND workspace_id=$2`,
		`DELETE FROM mission WHERE issue_id=$1 AND workspace_id=$2`,
		`DELETE FROM issue WHERE workspace_id=$2 AND (id=$1 OR parent_issue_id=$1)`,
	}
	for _, statement := range statements {
		if _, err := testPool.Exec(ctx, statement, missionID, workspaceID); err != nil {
			t.Errorf("cleanup mission HTTP fixture: %v", err)
		}
	}
}

func seedMissionRolePolicyBindings(
	t *testing.T,
	ctx context.Context,
	repository *orchestration.Repository,
	workspaceID, actorID pgtype.UUID,
	duties ...orchestration.Duty,
) []orchestration.RolePolicyBinding {
	t.Helper()
	bindings := make([]orchestration.RolePolicyBinding, 0, len(duties))
	profileKeys := make([]string, 0, len(duties))
	for _, duty := range duties {
		key := "http-" + duty.String() + "-" + strings.ToLower(uuid.NewString()[:8])
		created, err := repository.CreateRoleProfileVersion(ctx, orchestration.CreateRoleProfileVersionParams{
			WorkspaceID: workspaceID, CommandID: missionNewUUID(), ActorID: actorID,
			ProfileKey: key, Duty: duty, Name: "HTTP " + duty.String(),
			Config: orchestration.RoleProfileConfig{
				RequiredCapabilities: []string{},
				Runtime:              orchestration.RoleRuntimePreferences{AllowedRuntimeIDs: []string{}, PreferredRuntimeIDs: []string{}, Providers: []string{}, Models: []string{}},
				Tools:                orchestration.RoleToolPermissions{AllowedTools: []string{}, AllowedPaths: []string{}},
				Budget:               orchestration.RoleBudgetLimits{MaxReworkCycles: 1, MaxTechnicalRetries: 1},
				TimeoutSeconds:       900, MaxConcurrency: 1,
			},
		})
		if err != nil {
			t.Fatalf("seed %s RoleProfile: %v", duty, err)
		}
		profileKeys = append(profileKeys, key)
		bindings = append(bindings, orchestration.RolePolicyBinding{Duty: duty, ProfileKey: key, Version: created.Profile.Version})
	}
	t.Cleanup(func() {
		for _, key := range profileKeys {
			if _, err := testPool.Exec(context.Background(), `DELETE FROM role_profile WHERE workspace_id=$1 AND profile_key=$2`, workspaceID, key); err != nil {
				t.Errorf("cleanup RoleProfile %s: %v", key, err)
			}
		}
	})
	return bindings
}

func missionRolePolicyBindingsBody(bindings []orchestration.RolePolicyBinding) []map[string]any {
	result := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, map[string]any{"duty": binding.Duty.String(), "profile_key": binding.ProfileKey, "version": binding.Version})
	}
	return result
}
