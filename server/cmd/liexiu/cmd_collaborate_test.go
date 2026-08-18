package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

func newCollaborateTestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "send"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("operation", "", "")
	cmd.Flags().String("recipient-type", "", "")
	cmd.Flags().String("recipient-id", "", "")
	cmd.Flags().String("dedupe-key", "", "")
	cmd.Flags().String("command-id", "", "")
	cmd.Flags().String("payload", "", "")
	cmd.Flags().Bool("payload-stdin", false, "")
	cmd.Flags().String("payload-file", "", "")
	cmd.Flags().Bool("allow-external-file", false, "")
	cmd.Flags().String("artifact-id", "", "")
	cmd.Flags().String("reply-to-message-id", "", "")
	cmd.Flags().Duration("ttl", 24*time.Hour, "")
	cmd.Flags().Int("hops", 0, "")
	cmd.Flags().String("output", "json", "")
	return cmd
}

func TestRunCollaborateSendUsesTaskScopedProviderNeutralContract(t *testing.T) {
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	taskID := uuid.NewString()
	recipientID := uuid.NewString()
	commandID := uuid.NewString()
	var got protocol.RuntimeCollaborationToolRequestV1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/orchestration/collaboration/messages" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer mat_test_task_token" || r.Header.Get("X-Agent-ID") != agentID || r.Header.Get("X-Task-ID") != taskID || r.Header.Get("X-Workspace-ID") != workspaceID {
			t.Fatalf("task identity headers = %#v", r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1, "operation": "request_context",
			"message": map[string]any{"id": uuid.NewString()}, "activity_sequence": 7, "idempotent": false,
		})
	}))
	defer server.Close()
	t.Setenv("LIEXIU_SERVER_URL", server.URL)
	t.Setenv("LIEXIU_WORKSPACE_ID", workspaceID)
	t.Setenv("LIEXIU_AGENT_ID", agentID)
	t.Setenv("LIEXIU_TASK_ID", taskID)
	t.Setenv("LIEXIU_TOKEN", "mat_test_task_token")
	t.Setenv("LIEXIU_TASK_CONFIG_ROOT", t.TempDir())

	cmd := newCollaborateTestCommand()
	_ = cmd.Flags().Set("operation", "request_context")
	_ = cmd.Flags().Set("recipient-type", "agent")
	_ = cmd.Flags().Set("recipient-id", recipientID)
	_ = cmd.Flags().Set("dedupe-key", "run:test:context:api")
	_ = cmd.Flags().Set("command-id", commandID)
	_ = cmd.Flags().Set("payload", `{"summary":"need contract"}`)
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := runCollaborateSend(cmd, nil); err != nil {
		t.Fatalf("runCollaborateSend: %v", err)
	}
	if got.SchemaVersion != 1 || got.Operation != protocol.RuntimeCollaborationRequestContext || got.CommandID != commandID || got.Recipient.ID != recipientID || got.DedupeKey != "run:test:context:api" {
		t.Fatalf("request = %#v", got)
	}
	if got.TTLSeconds != 86400 || got.Hops != 0 || string(got.Payload) != `{"summary":"need contract"}` {
		t.Fatalf("bounded request fields = ttl %d hops %d payload %s", got.TTLSeconds, got.Hops, got.Payload)
	}
	if bytes.Contains(output.Bytes(), []byte("mat_test_task_token")) {
		t.Fatal("output leaked task token")
	}
}

func TestCollaborateSendValidatesOperationReferencesAndPayload(t *testing.T) {
	if _, err := parseCollaborationPayload(`{} trailing`); err == nil {
		t.Fatal("trailing payload accepted")
	}
	if _, err := parseCollaborationPayload(`[]`); err == nil {
		t.Fatal("array payload accepted")
	}
	if err := validateCollaborationOperationRefs(protocol.RuntimeCollaborationRespondContext, uuid.NewString(), "", 0); err == nil {
		t.Fatal("context response without reply/hop accepted")
	}
	if err := validateCollaborationOperationRefs(protocol.RuntimeCollaborationNotifyArtifact, "", "", 0); err == nil {
		t.Fatal("artifact notice without artifact accepted")
	}
	if err := validateCollaborationOperationRefs(protocol.RuntimeCollaborationReportBlocker, uuid.NewString(), "", 0); err == nil {
		t.Fatal("blocker with unrelated artifact accepted")
	}
}
