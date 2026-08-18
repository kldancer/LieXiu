package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
)

type CreateRoleProfileVersionRequest struct {
	CommandID   string                          `json:"command_id"`
	ProfileKey  string                          `json:"profile_key"`
	Duty        orchestration.Duty              `json:"duty"`
	Name        string                          `json:"name"`
	Description string                          `json:"description"`
	Config      orchestration.RoleProfileConfig `json:"config"`
}

func (h *Handler) CreateRoleProfileVersion(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Orchestration == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration service unavailable")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace_id")
	if !ok {
		return
	}
	actorText, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorID, ok := parseUUIDOrBadRequest(w, actorText, "actor_id")
	if !ok {
		return
	}
	var req CreateRoleProfileVersionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	commandID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.CommandID), "command_id")
	if !ok {
		return
	}
	result, err := h.Orchestration.CreateRoleProfileVersion(r.Context(), orchestration.CreateRoleProfileVersionCommand{
		WorkspaceID: workspaceID, CommandID: commandID, ActorID: actorID,
		ProfileKey: req.ProfileKey, Duty: req.Duty, Name: req.Name,
		Description: req.Description, Config: req.Config,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (h *Handler) ListRoleProfiles(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Orchestration == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration service unavailable")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace_id")
	if !ok {
		return
	}
	profiles, err := h.Orchestration.ListLatestRoleProfiles(r.Context(), workspaceID)
	if err != nil {
		writeMissionReadError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}
