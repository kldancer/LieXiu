package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

type CancelTaskByUserResponse struct {
	AgentTaskResponse
}

// CancelTaskByUser cancels a task within the caller's current workspace.
func (h *Handler) CancelTaskByUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskId")
	taskUUID, ok := parseUUIDOrBadRequest(w, taskID, "task id")
	if !ok {
		return
	}

	task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{
		ID:          taskUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          task.AgentID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}

	cancelled, err := h.TaskService.CancelTask(r.Context(), taskUUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := CancelTaskByUserResponse{
		AgentTaskResponse: taskToResponse(*cancelled, workspaceID),
	}
	h.hydrateTaskAttributions(r.Context(), []*TaskAttribution{resp.AgentTaskResponse.Attribution})

	writeJSON(w, http.StatusOK, resp)
}
