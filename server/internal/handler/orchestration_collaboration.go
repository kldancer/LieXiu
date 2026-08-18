package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	"github.com/kailonyang/liexiu/server/internal/util"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

type RuntimeCollaborationToolResponseV1 struct {
	SchemaVersion    int                                    `json:"schema_version"`
	Operation        protocol.RuntimeCollaborationOperation `json:"operation"`
	Message          orchestration.StoredMailboxMessage     `json:"message"`
	ActivitySequence int64                                  `json:"activity_sequence"`
	Idempotent       bool                                   `json:"idempotent"`
}

// SendRuntimeCollaborationMessage is the one provider-neutral write boundary
// for collaboration tools. Only an authenticated mat_ AgentTask token may use
// it. Workspace, Mission, Run, TaskNode, sender Agent and accountable human are
// all derived server-side; no Runtime adapter can self-assert those fields.
func (h *Handler) SendRuntimeCollaborationMessage(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.runtimeCollaborationScope(w, r)
	if !ok {
		return
	}
	var request protocol.RuntimeCollaborationToolRequestV1
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid collaboration tool request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid collaboration tool request")
		return
	}
	if request.SchemaVersion != protocol.RuntimeCollaborationSchemaVersion || !request.Operation.Valid() {
		writeError(w, http.StatusBadRequest, "unsupported collaboration tool contract")
		return
	}
	messageType, err := orchestration.MailboxMessageTypeForRuntimeOperation(request.Operation)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported collaboration operation")
		return
	}
	commandID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(request.CommandID), "command_id")
	if !ok {
		return
	}
	recipientID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(request.Recipient.ID), "recipient.id")
	if !ok {
		return
	}
	artifactID, ok := parseOptionalRuntimeCollaborationUUID(w, request.ArtifactID, "artifact_id")
	if !ok {
		return
	}
	replyID, ok := parseOptionalRuntimeCollaborationUUID(w, request.ReplyToMessageID, "reply_to_message_id")
	if !ok {
		return
	}
	ttl := orchestration.RuntimeCollaborationDefaultTTL
	if request.TTLSeconds < 0 || request.TTLSeconds > int64(orchestration.MailboxMaxTTL/time.Second) {
		writeError(w, http.StatusBadRequest, "ttl_seconds must be between 1 and 604800, or 0 for the default")
		return
	}
	if request.TTLSeconds > 0 {
		ttl = time.Duration(request.TTLSeconds) * time.Second
	}
	result, err := h.Orchestration.SendMailboxMessage(r.Context(), orchestration.SendMailboxMessageCommand{
		WorkspaceID: scope.workspaceID, MissionID: scope.run.MissionID,
		CommandID: commandID, CorrelationID: commandID, ActorID: scope.actorID,
		TaskNodeID: scope.run.TaskNodeID, RunID: scope.run.ID,
		ArtifactID: artifactID, ReplyToMessageID: replyID,
		Type:      messageType,
		Sender:    orchestration.MailboxActorRef{Type: orchestration.MailboxActorAgent, ID: uuidToString(scope.task.AgentID)},
		Recipient: orchestration.MailboxActorRef{Type: orchestration.MailboxActorType(request.Recipient.Type), ID: uuidToString(recipientID)},
		DedupeKey: request.DedupeKey, TTL: ttl, Hops: request.Hops,
		PayloadVersion: orchestration.MailboxPayloadVersion, Payload: request.Payload,
	})
	if err != nil {
		writeRuntimeCollaborationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, RuntimeCollaborationToolResponseV1{
		SchemaVersion: protocol.RuntimeCollaborationSchemaVersion,
		Operation:     request.Operation, Message: result.Message,
		ActivitySequence: result.Activity.Sequence, Idempotent: result.Idempotent,
	})
}

type runtimeCollaborationScope struct {
	workspaceID pgtype.UUID
	actorID     pgtype.UUID
	task        db.AgentTaskQueue
	run         db.OrchestrationRun
}

func (h *Handler) runtimeCollaborationScope(w http.ResponseWriter, r *http.Request) (runtimeCollaborationScope, bool) {
	var scope runtimeCollaborationScope
	if h == nil || h.Queries == nil || h.Orchestration == nil {
		writeError(w, http.StatusServiceUnavailable, "collaboration service unavailable")
		return scope, false
	}
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "collaboration tools require an orchestration task token")
		return scope, false
	}
	workspaceID, err := util.ParseUUID(ctxWorkspaceID(r.Context()))
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid task collaboration scope")
		return scope, false
	}
	taskID, err := util.ParseUUID(strings.TrimSpace(r.Header.Get("X-Task-ID")))
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid task collaboration scope")
		return scope, false
	}
	agentID, err := util.ParseUUID(strings.TrimSpace(r.Header.Get("X-Agent-ID")))
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid task collaboration scope")
		return scope, false
	}
	actorID, err := util.ParseUUID(strings.TrimSpace(r.Header.Get("X-User-ID")))
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid task collaboration scope")
		return scope, false
	}
	task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{ID: taskID, WorkspaceID: workspaceID})
	if err != nil || !task.OrchestrationRunID.Valid || task.AgentID != agentID {
		writeError(w, http.StatusForbidden, "task is not an active orchestration collaboration principal")
		return scope, false
	}
	run, err := h.Queries.GetOrchestrationRunInWorkspace(r.Context(), db.GetOrchestrationRunInWorkspaceParams{RunID: task.OrchestrationRunID, WorkspaceID: workspaceID})
	if err != nil {
		writeError(w, http.StatusForbidden, "task is not an active orchestration collaboration principal")
		return scope, false
	}
	principal, err := h.Queries.GetMailboxRunPrincipal(r.Context(), db.GetMailboxRunPrincipalParams{RunID: run.ID, WorkspaceID: workspaceID, MissionID: run.MissionID})
	if err != nil || principal.AgentID != task.AgentID {
		writeError(w, http.StatusForbidden, "task is not an active orchestration collaboration principal")
		return scope, false
	}
	return runtimeCollaborationScope{workspaceID: workspaceID, actorID: actorID, task: task, run: run}, true
}

func parseOptionalRuntimeCollaborationUUID(w http.ResponseWriter, value, path string) (pgtype.UUID, bool) {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, strings.TrimSpace(value), path)
}

func writeRuntimeCollaborationError(w http.ResponseWriter, err error) {
	var validationErr orchestration.CommandValidationError
	switch {
	case errors.As(err, &validationErr):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid collaboration command", "errors": validationErr.Errors})
	case errors.Is(err, orchestration.ErrMailboxPermissionDenied):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, orchestration.ErrMailboxReferenceInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, orchestration.ErrCommandConflict),
		errors.Is(err, orchestration.ErrMailboxDedupeConflict),
		errors.Is(err, orchestration.ErrMailboxStatusConflict),
		errors.Is(err, orchestration.ErrMailboxExpired):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "collaboration resource not found")
	default:
		writeError(w, http.StatusInternalServerError, "failed to send collaboration message")
	}
}
