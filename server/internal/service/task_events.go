package service

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/util"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
	"github.com/kailonyang/liexiu/server/pkg/redact"
)

func (s *TaskService) broadcastTaskDispatch(ctx context.Context, task db.AgentTaskQueue) {
	var payload map[string]any
	if task.Context != nil {
		json.Unmarshal(task.Context, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["task_id"] = util.UUIDToString(task.ID)
	payload["runtime_id"] = util.UUIDToString(task.RuntimeID)
	payload["issue_id"] = util.UUIDToString(task.IssueID)
	payload["agent_id"] = util.UUIDToString(task.AgentID)
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskDispatch,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		Payload:     payload,
	})
}

// taskEvent builds the shared task-lifecycle event contract. Scope hints are
// duplicated on the envelope intentionally: current listeners remain
// compatible with the payload map, while the realtime layer can route without
// decoding it once per-resource fanout is enabled.
func taskEvent(eventType, workspaceID string, task db.AgentTaskQueue, extra ...map[string]any) events.Event {
	payload := map[string]any{
		"task_id":  util.UUIDToString(task.ID),
		"agent_id": util.UUIDToString(task.AgentID),
		"issue_id": util.UUIDToString(task.IssueID),
		"status":   task.Status,
	}
	e := events.Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		TaskID:      util.UUIDToString(task.ID),
		Payload:     payload,
	}
	for _, fields := range extra {
		for key, value := range fields {
			payload[key] = value
		}
	}
	return e
}

func (s *TaskService) publishTaskEvent(eventType, workspaceID string, task db.AgentTaskQueue, extra ...map[string]any) {
	if workspaceID == "" {
		return
	}
	s.Bus.Publish(taskEvent(eventType, workspaceID, task, extra...))
}

func (s *TaskService) broadcastTaskEvent(ctx context.Context, eventType string, task db.AgentTaskQueue, extra ...map[string]any) {
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	s.publishTaskEvent(eventType, workspaceID, task, extra...)
}

// taskFailedFields adds the terminal failure context required by channel
// outbounds without changing the long-standing map payload used by existing
// task event consumers. Error text is redacted and omitted while an automatic
// retry is pending, so consumers can distinguish an intermediate failed
// attempt from a user-visible terminal failure.
func taskFailedFields(errMsg, failureReason string, retryPending bool) map[string]any {
	fields := map[string]any{
		"failure_reason": failureReason,
		"retry_pending":  retryPending,
	}
	if errMsg != "" && !retryPending {
		fields["error"] = redact.Text(errMsg)
	}
	return fields
}

func (s *TaskService) publishTaskFailedEvent(workspaceID string, task db.AgentTaskQueue, errMsg, failureReason string, retryPending bool) {
	s.publishTaskEvent(protocol.EventTaskFailed, workspaceID, task, taskFailedFields(errMsg, failureReason, retryPending))
}

func (s *TaskService) broadcastTaskFailedEvent(ctx context.Context, task db.AgentTaskQueue, errMsg, failureReason string, retryPending bool) {
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	s.publishTaskFailedEvent(workspaceID, task, errMsg, failureReason, retryPending)
}

// ResolveTaskWorkspaceID determines the workspace ID for a task.
// Runtime, agent, and issue tasks retain their canonical workspace lookup.
func (s *TaskService) ResolveTaskWorkspaceID(ctx context.Context, task db.AgentTaskQueue) string {
	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			return util.UUIDToString(issue.WorkspaceID)
		}
	}
	if task.RuntimeID.Valid {
		if runtime, err := s.Queries.GetAgentRuntime(ctx, task.RuntimeID); err == nil {
			return util.UUIDToString(runtime.WorkspaceID)
		}
	}
	if task.AgentID.Valid {
		if agent, err := s.Queries.GetAgent(ctx, task.AgentID); err == nil {
			return util.UUIDToString(agent.WorkspaceID)
		}
	}
	return ""
}

// broadcastIssueUpdated publishes the issue:updated event the frontend's
// realtime reconcile (onIssueUpdated) relies on to move an issue between status
// columns / status filters and reconcile their bucket counts. prevStatus is the
// issue's status before the write so the client can gate that reconcile on
// status_changed.
//
// The `issue` payload is a map (IssueToMap), which the workspace WS fanout
// marshals and broadcasts as-is — that is what drives the UI reconcile. This
// does not reproduce the full HTTP UpdateIssue side effects: a background
// status reset publishes the realtime projection only. That separation is
// intentional for the realtime-staleness fix (#4648 / MUL-3782).
func (s *TaskService) broadcastIssueUpdated(issue db.Issue, prevStatus string) {
	prefix := s.getIssuePrefix(issue.WorkspaceID)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueUpdated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload: map[string]any{
			"issue":          IssueToMap(issue, prefix),
			"status_changed": prevStatus != issue.Status,
			"prev_status":    prevStatus,
		},
	})
}

// IssueToMap renders an issue row as the map shape the issue:created /
// issue:updated broadcast payloads carry under their "issue" key. It is the
// single source of truth for that shape wherever the event is published from
// outside the HTTP handler — the channel engine's /issue command on
// issue:created, the background stuck-issue status reset on issue:updated.
// The workspace WS fanout marshals it as-is for the UI and downstream clients
// use the stable identity fields to reconcile their issue cache.
//
// The map must stay key-compatible with handler.IssueResponse, the other
// rendering of the same event. Clients type both as a complete Issue and
// insert it straight into the list cache without runtime validation, so a
// field missing here is a field that reads back undefined until the next
// refetch — see TestIssueToMap_KeysMatchIssueResponse, which fails if the two
// renderings drift apart.
func IssueToMap(issue db.Issue, issuePrefix string) map[string]any {
	return map[string]any{
		"id":              util.UUIDToString(issue.ID),
		"workspace_id":    util.UUIDToString(issue.WorkspaceID),
		"number":          issue.Number,
		"identifier":      IssueIdentifier(issuePrefix, issue.Number),
		"title":           issue.Title,
		"description":     util.TextToPtr(issue.Description),
		"status":          issue.Status,
		"priority":        issue.Priority,
		"assignee_type":   util.TextToPtr(issue.AssigneeType),
		"assignee_id":     util.UUIDToPtr(issue.AssigneeID),
		"creator_type":    issue.CreatorType,
		"creator_id":      util.UUIDToString(issue.CreatorID),
		"parent_issue_id": util.UUIDToPtr(issue.ParentIssueID),
		"project_id":      util.UUIDToPtr(issue.ProjectID),
		"position":        issue.Position,
		"stage":           util.Int4ToPtr(issue.Stage),
		"start_date":      util.DateToPtr(issue.StartDate),
		"due_date":        util.DateToPtr(issue.DueDate),
		"created_at":      util.TimestampToString(issue.CreatedAt),
		"updated_at":      util.TimestampToString(issue.UpdatedAt),
		"metadata":        util.JSONObjectOrEmpty(issue.Metadata),
	}
}

// IssueIdentifier renders the human-facing issue key ("MUL-42"). Callers that
// resolve the workspace prefix defensively may pass "": a failed workspace
// lookup should not surface as a stray "-42", so the number stands alone as
// "#42". The HTTP layer never passes "" — handler.getIssuePrefix derives a
// prefix from the workspace name before rendering anything.
func IssueIdentifier(issuePrefix string, number int32) string {
	if issuePrefix == "" {
		return "#" + strconv.Itoa(int(number))
	}
	return issuePrefix + "-" + strconv.Itoa(int(number))
}

// agentToMap builds a simple map for broadcasting agent status updates.
func agentToMap(a db.Agent) map[string]any {
	var rc any
	if a.RuntimeConfig != nil {
		json.Unmarshal(a.RuntimeConfig, &rc)
	}
	return map[string]any{
		"id":                   util.UUIDToString(a.ID),
		"workspace_id":         util.UUIDToString(a.WorkspaceID),
		"runtime_id":           util.UUIDToString(a.RuntimeID),
		"name":                 a.Name,
		"description":          a.Description,
		"avatar_url":           util.TextToPtr(a.AvatarUrl),
		"runtime_mode":         a.RuntimeMode,
		"runtime_config":       rc,
		"visibility":           a.Visibility,
		"status":               a.Status,
		"max_concurrent_tasks": a.MaxConcurrentTasks,
		"owner_id":             util.UUIDToPtr(a.OwnerID),
		"skills":               []any{},
		"created_at":           util.TimestampToString(a.CreatedAt),
		"updated_at":           util.TimestampToString(a.UpdatedAt),
		"archived_at":          util.TimestampToPtr(a.ArchivedAt),
		"archived_by":          util.UUIDToPtr(a.ArchivedBy),
	}
}
