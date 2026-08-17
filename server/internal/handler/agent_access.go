package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/util"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// Agent invocation permission model (MUL-3963).
//
// Two distinct questions, previously conflated in canAccessPrivateAgent:
//
//   - "can this actor SEE / open this agent in the UI"  -> canAccessPrivateAgent
//   - "can this actor TRIGGER a run for this agent"      -> canInvokeAgent
//
// The invoke gate is the security-critical one: a workspace admin must NOT be
// able to invoke someone's private agent (and thereby use that owner's
// private integration credentials) just because they are an admin. Admin retains
// management + inventory visibility, not the ability to run.
//
// permission_mode drives invoke:
//   - private   -> only the agent owner may invoke; NO admin bypass, NO A2A bypass.
//   - public_to -> the agent_invocation_target allow-list decides:
//       * workspace target -> any workspace member (and workspace-internal
//         agent/system principals) may invoke.
//       * member target    -> only the specific user may invoke.
//       * team target       -> reserved, inert in V1.
//
// A2A is judged by the top-of-chain human originator, never by the immediate
// agent actor: if user U triggers agent A and A @-mentions agent B, B is only
// invocable when U (the originator) is in B's allow-list. This prevents agents
// from forming an alternate path that bypasses the owner's white-list.

// canInvokeAgent reports whether a run may be enqueued for `agent` on behalf of
// the given actor. Judgement is by the *effective invoking user*:
//   - member actor -> the member themselves (actorID)
//   - agent actor  -> the top-of-chain human originator (originatorUserID)
//   - system actor -> the originator when one was resolved, else no user
//
// originatorUserID is the empty string when no human could be attributed. For
// private agents that means "deny" (unless the actor is the owner). For
// public_to agents, a workspace target still admits workspace-internal
// agent/system principals, but member/team targets fail closed without a
// matching human.
func (h *Handler) canInvokeAgent(ctx context.Context, agent db.Agent, actorType, actorID, originatorUserID, workspaceID string) bool {
	effectiveUser := actorID
	if actorType != "member" {
		// agent / system: never trust the immediate principal, only the
		// resolved human originator at the top of the chain.
		effectiveUser = originatorUserID
	}

	// The agent owner may always invoke their own agent.
	if effectiveUser != "" && uuidToString(agent.OwnerID) == effectiveUser {
		return true
	}

	if agent.PermissionMode != "public_to" {
		// private (or any unknown mode) is deny-by-default: no admin bypass,
		// no A2A bypass. Only the owner branch above passes.
		return false
	}

	targets, err := h.Queries.ListAgentInvocationTargets(ctx, agent.ID)
	if err != nil {
		return false
	}

	// Agents and system triggers are workspace-internal principals: a
	// workspace target admits them even when no human originator resolved.
	// This is a DELIBERATE, product-approved exception (MUL-3963): webhook /
	// system / workspace-wide automation must be able to trigger a
	// `public_to workspace` agent even though there is no human at the top of
	// the chain. It is scoped tightly — it ONLY relaxes the *workspace* target.
	// member/team targets still require a resolved human originator to match,
	// so an unattributed agent/system trigger FAILS CLOSED against a
	// member-/team-scoped private-ish allow-list and can never smuggle itself
	// onto someone's specific-people grant.
	workspaceBroad := actorType == "agent" || actorType == "system"
	isWorkspaceMember := false
	if effectiveUser != "" {
		if _, err := h.getWorkspaceMember(ctx, effectiveUser, workspaceID); err == nil {
			isWorkspaceMember = true
		}
	}

	for _, t := range targets {
		switch t.TargetType {
		case "workspace":
			if isWorkspaceMember || workspaceBroad {
				return true
			}
		case "member":
			// Requires a resolved human. agent/system triggers with no
			// originator (effectiveUser == "") never match here — fail closed.
			if effectiveUser != "" && uuidToString(t.TargetID) == effectiveUser {
				return true
			}
		case "team":
			// Reserved: team membership does not exist yet in V1, so team
			// targets never admit anyone (also fail-closed for system/agent).
		}
	}
	return false
}

// canAccessPrivateAgent gates the VIEW surfaces (list/detail navigation, chat
// transcript read, task-cancel authorization). It is NOT the trigger gate —
// see canInvokeAgent for that.
//
// Rules:
//   - agent actors always pass (A2A collaboration + inspection preserved).
//   - the agent owner always passes.
//   - workspace owner/admin pass (governance / inventory visibility retained).
//   - a regular member passes for a public_to agent only when they hit a
//     workspace or member target; private agents stay owner+admin only.
func (h *Handler) canAccessPrivateAgent(ctx context.Context, agent db.Agent, actorType, actorID, workspaceID string) bool {
	if actorType == "agent" {
		return true
	}
	if uuidToString(agent.OwnerID) == actorID {
		return true
	}
	member, err := h.getWorkspaceMember(ctx, actorID, workspaceID)
	if err != nil {
		return false
	}
	if roleAllowed(member.Role, "owner", "admin") {
		return true
	}
	if agent.PermissionMode != "public_to" {
		return false
	}
	targets, err := h.Queries.ListAgentInvocationTargets(ctx, agent.ID)
	if err != nil {
		return false
	}
	return memberHitsInvocationTargets(targets, actorID)
}

// memberHitsInvocationTargets is the pure predicate deciding whether a regular
// member is on a public_to agent's allow-list, used by both the single-agent
// view gate and the ListAgents batch filter. A workspace target admits any
// member; a member target admits the matching user; team targets are inert.
func memberHitsInvocationTargets(targets []db.AgentInvocationTarget, userID string) bool {
	for _, t := range targets {
		switch t.TargetType {
		case "workspace":
			return true
		case "member":
			if uuidToString(t.TargetID) == userID {
				return true
			}
		}
	}
	return false
}

// memberAllowedToViewAgent is the ListAgents / aggregation filter predicate.
// Caller supplies the agent's already-batch-loaded invocation targets so the
// list endpoint avoids an N+1. Workspace owner/admin and the agent owner see
// everything; a regular member sees a public_to agent only when on its
// allow-list, and never sees other members' private agents.
func memberAllowedToViewAgent(agent db.Agent, targets []db.AgentInvocationTarget, userID, role string) bool {
	if roleAllowed(role, "owner", "admin") {
		return true
	}
	if uuidToString(agent.OwnerID) == userID {
		return true
	}
	if agent.PermissionMode != "public_to" {
		return false
	}
	return memberHitsInvocationTargets(targets, userID)
}

// invokeOriginatorFromRequest resolves the top-of-chain human user id for an
// invocation initiated over HTTP. Members are their own originator; agent
// actors inherit the originator from the task named by the X-Task-ID header
// (set by the CLI on every request), matching
// TaskService.resolveOriginatorFromTriggerComment. Returns "" when no human
// can be attributed — canInvokeAgent then fails closed for member/team targets.
func (h *Handler) invokeOriginatorFromRequest(r *http.Request, actorType, actorID string) string {
	if actorType == "member" {
		return actorID
	}
	if actorType == "agent" {
		if taskIDHeader := r.Header.Get("X-Task-ID"); taskIDHeader != "" {
			if taskUUID, err := util.ParseUUID(taskIDHeader); err == nil {
				if task, err := h.Queries.GetAgentTask(r.Context(), taskUUID); err == nil {
					return uuidToString(task.OriginatorUserID)
				}
			}
		}
	}
	return ""
}

// commentSourceTaskIDForIssue returns the agent's currently-executing task (from
// the X-Task-ID header) when it is running on the given issue, else an invalid
// UUID. This is the exact issue-scoped lineage CreateComment stamps onto
// source_task_id; a cross-issue (or missing) task yields invalid so the persisted
// lineage — and every authority/originator resolution that reads it — fails closed
// (MUL-4857).
func (h *Handler) commentSourceTaskIDForIssue(r *http.Request, issue db.Issue) pgtype.UUID {
	task, ok := h.taskFromRequestHeader(r)
	if !ok || !task.IssueID.Valid || uuidToString(task.IssueID) != uuidToString(issue.ID) {
		return pgtype.UUID{}
	}
	return task.ID
}

// taskFromRequestHeader resolves the agent's currently-executing task from the
// X-Task-ID header (set by the CLI on every request). Returns ok=false when the
// header is absent, malformed, or names no existing task.
func (h *Handler) taskFromRequestHeader(r *http.Request) (db.AgentTaskQueue, bool) {
	taskIDHeader := r.Header.Get("X-Task-ID")
	if taskIDHeader == "" {
		return db.AgentTaskQueue{}, false
	}
	taskUUID, err := util.ParseUUID(taskIDHeader)
	if err != nil {
		return db.AgentTaskQueue{}, false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil {
		return db.AgentTaskQueue{}, false
	}
	return task, true
}

// accessibleAgentIDs returns the set of agent IDs in the workspace the actor
// is allowed to see, for use by workspace-wide aggregation endpoints
// (run counts, activity histograms, task snapshots) that need to filter out
// private / non-allow-listed agents the member can't access. Returns nil and
// false on error.
func (h *Handler) accessibleAgentIDs(ctx context.Context, workspaceID, actorType, actorID, role string) (map[string]struct{}, bool) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, false
	}
	agents, err := h.Queries.ListAllAgents(ctx, wsUUID)
	if err != nil {
		return nil, false
	}
	targetsByAgent, ok := h.loadInvocationTargetsByAgent(ctx, agents)
	if !ok {
		return nil, false
	}
	allowed := make(map[string]struct{}, len(agents))
	for _, a := range agents {
		if actorType == "member" {
			if !memberAllowedToViewAgent(a, targetsByAgent[uuidToString(a.ID)], actorID, role) {
				continue
			}
		}
		allowed[uuidToString(a.ID)] = struct{}{}
	}
	return allowed, true
}

// restrictedAgentIDs returns the ids of agents that EXIST in the workspace but
// must not be named to this actor. Aggregation endpoints that cannot simply drop
// a row (the dashboard rollups, whose per-agent totals have to keep reconciling
// with the workspace-level series rendered beside them) use it to fold those
// agents onto a single sentinel instead.
//
// For a plain member, agents they may not view land in the set. Hard-deleted
// agents are deliberately NOT in the set: they are already absent
// from the agent table, have no visibility left to protect, and the dashboard
// renders them as their own "deleted agents" bucket — folding them in here
// would relabel a real deletion as a permission boundary.
//
// The per-agent invocation-target lookup is skipped for actors who see every
// agent (agent actors, workspace owner/admin).
func (h *Handler) restrictedAgentIDs(ctx context.Context, workspaceID, actorType, actorID, role string) (map[string]struct{}, bool) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, false
	}
	agents, err := h.Queries.ListAllAgents(ctx, wsUUID)
	if err != nil {
		return nil, false
	}

	// Whether per-agent visibility can restrict anything for this actor at all.
	judgeUserAgents := actorType == "member" && !roleAllowed(role, "owner", "admin")
	var targetsByAgent map[string][]db.AgentInvocationTarget
	if judgeUserAgents {
		var ok bool
		if targetsByAgent, ok = h.loadInvocationTargetsByAgent(ctx, agents); !ok {
			return nil, false
		}
	}

	restricted := make(map[string]struct{})
	for _, a := range agents {
		if !judgeUserAgents || memberAllowedToViewAgent(a, targetsByAgent[uuidToString(a.ID)], actorID, role) {
			continue
		}
		restricted[uuidToString(a.ID)] = struct{}{}
	}
	return restricted, true
}

// loadInvocationTargetsByAgent batch-loads invocation targets for a set of
// agents and buckets them by agent id string. Avoids the per-agent query the
// list / aggregation paths would otherwise incur.
func (h *Handler) loadInvocationTargetsByAgent(ctx context.Context, agents []db.Agent) (map[string][]db.AgentInvocationTarget, bool) {
	ids := make([]pgtype.UUID, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	out := make(map[string][]db.AgentInvocationTarget, len(agents))
	if len(ids) == 0 {
		return out, true
	}
	rows, err := h.Queries.ListAgentInvocationTargetsByAgentIDs(ctx, ids)
	if err != nil {
		return nil, false
	}
	for _, row := range rows {
		aid := uuidToString(row.AgentID)
		out[aid] = append(out[aid], row)
	}
	return out, true
}
