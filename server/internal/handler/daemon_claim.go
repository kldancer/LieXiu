package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/service"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

// claimBuildFailure captures a pre-response failure from
// buildClaimedTaskResponse (workspace isolation, retired historical kind, ...) so
// the per-runtime handler can render the exact status/message/outcome and the
// batch handler can skip the task. Any task cancellation is already performed
// inside the builder before it returns one.
type claimBuildFailure struct {
	outcome string
	status  int
	message string
}

// buildClaimedTaskResponse assembles the full daemon claim payload for a
// single already-claimed task and computes the exact comment ids embedded in
// it (deliveredCommentIDs). Shared by the per-runtime handler
// (ClaimTaskByRuntime) and the machine-level batch handler
// (ClaimTasksByRuntime, MUL-4257) so both build byte-identical payloads and
// feed the same delivery receipt into FinalizeTaskClaim. A non-nil failure
// means the task must not be dispatched; the builder has already cancelled it
// where the failure semantics require it.
func (h *Handler) buildClaimedTaskResponse(r *http.Request, task *db.AgentTaskQueue, runtime db.AgentRuntime, runtimeID, runtimeWorkspaceID string) (resp AgentTaskResponse, deliveredCommentIDs []pgtype.UUID, agentSkillCount, builtinSkillCount int, failure *claimBuildFailure) {
	// Build response with fresh agent data (name + skills + custom_env + custom_args).
	resp = taskToResponse(*task, runtimeWorkspaceID)
	supportsCoalescedComments := requestHasClientCapability(r, protocol.DaemonCapabilityCoalescedCommentsV1)
	// Empty-but-non-nil so pgx persists '{}' rather than NULL for tasks without
	// comment input. Comment tasks replace this with the ids actually embedded
	// in the capability-aware response built below.
	deliveredCommentIDs = []pgtype.UUID{}
	resp.ConnectedApps = parseRuntimeConnectedAppsForClaim(task.RuntimeConnectedApps, task.ID)
	if agent, err := h.Queries.GetAgent(r.Context(), task.AgentID); err == nil {
		useSkillRefs := requestHasClientCapability(r, protocol.DaemonCapabilitySkillBundlesV1)
		var customEnv map[string]string
		if agent.CustomEnv != nil {
			if err := json.Unmarshal(agent.CustomEnv, &customEnv); err != nil {
				slog.Warn("failed to unmarshal agent custom_env", "agent_id", uuidToString(agent.ID), "error", err)
			}
		}
		var customArgs []string
		if agent.CustomArgs != nil {
			if err := json.Unmarshal(agent.CustomArgs, &customArgs); err != nil {
				slog.Warn("failed to unmarshal agent custom_args", "agent_id", uuidToString(agent.ID), "error", err)
			}
		}
		var mcpConfig json.RawMessage
		if agent.McpConfig != nil {
			mcpConfig = json.RawMessage(agent.McpConfig)
		}
		// Layer a persisted provider-neutral per-task overlay on top of the
		// agent's saved mcp_config. This preserves historical tasks and the
		// explicit Runtime Adapter seam without coupling the control plane to a
		// connected-app vendor.
		if len(task.RuntimeMcpOverlay) > 0 {
			if merged, err := mergeMCPOverlay(mcpConfig, json.RawMessage(task.RuntimeMcpOverlay)); err != nil {
				slog.Warn("daemon claim: merge runtime_mcp_overlay failed; falling back to agent mcp_config", "task_id", uuidToString(task.ID), "error", err)
			} else {
				mcpConfig = merged
			}
		}
		// runtime_config is stored as JSONB and may legitimately be the
		// empty object `{}` for agents that haven't opted into any
		// provider-specific tuning. Forward only non-empty payloads so the
		// daemon's per-provider decoders treat absent-or-empty identically.
		var runtimeConfig json.RawMessage
		if rc := bytes.TrimSpace(agent.RuntimeConfig); len(rc) > 0 && !bytes.Equal(rc, []byte("{}")) && !bytes.Equal(rc, []byte("null")) {
			runtimeConfig = json.RawMessage(agent.RuntimeConfig)
		}
		resp.Agent = &TaskAgentData{
			ID:                    uuidToString(agent.ID),
			Name:                  agent.Name,
			Instructions:          agent.Instructions,
			CustomEnv:             customEnv,
			CustomArgs:            customArgs,
			McpConfig:             mcpConfig,
			Model:                 agent.Model.String,
			ThinkingLevel:         agent.ThinkingLevel.String,
			ServiceTier:           agent.ServiceTier.String,
			RuntimeConfig:         runtimeConfig,
			DisabledRuntimeSkills: disabledRuntimeSkillsFor(agent.DisabledRuntimeSkills, runtimeID, runtime.Provider),
		}
		if useSkillRefs {
			_, skillRefs := h.TaskService.LoadAgentSkillBundles(r.Context(), task.AgentID)
			agentSkillCount = len(skillRefs)
			resp.Agent.SkillRefs = skillRefs
		} else {
			skills := h.TaskService.LoadAgentSkills(r.Context(), task.AgentID)
			agentSkillCount = len(skills)
			builtinSkills := h.TaskService.BuiltinSkills()
			builtinSkillCount = len(builtinSkills)
			skills = append(skills, builtinSkills...)
			resp.Agent.Skills = skills
		}
	}

	// Resolve the runtime owner's profile description so the daemon can
	// inject "## Requesting User" into the brief. Empty fields short-circuit
	// the heading entirely on the daemon side; cloud / system runtimes with
	// no owner stay anonymous. Failure here must not block claim — the agent
	// can still run without the user-context section.
	if runtime.OwnerID.Valid {
		if owner, err := h.Queries.GetUser(r.Context(), runtime.OwnerID); err == nil {
			resp.RequestingUserName = owner.Name
			resp.RequestingUserProfileDescription = owner.ProfileDescription
		} else {
			slog.Debug("failed to load runtime owner for brief injection",
				"runtime_id", runtimeID,
				"owner_id", uuidToString(runtime.OwnerID),
				"error", err,
			)
		}
	}

	// Stored task initiator: chat tasks persist the real message sender at
	// enqueue time (web: request user; Lark: inbound sender — NOT the chat
	// session creator, which for Lark groups is the installer). When set, it is
	// the authoritative initiator for this run; resolve the live name/email so
	// the daemon can render `## Task Initiator`. Comment-triggered tasks instead
	// resolve their initiator from the triggering comment's author below; the
	// two paths are mutually exclusive (a task is either chat or issue-bound).
	// See MUL-2645.
	if task.InitiatorUserID.Valid {
		resp.InitiatorType = "member"
		resp.InitiatorID = uuidToString(task.InitiatorUserID)
		if u, err := h.Queries.GetUser(r.Context(), task.InitiatorUserID); err == nil {
			resp.InitiatorName = u.Name
			resp.InitiatorEmail = u.Email
		}
	}

	// Include workspace ID and repos so the daemon can set up worktrees.
	//
	// Repo precedence: project-bound github_repo resources override workspace
	// repos when present. Mixing both would just confuse the agent — if a
	// project explicitly attached its repos, those are the authoritative set
	// for issues inside that project. When the project has no github_repo
	// resources (or no project at all), we fall back to the workspace repos.
	if task.IssueID.Valid {
		if issue, err := h.Queries.GetIssue(r.Context(), task.IssueID); err == nil {
			resp.WorkspaceID = uuidToString(issue.WorkspaceID)
			resp.ThreadName = issue.Title

			var projectRepos []RepoData
			if issue.ProjectID.Valid {
				resp.ProjectID = uuidToString(issue.ProjectID)
				if proj, err := h.Queries.GetProject(r.Context(), issue.ProjectID); err == nil {
					resp.ProjectTitle = proj.Title
					resp.ProjectDescription = proj.Description.String
				}
				if rows := h.listProjectResourcesForProject(r.Context(), issue.ProjectID); len(rows) > 0 {
					out := make([]ProjectResourceData, 0, len(rows))
					for _, row := range rows {
						label := ""
						if row.Label.Valid {
							label = row.Label.String
						}
						ref := json.RawMessage(row.ResourceRef)
						if len(ref) == 0 {
							ref = json.RawMessage("{}")
						}
						out = append(out, ProjectResourceData{
							ID:           uuidToString(row.ID),
							ResourceType: row.ResourceType,
							ResourceRef:  ref,
							Label:        label,
						})
						// Lift github_repo resources into the daemon's repo list
						// so `liexiu repo checkout` and the meta-skill render
						// them as the issue's repos.
						if row.ResourceType == "github_repo" {
							var payload struct {
								URL string `json:"url"`
								Ref string `json:"ref,omitempty"`
							}
							if json.Unmarshal(row.ResourceRef, &payload) == nil && payload.URL != "" {
								projectRepos = append(projectRepos, RepoData{URL: payload.URL, Ref: strings.TrimSpace(payload.Ref)})
							}
						}
					}
					resp.ProjectResources = out
				}
			}

			if len(projectRepos) > 0 {
				resp.Repos = projectRepos
			} else if ws, err := h.Queries.GetWorkspace(r.Context(), issue.WorkspaceID); err == nil && ws.Repos != nil {
				var repos []RepoData
				if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
					resp.Repos = repos
				}
			}
		}

		// Load every planned input as one chronological, de-duplicated set.
		// The trigger is included here so the delivery receipt can only contain
		// comments whose body we successfully embedded. Missing/deleted rows are
		// intentionally absent and remain eligible for reconciliation. A stable
		// payload budget always keeps the primary trigger, then admits an oldest-
		// first prefix of additional comments; overflow is reconciled later.
		// Workspace-scoped load (MUL-4252) so a foreign comment UUID resolves to
		// "missing" instead of leaking another tenant's text into the prompt.
		plannedCommentIDs := append([]pgtype.UUID{}, task.CoalescedCommentIds...)
		if task.TriggerCommentID.Valid {
			plannedCommentIDs = append(plannedCommentIDs, task.TriggerCommentID)
		}
		loadedComments := h.buildCoalescedCommentData(r.Context(), runtime.WorkspaceID, plannedCommentIDs)
		triggerCommentID := uuidToString(task.TriggerCommentID)
		var deliveredComments []CoalescedCommentData
		triggerLoaded := false
		for _, comment := range loadedComments {
			if comment.ID == triggerCommentID {
				triggerLoaded = true
				break
			}
		}
		if task.TriggerCommentID.Valid && triggerLoaded {
			deliveredComments = selectCommentDelivery(
				loadedComments,
				triggerCommentID,
				!supportsCoalescedComments,
				maxClaimCommentPayloadBytes,
			)
		}
		// If the persisted trigger body cannot be loaded, fail closed on comment
		// coverage for this claim. The trigger snapshot CAS below also rejects a
		// concurrent edit/delete that changes the FK after this read.
		deliveredCommentIDs = commentDataIDs(deliveredComments)
		// taskToResponse exposes the enqueue plan to UI task-list callers. A
		// daemon claim must instead advertise only the structured ids actually
		// present in this payload, especially when the delivery budget truncates.
		resp.CoalescedCommentIDs = nil
		for _, comment := range deliveredComments {
			if comment.ID == triggerCommentID {
				// Populate the actual payload from the same successful read that
				// earned the receipt. The richer GetComment lookup below resolves
				// initiator ids and count hints, but a transient second-read failure
				// must never acknowledge a body that was not embedded.
				resp.TriggerCommentContent = comment.Content
				resp.TriggerThreadID = comment.ThreadID
				resp.TriggerAuthorType = comment.AuthorType
				resp.TriggerAuthorName = comment.AuthorName
				continue
			}
			resp.CoalescedCommentIDs = append(resp.CoalescedCommentIDs, comment.ID)
			resp.CoalescedComments = append(resp.CoalescedComments, comment)
		}

		// Fetch the triggering comment content so the daemon can embed it
		// directly in the agent prompt (prevents the agent from ignoring comments
		// when stale output files exist in a reused workdir). Also surface the
		// comment author's kind and display name so the agent knows whether it
		// was triggered by a human or by another agent — a signal used by the
		// harness instructions to avoid mention loops between agents.
		effectiveTriggerUUID := task.TriggerCommentID
		if effectiveTriggerUUID.Valid {
			// Scope by the runtime's workspace so a task row carrying a foreign
			// comment UUID can never pull another workspace's comment text into
			// this agent's prompt. The task's issue workspace is asserted equal
			// to runtime.WorkspaceID below, so this is the right tenant (MUL-4252).
			if comment, err := h.Queries.GetCommentInWorkspace(r.Context(), db.GetCommentInWorkspaceParams{
				ID:          effectiveTriggerUUID,
				WorkspaceID: runtime.WorkspaceID,
			}); err == nil {
				resp.TriggerCommentContent = comment.Content
				resp.TriggerThreadID = uuidToString(comment.ID)
				if comment.ParentID.Valid {
					resp.TriggerThreadID = uuidToString(comment.ParentID)
				}
				resp.TriggerAuthorType = comment.AuthorType
				// The triggering comment's author is the task initiator — the
				// real requester behind this run. Surface it (type + id + name,
				// plus email for members) so a workspace-visible agent can
				// attribute the request to the right person instead of to the
				// runtime owner. Same lookups as the display name above; we just
				// also capture the id and email. See MUL-2645.
				resp.InitiatorType = comment.AuthorType
				if comment.AuthorID.Valid {
					resp.InitiatorID = uuidToString(comment.AuthorID)
				}
				switch comment.AuthorType {
				case "agent":
					if comment.AuthorID.Valid {
						if a, err := h.Queries.GetAgent(r.Context(), comment.AuthorID); err == nil {
							resp.TriggerAuthorName = a.Name
							resp.InitiatorName = a.Name
						}
					}
				case "member":
					// For member-authored comments, AuthorID is a user UUID
					// (see handler.resolveActor) — look up the user's display name.
					if comment.AuthorID.Valid {
						if u, err := h.Queries.GetUser(r.Context(), comment.AuthorID); err == nil {
							resp.TriggerAuthorName = u.Name
							resp.InitiatorName = u.Name
							resp.InitiatorEmail = u.Email
						}
					}
				}
				// Count comments that arrived issue-wide since this agent's last
				// run, so the daemon can tell it the full catch-up volume up front
				// (the prompt then steers it to read the triggering thread first).
				// Anchor = the prior task's started_at (never completed_at: a long
				// run would miss comments posted while it ran). Cold start (no prior
				// task) → no anchor → no hint. Excludes the agent's own comments and
				// the triggering comment itself because that body is already
				// injected into the prompt. Best-effort: any DB error or zero count
				// leaves the hint suppressed.
				if startedAt, err := h.Queries.GetLastTaskStartedAtForIssueAndAgent(r.Context(), db.GetLastTaskStartedAtForIssueAndAgentParams{
					AgentID: task.AgentID,
					IssueID: comment.IssueID,
				}); err == nil && startedAt.Valid {
					if cnt, err := h.Queries.CountNewCommentsSince(r.Context(), db.CountNewCommentsSinceParams{
						AnchorID:    effectiveTriggerUUID,
						IssueID:     comment.IssueID,
						WorkspaceID: comment.WorkspaceID,
						Since:       startedAt,
						AuthorID:    task.AgentID,
					}); err == nil && cnt > 0 {
						resp.NewCommentCount = int(cnt)
						resp.NewCommentsSince = startedAt.Time.UTC().Format(time.RFC3339)
					}
				}
			}
		}

		if !supportsCoalescedComments {
			// Legacy daemons ignore the structured coalesced fields. Fold every
			// successfully loaded comment into the one trigger field they already
			// understand, then hide the structured fields to avoid duplicate prompt
			// sections in intermediate daemons that understand them but do not yet
			// advertise the capability.
			if len(resp.CoalescedComments) > 0 || (resp.TriggerCommentContent == "" && len(deliveredComments) > 0) {
				resp.TriggerCommentContent = formatLegacyCommentBundle(deliveredComments)
			}
			resp.CoalescedCommentIDs = nil
			resp.CoalescedComments = nil
		} else if resp.TriggerCommentContent == "" && len(deliveredComments) > 0 {
			// A deleted newest trigger must not suppress the structured earlier
			// comments: buildCommentPrompt renders them inside its trigger-content
			// branch. The missing id itself is not acknowledged in the receipt.
			resp.TriggerCommentContent = "The newest triggering comment is no longer available. Address every earlier comment included below."
		}

		// Resolve the prior agent session / workdir to resume.
		if task.RerunOfTaskID.Valid {
			// Manual retry: resume precisely from the source task the user
			// clicked, NOT the most-recent (agent, issue) row — a parallel task
			// on the same issue must never hijack the resume (MUL-4869). The
			// workdir is ALWAYS reused when it still exists; the session is
			// resumed only when the source failure did not poison the
			// conversation AND the source ran on this runtime.
			//
			// Resume-safety is computed HERE from the source task, not read off
			// task.ForceFreshSession: RerunIssue pins that flag to true so an OLD
			// claim handler mid rolling-deploy degrades to a clean start instead
			// of resuming a different execution via the (agent, issue) lookup.
			// service.ResumeUnsafeFailure mirrors GetLastTaskSession, including
			// its 400/invalid_request_error text defense for legacy /
			// mis-classified rows that the exact-source path would otherwise miss.
			//
			// When the source workdir is gone (GC'd), absent on this runtime, or
			// was never recorded (failed too early), execenv.Reuse falls back to a
			// fresh Prepare and gateResumeToReusedWorkdir drops the now-unusable
			// session — reuse is best-effort, never a silent swap onto a stale
			// directory. PriorWorkDir is offered regardless of runtime (a shared
			// mount may still resolve it); only the per-cwd session is
			// runtime-gated.
			if src, err := h.Queries.GetAgentTask(r.Context(), task.RerunOfTaskID); err == nil {
				if src.WorkDir.Valid {
					resp.PriorWorkDir = src.WorkDir.String
				}
				if !service.ResumeUnsafeFailure(src.FailureReason.String, src.Error.String) &&
					src.SessionID.Valid && src.RuntimeID == task.RuntimeID {
					resp.PriorSessionID = src.SessionID.String
				}
				// MUL-5305: if the source task withheld its Codex session because
				// the rollout was missing, this rerun has nothing resumable from it
				// — disclose the gap rather than silently starting fresh.
				if src.SessionRolloutMissing {
					resp.PriorSessionResumeUnavailable = true
				}
			}
		} else if !task.ForceFreshSession {
			// Non-rerun follow-up on the same issue: resume the most recent
			// (agent, issue) session so the agent keeps the issue's conversation
			// context across turns. The "Focus on THIS comment" guard in
			// prompt.go defends against inheriting the prior turn's "Done."
			// marker, and GetLastTaskSession already excludes poisoned sessions.
			if prior, err := h.Queries.GetLastTaskSession(r.Context(), db.GetLastTaskSessionParams{
				AgentID: task.AgentID,
				IssueID: task.IssueID,
			}); err == nil && prior.SessionID.Valid {
				if prior.RuntimeID == task.RuntimeID {
					resp.PriorSessionID = prior.SessionID.String
				}
				if prior.WorkDir.Valid {
					resp.PriorWorkDir = prior.WorkDir.String
				}
			}
			// MUL-5305: if the most recent terminal task withheld its Codex
			// session because the rollout was missing, GetLastTaskSession fell
			// back to an older session (or none). Disclose the continuity gap so
			// the next run tells the user the most recent turn's context could not
			// be carried over — even when that older session resumes cleanly,
			// which the resume-presence gate would otherwise pass silently.
			if missing, err := h.Queries.GetLatestTaskRolloutMissing(r.Context(), db.GetLatestTaskRolloutMissingParams{
				AgentID: task.AgentID,
				IssueID: task.IssueID,
			}); err == nil && missing {
				resp.PriorSessionResumeUnavailable = true
			}
		}
	}

	// Handoff note (MUL-3375) is populated by taskToResponse (the shared mapper
	// resp came from above), so the daemon's prompt + issue_context.md render the
	// assignment-handoff branch. Empty for all other task kinds.

	// Workspace isolation check: the daemon uses this response's workspace_id
	// as the only authority for LIEXIU_WORKSPACE_ID in the agent env. An
	// empty value would make the CLI silently fall back to the user-global
	// config and talk to whatever workspace the user happened to last
	// configure; a value that doesn't match the runtime's workspace means
	// upstream routed a foreign-workspace task here. Both cases must hard-
	// fail AND cancel the just-dispatched task so the queue / agent status
	// don't sit stuck until the stale-task sweeper fires minutes later.
	if resp.WorkspaceID == "" || resp.WorkspaceID != runtimeWorkspaceID {
		slog.Error("task claim: workspace isolation check failed, cancelling task",
			"task_id", uuidToString(task.ID),
			"runtime_id", runtimeID,
			"runtime_workspace", runtimeWorkspaceID,
			"resolved_workspace", resp.WorkspaceID,
			"has_issue", task.IssueID.Valid,
			"has_runtime", task.RuntimeID.Valid,
		)
		if _, cerr := h.TaskService.CancelTask(r.Context(), task.ID); cerr != nil {
			slog.Error("task claim: cancel after workspace check failed",
				"task_id", uuidToString(task.ID), "error", cerr)
		}
		return resp, deliveredCommentIDs, agentSkillCount, builtinSkillCount, &claimBuildFailure{
			outcome: "error_workspace",
			status:  http.StatusInternalServerError,
			message: "task workspace isolation check failed",
		}
	}

	// Surface a bounded snapshot of the same agent's other in-flight issue
	// tasks. Queued tasks cannot coordinate yet and are intentionally omitted.
	// This is advisory context, not a queue gate: cross-issue parallelism and
	// serial handoffs remain valid, while the prompt can stop an unaware second
	// run from opening a duplicate PR. Scope the query to the already-validated
	// runtime workspace so corrupt cross-tenant task links never leak.
	if siblings, err := h.Queries.ListActiveSiblingIssueTasks(r.Context(), db.ListActiveSiblingIssueTasksParams{
		AgentID:     task.AgentID,
		TaskID:      task.ID,
		WorkspaceID: parseUUID(resp.WorkspaceID),
	}); err == nil {
		resp.ActiveSiblingRuns = make([]ActiveSiblingRunData, 0, len(siblings))
		for _, sibling := range siblings {
			resp.ActiveSiblingRuns = append(resp.ActiveSiblingRuns, ActiveSiblingRunData{
				TaskID:          uuidToString(sibling.TaskID),
				IssueID:         uuidToString(sibling.IssueID),
				IssueIdentifier: fmt.Sprintf("%s-%d", sibling.IssuePrefix, sibling.IssueNumber),
				IssueTitle:      sibling.IssueTitle,
				Status:          sibling.Status,
				CreatedAt:       timestampToString(sibling.CreatedAt),
				StartedAt:       timestampToString(sibling.StartedAt),
			})
		}
	} else {
		slog.Warn("task claim: failed to load active sibling runs",
			"task_id", uuidToString(task.ID),
			"agent_id", uuidToString(task.AgentID),
			"error", err,
		)
	}

	// Workspace-level Context (workspace.context DB column) — the per-workspace
	// system prompt that workspace owners set in Settings → General. Inject it
	// into the brief regardless of task kind (issue / provider conversation /
	// quick-create) so every agent running in the workspace sees the same
	// shared context. Empty string when the owner hasn't set one; the daemon
	// skips rendering the heading in that case.
	if ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(resp.WorkspaceID)); err == nil {
		if ws.Context.Valid {
			resp.WorkspaceContext = ws.Context.String
		}
	} else {
		slog.Warn("task claim: failed to load workspace for context injection",
			"task_id", uuidToString(task.ID),
			"workspace_id", resp.WorkspaceID,
			"error", err,
		)
	}

	// Last gate before dispatch: refuse to hand a worktree-mode local_directory
	// task to a daemon that cannot implement the mode.
	//
	// The save-time gate in project_resource.go cannot cover this. It checks the
	// version at the moment the resource is written; a machine downgraded
	// afterwards still claims tasks, and an old daemon json-skips execution_mode
	// entirely — it would run the task IN PLACE, editing the working copy the
	// user asked to isolate. That is the exact outcome worktree mode exists to
	// prevent, so it fails closed here, against the version of the runtime that
	// is actually claiming.
	if reason := worktreeClaimBlockReason(
		resp.ProjectResources,
		runtime,
		requestHasClientCapability(r, protocol.DaemonCapabilityLocalWorktreeV1),
	); reason != "" {
		slog.Error("task claim: runtime too old for worktree mode; cancelling rather than running in place",
			"task_id", uuidToString(task.ID),
			"runtime_id", runtimeID,
			"daemon_id", runtime.DaemonID.String,
			"reason", reason,
		)
		// Cancel rather than leave it dispatched: the resource is pinned to this
		// daemon, so redelivery would hand it straight back to the same too-old
		// runtime forever.
		//
		// Cancel WITH the reason persisted on the row. The 4xx below only
		// reaches the daemon's log, and the batch-claim path drops the task from
		// its response entirely — so without this the user is left with a task
		// that says "cancelled" and nothing else, for a condition only they can
		// fix (update the app on that machine).
		//
		// Through the TaskService, not a raw query: cancellation has side
		// effects beyond the row — audit capture, agent status reconcile, the
		// task:cancelled broadcast that clears live cards, and
		// NotifyTaskFinished waking capacity/serial waiters. A direct query
		// leaves all of those stale.
		if _, cerr := h.TaskService.CancelTaskWithReason(r.Context(), task.ID, reason, "local_directory_error"); cerr != nil {
			// The cancel did not commit, so the row is still claimed. The
			// daemon is about to be refused, so left alone the task would
			// strand in dispatched until stale reclaim, with no visible
			// reason. Requeue it instead — the next claim re-runs this gate
			// and retries the cancel — and report a 5xx so the daemon reads
			// this as a transient server problem, not a claim refusal.
			slog.Error("task claim: cancel after worktree version gate failed; requeueing so the gate can run again",
				"task_id", uuidToString(task.ID), "error", cerr)
			if _, rerr := h.TaskService.RequeueTaskAfterClaimFailure(r.Context(), *task); rerr != nil {
				slog.Error("task claim: requeue after worktree-gate cancel failure failed; stale reclaim will recover it",
					"task_id", uuidToString(task.ID), "error", rerr)
			}
			return resp, deliveredCommentIDs, agentSkillCount, builtinSkillCount, &claimBuildFailure{
				outcome: "error_worktree_gate_cancel",
				status:  http.StatusInternalServerError,
				message: "failed to cancel a worktree task blocked by daemon version; task requeued",
			}
		}
		return resp, deliveredCommentIDs, agentSkillCount, builtinSkillCount, &claimBuildFailure{
			outcome: "error_worktree_daemon_version",
			status:  http.StatusUnprocessableEntity,
			message: reason,
		}
	}

	return resp, deliveredCommentIDs, agentSkillCount, builtinSkillCount, nil
}

// worktreeClaimBlockReason returns a user-facing reason when this runtime must
// not run the task, or "" when it may proceed.
//
// The decision is made on the CAPABILITY the daemon advertised on this very
// request, not on its version string. A daemon without the implementation does
// not merely lose concurrency — it json-skips execution_mode and runs the task
// in place, editing the working copy the user asked to isolate. Version strings
// could not answer that reliably: the git-describe dev-build exemption that
// keeps `make daemon` unblocked let a v0.4.23-era daemon straight through, and
// two tasks silently ran in the user's own directory (MUL-5707).
//
// Only resources bound to the claiming runtime's own daemon are considered: a
// project may carry one local_directory per machine, and another machine's
// worktree resource says nothing about this one's ability to run the task.
func worktreeClaimBlockReason(resources []ProjectResourceData, runtime db.AgentRuntime, hasWorktreeCapability bool) string {
	if !runtime.DaemonID.Valid || runtime.DaemonID.String == "" {
		return ""
	}
	if hasWorktreeCapability {
		return ""
	}
	for _, res := range resources {
		if res.ResourceType != "local_directory" {
			continue
		}
		var ref localDirectoryRef
		if err := json.Unmarshal(res.ResourceRef, &ref); err != nil {
			continue
		}
		if ref.ExecutionMode != localDirectoryModeWorktree || ref.DaemonID != runtime.DaemonID.String {
			continue
		}
		return fmt.Sprintf(
			"This machine's LieXiu runtime does not support parallel (worktree) mode, which %q is set to use. "+
				"Update the LieXiu app on that machine to the latest version, then re-run this task. "+
				"Refusing to run rather than falling back to editing the directory directly, which is what this mode exists to prevent.",
			ref.LocalPath)
	}
	return ""
}
