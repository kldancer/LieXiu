package service

import (
	"strings"
	"testing"

	"github.com/kailonyang/liexiu/server/internal/util"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

func TestTaskFailedFieldsTerminalFailure(t *testing.T) {
	secret := "sk-" + strings.Repeat("a", 24)
	fields := taskFailedFields("provider rejected "+secret, "agent_error", false)

	if fields["failure_reason"] != "agent_error" {
		t.Fatalf("failure_reason = %v, want agent_error", fields["failure_reason"])
	}
	if fields["retry_pending"] != false {
		t.Fatalf("retry_pending = %v, want false", fields["retry_pending"])
	}
	errorText, ok := fields["error"].(string)
	if !ok || errorText == "" {
		t.Fatal("terminal failure must include deliverable error text")
	}
	if strings.Contains(errorText, secret) {
		t.Fatal("terminal failure payload leaked an API key")
	}
	if !strings.Contains(errorText, "[REDACTED API KEY]") {
		t.Fatalf("terminal failure payload was not redacted: %q", errorText)
	}
}

func TestTaskFailedFieldsRetryPendingOmitsError(t *testing.T) {
	fields := taskFailedFields("task timed out", "timeout", true)

	if fields["retry_pending"] != true {
		t.Fatalf("retry_pending = %v, want true", fields["retry_pending"])
	}
	if _, present := fields["error"]; present {
		t.Fatal("retry-pending failure must not expose a terminal error")
	}
}

func TestTaskEventCarriesPayloadAndScopeHints(t *testing.T) {
	task := db.AgentTaskQueue{ID: testUUID(41), AgentID: testUUID(42), IssueID: testUUID(43), Status: "failed"}
	e := taskEvent(protocol.EventTaskFailed, "workspace-1", task, map[string]any{
		"failure_reason": "timeout",
		"retry_pending":  false,
	})

	if e.TaskID != util.UUIDToString(task.ID) {
		t.Fatalf("event TaskID = %q, want %q", e.TaskID, util.UUIDToString(task.ID))
	}
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", e.Payload)
	}
	for key, want := range map[string]any{
		"task_id":        util.UUIDToString(task.ID),
		"agent_id":       util.UUIDToString(task.AgentID),
		"issue_id":       util.UUIDToString(task.IssueID),
		"status":         "failed",
		"failure_reason": "timeout",
		"retry_pending":  false,
	} {
		if got := payload[key]; got != want {
			t.Errorf("payload[%q] = %v, want %v", key, got, want)
		}
	}
}
