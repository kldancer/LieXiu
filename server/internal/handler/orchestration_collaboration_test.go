package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/kailonyang/liexiu/server/internal/middleware"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	"github.com/kailonyang/liexiu/server/internal/util"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

func TestSendRuntimeCollaborationMessageDerivesTaskTokenIdentity(t *testing.T) {
	ctx := context.Background()
	workspaceID := util.MustParseUUID(testWorkspaceID)
	ownerID := util.MustParseUUID(testUserID)
	queries := db.New(testPool)
	repository := orchestration.NewRepository(queries, testPool)
	orchestrationService := orchestration.NewService(queries, repository, nil, orchestration.DefaultPlanHardLimits())
	localHandler := *testHandler
	localHandler.Orchestration = orchestrationService

	created, err := orchestrationService.QuickCreateMission(ctx, orchestration.QuickCreateMissionCommand{
		WorkspaceID: workspaceID, CommandID: util.MustParseUUID(uuid.NewString()), ActorID: ownerID,
		Prompt: "runtime collaboration handler integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	var taskNodeID, agentID, runtimeID, assignmentID, runID, taskID string
	if err := testPool.QueryRow(ctx, `SELECT issue_id FROM task_node WHERE workspace_id=$1 AND mission_id=$2 AND role='executor' LIMIT 1`, workspaceID, created.MissionID).Scan(&taskNodeID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT id,runtime_id FROM agent WHERE workspace_id=$1 AND runtime_id IS NOT NULL ORDER BY created_at LIMIT 1`, workspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_assignment (workspace_id,mission_id,task_node_id,role,agent_id,runtime_id,status,sequence,created_by)
		VALUES ($1,$2,$3,'executor',$4,$5,'active',1,$6) RETURNING id
	`, workspaceID, created.MissionID, taskNodeID, agentID, runtimeID, ownerID).Scan(&assignmentID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_run (workspace_id,mission_id,task_node_id,assignment_id,purpose,attempt,status,input,dispatch_deadline_at,timeout_seconds)
		VALUES ($1,$2,$3,$4,'execute',1,'running','{}',now()+interval '1 hour',3600) RETURNING id
	`, workspaceID, created.MissionID, taskNodeID, assignmentID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id,runtime_id,issue_id,status,context,orchestration_run_id,
			originator_user_id,accountable_user_id,originator_source,trigger_evidence_kind,trigger_evidence_ref_id
		) VALUES ($1,$2,$3,'running','{}',$4,$5,$5,'direct_human','orchestration_run',$4)
		RETURNING id
	`, agentID, runtimeID, taskNodeID, runID, ownerID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, created.MissionID)
	})

	recipientID := agentID
	requestBody := protocol.RuntimeCollaborationToolRequestV1{
		SchemaVersion: 1, Operation: protocol.RuntimeCollaborationReportBlocker,
		CommandID: uuid.NewString(), DedupeKey: "handler:runtime:blocker",
		Recipient: protocol.RuntimeCollaborationRecipientV1{Type: "agent", ID: recipientID},
		Payload:   json.RawMessage(`{"summary":"blocked on bounded dependency"}`),
	}
	body, _ := json.Marshal(requestBody)
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/collaboration/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	member, err := queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: ownerID, WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, member))
	recorder := httptest.NewRecorder()
	localHandler.SendRuntimeCollaborationMessage(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response RuntimeCollaborationToolResponseV1
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Operation != protocol.RuntimeCollaborationReportBlocker || response.Message.Sender.ID != agentID || response.Message.RunID != runID || response.Message.TaskNodeID != taskNodeID || response.Message.MissionID != util.UUIDToString(created.MissionID) {
		t.Fatalf("derived response = %#v", response)
	}
	if response.Message.Recipient.ID != recipientID || response.Message.Type != orchestration.MailboxMessageBlocker || response.ActivitySequence < 1 {
		t.Fatalf("collaboration response = %#v", response)
	}

	forged := httptest.NewRequest(http.MethodPost, "/api/orchestration/collaboration/messages", bytes.NewReader(body))
	forged.Header.Set("X-Workspace-ID", testWorkspaceID)
	forged.Header.Set("X-User-ID", testUserID)
	forged.Header.Set("X-Agent-ID", agentID)
	forged.Header.Set("X-Task-ID", taskID)
	forged = forged.WithContext(middleware.SetMemberContext(forged.Context(), testWorkspaceID, member))
	forgedRecorder := httptest.NewRecorder()
	localHandler.SendRuntimeCollaborationMessage(forgedRecorder, forged)
	if forgedRecorder.Code != http.StatusForbidden {
		t.Fatalf("forged member status=%d body=%s", forgedRecorder.Code, forgedRecorder.Body.String())
	}
}
