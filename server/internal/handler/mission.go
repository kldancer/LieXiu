package handler

import (
	"encoding/json"
	"errors"
	"fmt"
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

type RolePolicyBindingRequest struct {
	Duty       orchestration.Duty `json:"duty"`
	ProfileKey string             `json:"profile_key"`
	Version    int32              `json:"version"`
	AgentID    string             `json:"agent_id,omitempty"`
}

type StartMissionRequest struct {
	CommandID          string                     `json:"command_id"`
	ExpectedRevision   int64                      `json:"expected_revision"`
	RolePolicyBindings []RolePolicyBindingRequest `json:"role_policy_bindings"`
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

type ResolveHumanGateRequest struct {
	CommandID            string `json:"command_id"`
	ExpectedRevision     int64  `json:"expected_revision"`
	ExpectedTaskRevision int64  `json:"expected_task_revision"`
	ExpectedGateRevision int64  `json:"expected_gate_revision"`
	Resolution           string `json:"resolution"`
	Reason               string `json:"reason,omitempty"`
}

type ResolveHumanGateResponse struct {
	MissionID     string   `json:"mission_id"`
	TaskNodeID    string   `json:"task_node_id"`
	GateID        string   `json:"gate_id"`
	Status        string   `json:"status"`
	Revision      int64    `json:"revision"`
	TaskRevision  int64    `json:"task_revision"`
	GateRevision  int64    `json:"gate_revision"`
	CreatedRunIDs []string `json:"created_run_ids"`
	Replayed      bool     `json:"replayed"`
}

type RequestMissionPlanRequest struct {
	CommandID         string                                 `json:"command_id"`
	ExpectedRevision  int64                                  `json:"expected_revision"`
	Objective         string                                 `json:"objective"`
	ContextRefs       []orchestration.PlanProposalContextRef `json:"context_refs"`
	DeliveryCriteria  []string                               `json:"delivery_criteria"`
	RolePolicyBinding RolePolicyBindingRequest               `json:"role_policy_binding"`
}

type EditPlanProposalRequest struct {
	CommandID        string                     `json:"command_id"`
	ExpectedRevision int64                      `json:"expected_revision"`
	Proposal         orchestration.PlanProposal `json:"proposal"`
}

type RejectPlanProposalRequest struct {
	CommandID        string `json:"command_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type PlanProposalCommandResponse struct {
	MissionID  string `json:"mission_id"`
	Status     string `json:"status"`
	Revision   int64  `json:"revision"`
	ArtifactID string `json:"artifact_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	Replayed   bool   `json:"replayed"`
}

func (h *Handler) RequestMissionPlan(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, actorID, ok := h.missionPlanActorScope(w, r)
	if !ok {
		return
	}
	var req RequestMissionPlanRequest
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
	binding, ok := parseRolePolicyBindingRequest(w, req.RolePolicyBinding, "role_policy_binding")
	if !ok {
		return
	}
	result, err := h.Orchestration.RequestPlan(r.Context(), orchestration.RequestPlanCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CommandID: commandID, ActorID: actorID,
		ExpectedRevision:  req.ExpectedRevision,
		Input:             orchestration.PlanProposalInput{Objective: req.Objective, ContextRefs: req.ContextRefs, DeliveryCriteria: req.DeliveryCriteria},
		RolePolicyBinding: binding,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, PlanProposalCommandResponse{MissionID: uuidToString(result.Mission.IssueID), Status: result.Mission.Status, Revision: result.Mission.Revision, RunID: uuidToString(result.Run.ID), Replayed: result.Idempotent})
}

func (h *Handler) EditPlanProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, artifactID, actorID, ok := h.missionPlanArtifactScope(w, r)
	if !ok {
		return
	}
	var req EditPlanProposalRequest
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
	result, err := h.Orchestration.EditPlanProposal(r.Context(), orchestration.EditPlanProposalCommand{WorkspaceID: workspaceID, MissionID: missionID, ProposalArtifactID: artifactID, CommandID: commandID, ActorID: actorID, ExpectedRevision: req.ExpectedRevision, Proposal: req.Proposal})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PlanProposalCommandResponse{MissionID: uuidToString(result.Mission.IssueID), Status: result.Mission.Status, Revision: result.Mission.Revision, ArtifactID: uuidToString(result.Artifact.ID), RunID: uuidToString(result.Artifact.RunID), Replayed: result.Idempotent})
}

func (h *Handler) RejectPlanProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, artifactID, actorID, ok := h.missionPlanArtifactScope(w, r)
	if !ok {
		return
	}
	var req RejectPlanProposalRequest
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
	result, err := h.Orchestration.RejectPlanProposal(r.Context(), orchestration.RejectPlanProposalCommand{WorkspaceID: workspaceID, MissionID: missionID, ProposalArtifactID: artifactID, CommandID: commandID, ActorID: actorID, ExpectedRevision: req.ExpectedRevision, Reason: req.Reason})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PlanProposalCommandResponse{MissionID: uuidToString(result.Mission.IssueID), Status: result.Mission.Status, Revision: result.Mission.Revision, ArtifactID: uuidToString(result.Artifact.ID), RunID: uuidToString(result.Artifact.RunID), Replayed: result.Idempotent})
}

func (h *Handler) ApprovePlanProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID, missionID, artifactID, actorID, ok := h.missionPlanArtifactScope(w, r)
	if !ok {
		return
	}
	var req MissionLifecycleRequest
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
	result, err := h.Orchestration.ApprovePlanProposal(r.Context(), orchestration.SubmitPlanProposalCommand{WorkspaceID: workspaceID, MissionID: missionID, ProposalArtifactID: artifactID, CommandID: commandID, ActorID: actorID, ExpectedRevision: req.ExpectedRevision})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PlanProposalCommandResponse{MissionID: uuidToString(result.Mission.IssueID), Status: result.Mission.Status, Revision: result.Mission.Revision, ArtifactID: uuidToString(artifactID), Replayed: result.Idempotent})
}

func (h *Handler) missionPlanActorScope(w http.ResponseWriter, r *http.Request) (workspaceID, missionID, actorID pgtype.UUID, ok bool) {
	workspaceID, missionID, ok = h.missionReadScope(w, r)
	if !ok {
		return workspaceID, missionID, actorID, false
	}
	actorText, valid := requireUserID(w, r)
	if !valid {
		return workspaceID, missionID, actorID, false
	}
	actorID, valid = parseUUIDOrBadRequest(w, actorText, "actor_id")
	return workspaceID, missionID, actorID, valid
}

func parseRolePolicyBindingRequest(w http.ResponseWriter, request RolePolicyBindingRequest, path string) (orchestration.RolePolicyBinding, bool) {
	binding := orchestration.RolePolicyBinding{
		Duty: request.Duty, ProfileKey: request.ProfileKey, Version: request.Version,
	}
	if strings.TrimSpace(request.AgentID) == "" {
		return binding, true
	}
	agentID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(request.AgentID), path+".agent_id")
	if !ok {
		return orchestration.RolePolicyBinding{}, false
	}
	binding.AgentID = agentID
	return binding, true
}

func (h *Handler) missionPlanArtifactScope(w http.ResponseWriter, r *http.Request) (workspaceID, missionID, artifactID, actorID pgtype.UUID, ok bool) {
	workspaceID, missionID, actorID, ok = h.missionPlanActorScope(w, r)
	if !ok {
		return workspaceID, missionID, artifactID, actorID, false
	}
	artifactID, ok = parseUUIDOrBadRequest(w, chi.URLParam(r, "artifactID"), "proposal_artifact_id")
	return workspaceID, missionID, artifactID, actorID, ok
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
	workspaceID, missionID, actorID, ok := h.missionPlanActorScope(w, r)
	if !ok {
		return
	}
	var req StartMissionRequest
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
	bindings := make([]orchestration.RolePolicyBinding, 0, len(req.RolePolicyBindings))
	for index, item := range req.RolePolicyBindings {
		binding, valid := parseRolePolicyBindingRequest(w, item, fmt.Sprintf("role_policy_bindings[%d]", index))
		if !valid {
			return
		}
		bindings = append(bindings, binding)
	}
	result, err := h.Orchestration.StartMission(r.Context(), orchestration.StartMissionCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CommandID: commandID,
		ActorID: actorID, ExpectedRevision: req.ExpectedRevision, RolePolicyBindings: bindings,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	advance, err := h.Orchestration.AdvanceMission(r.Context(), orchestration.AdvanceMissionCommand{
		WorkspaceID: workspaceID, MissionID: missionID, CorrelationID: commandID,
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

func (h *Handler) ResolveHumanGate(w http.ResponseWriter, r *http.Request) {
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
	gateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "gateID"), "gate_id")
	if !ok {
		return
	}
	var req ResolveHumanGateRequest
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
	result, err := h.Orchestration.ResolveHumanGate(r.Context(), orchestration.ResolveHumanGateCommand{
		WorkspaceID: workspaceID, MissionID: missionID, GateID: gateID, CommandID: commandID, ActorID: actorID,
		ExpectedRevision: req.ExpectedRevision, ExpectedTaskRevision: req.ExpectedTaskRevision,
		ExpectedGateRevision: req.ExpectedGateRevision, Resolution: req.Resolution, Reason: req.Reason,
	})
	if err != nil {
		writeMissionCommandError(w, err)
		return
	}
	mission := result.Mission
	if result.Advance.Mission.IssueID.Valid {
		mission = result.Advance.Mission
	}
	runIDs := make([]string, 0, len(result.Advance.CreatedRuns))
	for _, run := range result.Advance.CreatedRuns {
		runIDs = append(runIDs, uuidToString(run.ID))
	}
	writeJSON(w, http.StatusAccepted, ResolveHumanGateResponse{
		MissionID: uuidToString(mission.IssueID), TaskNodeID: uuidToString(result.TaskNode.IssueID), GateID: uuidToString(result.Gate.ID),
		Status: mission.Status, Revision: mission.Revision, TaskRevision: result.TaskNode.Revision, GateRevision: result.Gate.Revision,
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
	var routingErr *orchestration.RoutingUnavailableError
	switch {
	case errors.As(err, &validationErr):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  "invalid mission command",
			"errors": validationErr.Errors,
		})
	case errors.As(err, &routingErr):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   routingErr.Error(),
			"duty":    routingErr.Duty,
			"routing": routingErr.Routing,
		})
	case errors.Is(err, orchestration.ErrOwnerRequired):
		writeError(w, http.StatusForbidden, "workspace owner permission is required")
	case errors.Is(err, orchestration.ErrCommandConflict),
		errors.Is(err, orchestration.ErrRevisionConflict),
		errors.Is(err, orchestration.ErrRolePolicyAlreadyFrozen),
		errors.Is(err, orchestration.ErrPlanProposalNotPending),
		errors.Is(err, orchestration.ErrMissionNotStartable),
		errors.Is(err, orchestration.ErrMissionNotCancellable),
		errors.Is(err, orchestration.ErrMissionHasNoTasks),
		errors.Is(err, orchestration.ErrMissionHasNoReadyTasks),
		errors.Is(err, orchestration.ErrBudgetApprovalNotRequired),
		errors.Is(err, orchestration.ErrMissionNotDraft),
		errors.Is(err, orchestration.ErrMissionNotRetryable),
		errors.Is(err, orchestration.ErrTaskNodeNotRetryable),
		errors.Is(err, orchestration.ErrTaskRevisionConflict),
		errors.Is(err, orchestration.ErrHumanGateNotPending),
		errors.Is(err, orchestration.ErrHumanGateRevisionConflict),
		errors.Is(err, orchestration.ErrHumanGateResolutionRequired):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "failed to create mission")
	}
}
