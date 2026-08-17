package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
)

type QuickCreateMissionRequest struct {
	CommandID string `json:"command_id"`
	Prompt    string `json:"prompt"`
	ProjectID string `json:"project_id,omitempty"`
}

type QuickCreateMissionResponse struct {
	MissionID string                      `json:"mission_id"`
	Status    orchestration.MissionStatus `json:"status"`
	Revision  int64                       `json:"revision"`
	Replayed  bool                        `json:"replayed"`
}

type MissionLifecycleRequest struct {
	CommandID        string `json:"command_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

type MissionLifecycleResponse struct {
	MissionID    string   `json:"mission_id"`
	Status       string   `json:"status"`
	Revision     int64    `json:"revision"`
	AffectedRuns []string `json:"affected_run_ids,omitempty"`
	Replayed     bool     `json:"replayed"`
}

type RetryMissionTaskRequest struct {
	CommandID            string `json:"command_id"`
	ExpectedRevision     int64  `json:"expected_revision"`
	ExpectedTaskRevision int64  `json:"expected_task_revision"`
	Reason               string `json:"reason,omitempty"`
}

type RetryMissionTaskResponse struct {
	MissionID     string   `json:"mission_id"`
	TaskNodeID    string   `json:"task_node_id"`
	Status        string   `json:"status"`
	Revision      int64    `json:"revision"`
	CreatedRunIDs []string `json:"created_run_ids"`
	Replayed      bool     `json:"replayed"`
}

type ApproveMissionBudgetRequest struct {
	CommandID         string `json:"command_id"`
	ExpectedRevision  int64  `json:"expected_revision"`
	GrantTokens       int64  `json:"grant_tokens,omitempty"`
	GrantCostUSDTicks int64  `json:"grant_cost_usd_ticks,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

type ApproveMissionBudgetResponse struct {
	MissionID     string   `json:"mission_id"`
	Status        string   `json:"status"`
	Revision      int64    `json:"revision"`
	CreatedRunIDs []string `json:"created_run_ids"`
	Replayed      bool     `json:"replayed"`
}

// QuickCreateMission keeps the historical /api/issues/quick-create route as a
// compatibility surface while changing its product meaning: it now creates a
// ready, reviewable Mission plan and never dispatches an AgentTask. Starting
// execution remains an explicit owner command.
func (h *Handler) QuickCreateMission(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Orchestration == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration service unavailable")
		return
	}
	var req QuickCreateMissionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace_id")
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
	commandID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.CommandID), "command_id")
	if !ok {
		return
	}
	var projectID pgtype.UUID
	if value := strings.TrimSpace(req.ProjectID); value != "" {
		projectID, ok = parseUUIDOrBadRequest(w, value, "project_id")
		if !ok {
			return
		}
	}

	result, err := h.Orchestration.QuickCreateMission(r.Context(), orchestration.QuickCreateMissionCommand{
		WorkspaceID: workspaceID,
		CommandID:   commandID,
		ActorID:     actorID,
		Prompt:      prompt,
		ProjectID:   projectID,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, QuickCreateMissionResponse{
		MissionID: uuidToString(result.MissionID),
		Status:    result.Status,
		Revision:  result.Revision,
		Replayed:  result.Replayed,
	})
}

func (h *Handler) RetryMissionTask(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Orchestration == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration service unavailable")
		return
	}
	workspaceID, missionID, ok := h.missionReadScope(w, r)
	if !ok {
		return
	}
	taskNodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskNodeID"), "task_node_id")
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
	var req RetryMissionTaskRequest
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
	result, err := h.Orchestration.RetryTaskNode(r.Context(), orchestration.RetryTaskNodeCommand{
		WorkspaceID: workspaceID, MissionID: missionID, TaskNodeID: taskNodeID,
		CommandID: commandID, ActorID: actorID,
		ExpectedRevision: req.ExpectedRevision, ExpectedTaskRevision: req.ExpectedTaskRevision,
		Reason: req.Reason,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	runIDs := make([]string, 0, len(result.Advance.CreatedRuns))
	for _, run := range result.Advance.CreatedRuns {
		runIDs = append(runIDs, uuidToString(run.ID))
	}
	writeJSON(w, http.StatusAccepted, RetryMissionTaskResponse{
		MissionID: uuidToString(result.Mission.IssueID), TaskNodeID: uuidToString(result.TaskNode.IssueID),
		Status: result.TaskNode.Status, Revision: result.TaskNode.Revision,
		CreatedRunIDs: runIDs, Replayed: result.Idempotent,
	})
}

// StartMission is the explicit owner gate between a reviewable plan and
// execution. The initial Advance is part of the HTTP command boundary so a
// successful click cannot leave a running Mission with no dispatched work.
func (h *Handler) StartMission(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, actorID, req, ok := h.missionLifecycleScope(w, r)
	if !ok {
		return
	}
	result, err := h.Orchestration.StartMission(r.Context(), orchestration.StartMissionCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CommandID: req.commandID,
		ActorID: actorID, ExpectedRevision: req.request.ExpectedRevision,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	advance, err := h.Orchestration.AdvanceMission(r.Context(), orchestration.AdvanceMissionCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CorrelationID: req.commandID,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	runIDs := make([]string, 0, len(advance.CreatedRuns))
	for _, run := range advance.CreatedRuns {
		runIDs = append(runIDs, uuidToString(run.ID))
	}
	mission := result.Mission
	if advance.Mission.IssueID.Valid {
		mission = advance.Mission
	}
	writeJSON(w, http.StatusAccepted, MissionLifecycleResponse{
		MissionID: uuidToString(mission.IssueID), Status: mission.Status,
		Revision: mission.Revision, AffectedRuns: runIDs, Replayed: result.Idempotent,
	})
}

func (h *Handler) CancelMission(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, actorID, req, ok := h.missionLifecycleScope(w, r)
	if !ok {
		return
	}
	result, err := h.Orchestration.CancelMission(r.Context(), orchestration.CancelMissionCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CommandID: req.commandID,
		ActorID: actorID, ExpectedRevision: req.request.ExpectedRevision,
		Reason: req.request.Reason,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	runIDs := make([]string, 0, len(result.ActiveRuns))
	for _, run := range result.ActiveRuns {
		runIDs = append(runIDs, uuidToString(run.ID))
	}
	writeJSON(w, http.StatusAccepted, MissionLifecycleResponse{
		MissionID: uuidToString(result.Mission.IssueID), Status: result.Mission.Status,
		Revision: result.Mission.Revision, AffectedRuns: runIDs, Replayed: result.Idempotent,
	})
}

type parsedMissionLifecycleRequest struct {
	request   MissionLifecycleRequest
	commandID pgtype.UUID
}

func (h *Handler) missionLifecycleScope(
	w http.ResponseWriter,
	r *http.Request,
) (workspaceID, missionID, actorID pgtype.UUID, request parsedMissionLifecycleRequest, ok bool) {
	workspaceID, missionID, ok = h.missionReadScope(w, r)
	if !ok {
		return workspaceID, missionID, actorID, request, false
	}
	actorText, valid := requireUserID(w, r)
	if !valid {
		return workspaceID, missionID, actorID, request, false
	}
	actorID, valid = parseUUIDOrBadRequest(w, actorText, "actor_id")
	if !valid {
		return workspaceID, missionID, actorID, request, false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request.request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return workspaceID, missionID, actorID, request, false
	}
	request.commandID, valid = parseUUIDOrBadRequest(w, strings.TrimSpace(request.request.CommandID), "command_id")
	return workspaceID, missionID, actorID, request, valid
}

func (h *Handler) ApproveMissionBudget(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Orchestration == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration service unavailable")
		return
	}
	workspaceID, missionID, ok := h.missionReadScope(w, r)
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
	var req ApproveMissionBudgetRequest
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
	result, err := h.Orchestration.ApproveMissionBudget(r.Context(), orchestration.ApproveMissionBudgetCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CommandID: commandID, ActorID: actorID,
		ExpectedRevision: req.ExpectedRevision, GrantTokens: req.GrantTokens,
		GrantCostUSDTicks: req.GrantCostUSDTicks, Reason: req.Reason,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	runIDs := make([]string, 0, len(result.Advance.CreatedRuns))
	for _, run := range result.Advance.CreatedRuns {
		runIDs = append(runIDs, uuidToString(run.ID))
	}
	mission := result.Mission
	if result.Advance.Mission.IssueID.Valid {
		mission = result.Advance.Mission
	}
	writeJSON(w, http.StatusAccepted, ApproveMissionBudgetResponse{
		MissionID: uuidToString(mission.IssueID), Status: mission.Status, Revision: mission.Revision,
		CreatedRunIDs: runIDs, Replayed: result.Idempotent,
	})
}

func (h *Handler) GetMissionProjection(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, ok := h.missionReadScope(w, r)
	if !ok {
		return
	}
	projection, err := h.Orchestration.GetMissionProjection(r.Context(), workspaceID, missionID)
	if err != nil {
		writeMissionReadError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, projection)
}

func (h *Handler) ListMissionActivities(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, ok := h.missionReadScope(w, r)
	if !ok {
		return
	}
	afterSequence, ok := parseNonNegativeQueryInt(w, r, "after_sequence", 0)
	if !ok {
		return
	}
	limitValue, ok := parseNonNegativeQueryInt(w, r, "limit", orchestration.DefaultProjectionActivityLimit)
	if !ok {
		return
	}
	if limitValue > int64(orchestration.MaxActivityPageSize) {
		writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
		return
	}
	page, err := h.Orchestration.ListMissionActivities(r.Context(), workspaceID, missionID, afterSequence, int32(limitValue))
	if err != nil {
		writeMissionReadError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) GetMissionRunDetail(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, ok := h.missionReadScope(w, r)
	if !ok {
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runID"), "run_id")
	if !ok {
		return
	}
	detail, err := h.Orchestration.GetRunDetail(r.Context(), workspaceID, missionID, runID)
	if err != nil {
		writeMissionReadError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) missionReadScope(w http.ResponseWriter, r *http.Request) (workspaceID, missionID pgtype.UUID, ok bool) {
	if h == nil || h.Orchestration == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration service unavailable")
		return workspaceID, missionID, false
	}
	workspace, valid := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace_id")
	if !valid {
		return workspaceID, missionID, false
	}
	mission, valid := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "mission_id")
	if !valid {
		return workspaceID, missionID, false
	}
	return workspace, mission, true
}

func parseNonNegativeQueryInt(w http.ResponseWriter, r *http.Request, name string, fallback int) (int64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return int64(fallback), true
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < 0 {
		writeError(w, http.StatusBadRequest, name+" must be a non-negative integer")
		return 0, false
	}
	return value, true
}

func writeMissionReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "mission resource not found")
	case errors.Is(err, orchestration.ErrInvalidActivityCursor), errors.Is(err, orchestration.ErrInvalidActivityLimit):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "failed to load mission")
	}
}

func writeMissionCommandError(w http.ResponseWriter, err error) {
	var validationErr orchestration.CommandValidationError
	switch {
	case errors.As(err, &validationErr):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  "invalid mission command",
			"errors": validationErr.Errors,
		})
	case errors.Is(err, orchestration.ErrOwnerRequired):
		writeError(w, http.StatusForbidden, "workspace owner permission is required")
	case errors.Is(err, orchestration.ErrCommandConflict),
		errors.Is(err, orchestration.ErrRevisionConflict),
		errors.Is(err, orchestration.ErrMissionNotStartable),
		errors.Is(err, orchestration.ErrMissionNotCancellable),
		errors.Is(err, orchestration.ErrMissionHasNoTasks),
		errors.Is(err, orchestration.ErrMissionHasNoReadyTasks),
		errors.Is(err, orchestration.ErrBudgetApprovalNotRequired),
		errors.Is(err, orchestration.ErrMissionNotDraft),
		errors.Is(err, orchestration.ErrMissionNotRetryable),
		errors.Is(err, orchestration.ErrTaskNodeNotRetryable),
		errors.Is(err, orchestration.ErrTaskRevisionConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "failed to create mission")
	}
}
