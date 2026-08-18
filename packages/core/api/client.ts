import type {
  Issue,
  CreateIssueRequest,
  MoveIssueRequest,
  UpdateIssueRequest,
  GroupedIssuesResponse,
  ListIssuesResponse,
  SearchIssuesResponse,
  SearchProjectsResponse,
  UpdateMeRequest,
  ListIssuesParams,
  ListGroupedIssuesParams,
  IssueTableFacetsRequest,
  IssueTableFacetsResponse,
  IssueTableGroupsRequest,
  IssueTableGroupsResponse,
  IssueTableRowsRequest,
  IssueTableRowsResponse,
  Agent,
  CreateAgentRequest,
  UpdateAgentRequest,
  AgentEnvResponse,
  UpdateAgentEnvRequest,
  AgentTask,
  AgentActivityBucket,
  AgentRunCount,
  WorkspaceWorkingAgent,
  WorkspaceWorkingAgentMineRelation,
  WorkspaceWorkingAgentType,
  AgentRuntime,
  RuntimeProfile,
  CreateRuntimeProfileRequest,
  UpdateRuntimeProfileRequest,
  Comment,
  CommentTriggerPreview,
  IssueTriggerPreview,
  IssueTriggerPreviewParams,
  Workspace,
  WorkspaceRepo,
  MemberWithUser,
  User,
  Skill,
  SkillSummary,
  CreateSkillRequest,
  UpdateSkillRequest,
  SetAgentSkillsRequest,
  SetAgentRuntimeSkillEnabledRequest,
  PersonalAccessToken,
  CreatePersonalAccessTokenRequest,
  CreatePersonalAccessTokenResponse,
  RuntimeUsage,
  IssueUsageSummary,
  RuntimeHourlyActivity,
  RuntimeUsageByAgent,
  RuntimeUsageByHour,
  DashboardUsageDaily,
  DashboardUsageByAgent,
  DashboardAgentRunTime,
  DashboardRunTimeDaily,
  DashboardFailureDaily,
  DashboardFailureByAgent,
  RuntimeUpdate,
  RuntimeModelListRequest,
  RuntimeLocalSkillListRequest,
  CreateRuntimeLocalSkillImportRequest,
  RuntimeLocalSkillImportRequest,
  TimelineEntry,
  AssigneeFrequencyEntry,
  TaskMessagePayload,
  Attachment,
  CancelTaskResponse,
  Project,
  CreateProjectRequest,
  UpdateProjectRequest,
  ListProjectsResponse,
  ProjectResource,
  CreateProjectResourceRequest,
  UpdateProjectResourceRequest,
  ListProjectResourcesResponse,
  Label,
  QuickAction,
  CreateQuickActionRequest,
  UpdateQuickActionRequest,
  ListQuickActionsResponse,
  CreateLabelRequest,
  UpdateLabelRequest,
  ListLabelsResponse,
  IssueLabelsResponse,
  LabelResourceType,
  ResourceLabelsResponse,
  GitHubPullRequest,
  ListGitHubInstallationsResponse,
  ListGitHubRepositoriesResponse,
  GitHubConnectResponse,
  ListVCSConnectionsResponse,
  ConnectVCSRequest,
  ConnectVCSResponse,
  BootstrapRequest,
  BootstrapResponse,
  BootstrapStatus,
  LocalSessionResponse,
} from "../types";
import type {
  ActivityPage,
  ApproveMissionBudgetRequest,
  MissionBudgetApprovalResponse,
  MissionLifecycleRequest,
  MissionLifecycleResponse,
  MissionProjection,
  RetryMissionTaskRequest,
  RetryMissionTaskResponse,
  RunDetailProjection,
  RequestPlanRequest, EditPlanProposalRequest, RejectPlanProposalRequest, PlanCommandResponse,
  RolePolicyBinding, RoleProfile, StartMissionRequest,
  ResolveHumanGateRequest, ResolveHumanGateResponse,
} from "../orchestration/types";
import type { ProjectCommandCenterProjection } from "../orchestration/project-center";
import { z } from "zod";
import {
  ActivityPageSchema,
  EMPTY_ACTIVITY_PAGE,
  EMPTY_MISSION_PROJECTION,
  EMPTY_RUN_DETAIL,
  MissionBudgetApprovalResponseSchema,
  MissionLifecycleResponseSchema,
  MissionProjectionSchema,
  RunDetailProjectionSchema,
  RetryMissionTaskResponseSchema,
  PlanCommandResponseSchema,
  RoleProfilesResponseSchema,
  ResolveHumanGateResponseSchema,
} from "../orchestration/schemas";
import { parseProjectCommandCenterProjection } from "../orchestration/project-center";
import type {
  CloudRuntimeNode,
  CreateCloudRuntimeNodeRequest,
  ListCloudRuntimeNodesParams,
} from "../runtimes/cloud-runtime";
import { type Logger, noopLogger } from "../logger";
import { createRequestId } from "../utils";
import { getCurrentSlug } from "../platform/workspace-storage";
import { parseWithFallback } from "./schema";

const QuickCreateMissionResponseSchema = z.object({
  mission_id: z.string().min(1),
  status: z.literal("ready"),
}).loose();

import {
  AgentTaskListSchema,
  AttachmentResponseSchema,
  ChildIssuesResponseSchema,
  CommentsListSchema,
  CommentTriggerPreviewSchema,
  IssueTriggerPreviewSchema,
  CloudRuntimeNodeListSchema,
  CloudRuntimeNodeSchema,
  DashboardAgentRunTimeListSchema,
  DashboardRunTimeDailyListSchema,
  DashboardFailureDailyListSchema,
  DashboardFailureByAgentListSchema,
  DashboardUsageByAgentListSchema,
  DashboardUsageDailyListSchema,
  EMPTY_APP_CONFIG,
  EMPTY_ATTACHMENT,
  EMPTY_CLOUD_RUNTIME_NODE,
  EMPTY_CLOUD_RUNTIME_NODE_LIST,
  EMPTY_GROUPED_ISSUES_RESPONSE,
  EMPTY_ISSUE_TABLE_FACETS_RESPONSE,
  EMPTY_ISSUE_TABLE_GROUPS_RESPONSE,
  EMPTY_ISSUE_TABLE_ROWS_RESPONSE,
  EMPTY_LIST_ISSUES_RESPONSE,
  EMPTY_SEARCH_ISSUES_RESPONSE,
  EMPTY_SEARCH_PROJECTS_RESPONSE,
  EMPTY_TIMELINE_ENTRIES,
  EMPTY_USER,
  BootstrapResponseSchema,
  LocalSessionResponseSchema,
  BootstrapStatusSchema,
  EMPTY_BOOTSTRAP_STATUS,
  AppConfigSchema,
  type AppConfigResponse,
  GroupedIssuesResponseSchema,
  IssueTableFacetsResponseSchema,
  IssueTableGroupsResponseSchema,
  IssueTableRowsResponseSchema,
  ListIssuesResponseSchema,
  CreateIssueResponseSchema,
  RuntimeHourlyActivityListSchema,
  RuntimeUsageByAgentListSchema,
  RuntimeUsageByHourListSchema,
  RuntimeUsageListSchema,
  SearchIssuesResponseSchema,
  SearchProjectsResponseSchema,
  TimelineEntriesSchema,
  UserSchema,
  LabelSchema,
  ListLabelsResponseSchema,
  QuickActionSchema,
  ListQuickActionsResponseSchema,
  QuickActionRenderSchema,
  EMPTY_QUICK_ACTION,
  EMPTY_LIST_QUICK_ACTIONS_RESPONSE,
  CommentSchema,
  EMPTY_COMMENT,
  EMPTY_ISSUE_PULL_REQUESTS_RESPONSE,
  IssuePullRequestsResponseSchema,
  ResourceLabelsResponseSchema,
  EMPTY_LABEL,
  EMPTY_LIST_LABELS_RESPONSE,
  EMPTY_RESOURCE_LABELS_RESPONSE,
  GitHubConnectResponseSchema,
  ListGitHubInstallationsResponseSchema,
  ListGitHubRepositoriesResponseSchema,
  EMPTY_GITHUB_CONNECT_RESPONSE,
  EMPTY_LIST_GITHUB_INSTALLATIONS_RESPONSE,
  EMPTY_LIST_GITHUB_REPOSITORIES_RESPONSE,
  RuntimeModelListRequestSchema,
  MALFORMED_RUNTIME_MODEL_LIST_REQUEST,
  SkillSchema,
  EMPTY_SKILL,
} from "./schemas";

/** Identifies the calling client to the server.
 *  Sent on every HTTP request as X-Client-Platform / X-Client-Version /
 *  X-Client-OS so the backend can log, gate, or split metrics by client.
 *  See server/internal/middleware/client.go for the receiving end. */
export interface ApiClientIdentity {
  /** Logical client kind. Server expects: "web" | "desktop" | "cli" | "daemon". */
  platform?: string;
  /** Client/app version string (e.g. "0.1.0", git tag, commit). */
  version?: string;
  /** Coarse operating-system bucket (for example "macos", "windows", or "linux"). */
  os?: string;
}

export interface ApiClientOptions {
  logger?: Logger;
  onUnauthorized?: () => void;
  /** Identifies the client to the server. Sent as X-Client-* headers. */
  identity?: ApiClientIdentity;
}

export interface ClientRuntimeSnapshot {
  probe_result: "success" | "error";
  runtime_count?: number;
  provider_summary?: Record<string, number>;
  online_count?: number;
  offline_count?: number;
}

export interface ClientUsageRequest {
  install_id: string;
  runtime?: ClientRuntimeSnapshot;
}

export class ApiError extends Error {
  readonly status: number;
  readonly statusText: string;
  // Raw decoded JSON body (when the server returned one). Carries structured
  // error fields like `code` so callers can branch on machine-readable
  // identifiers instead of pattern-matching the human-readable message.
  readonly body?: unknown;

  constructor(message: string, status: number, statusText: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.statusText = statusText;
    this.body = body;
  }
}

// errorCode extracts the stable `code` a handler attaches to a failure
// (writeErrorCode), so a caller can render its own localized sentence instead
// of toasting the server's English one. Returns undefined for a non-ApiError,
// or a server that did not send one — the caller then falls back to
// err.message, which is what every endpoint that has not adopted this yet
// produces.
export function errorCode(err: unknown): string | undefined {
  if (err instanceof ApiError && err.body && typeof err.body === "object") {
    const code = (err.body as { code?: unknown }).code;
    if (typeof code === "string" && code.length > 0) return code;
  }
  return undefined;
}

// dispatchReasonCode extracts the stable, machine-readable admission reason
// (MUL-4525) from a blocked-trigger error's structured body, when present. UI
// callers localize a blocked/partial trigger from this code instead of pattern
// matching the human-readable message. Returns undefined for non-ApiErrors or
// bodies without a reason_code (older servers), so callers fall back to their
// generic failure toast.
export function dispatchReasonCode(err: unknown): string | undefined {
  if (err instanceof ApiError && err.body && typeof err.body === "object") {
    const code = (err.body as { reason_code?: unknown }).reason_code;
    if (typeof code === "string" && code.length > 0) return code;
  }
  return undefined;
}

// Thrown by getAttachmentTextContent when the server refuses to inline a
// file because it exceeds the 2 MB cap. UI maps to a "too large, please
// download" affordance with the Download CTA still available.
export class PreviewTooLargeError extends Error {
  constructor() {
    super("attachment too large for inline preview");
    this.name = "PreviewTooLargeError";
  }
}

// Thrown by getAttachmentTextContent when the server's text whitelist
// rejects the content type. Normally the client's isPreviewable() guard
// catches this earlier, but the two whitelists can drift — surfacing the
// 415 as a typed error makes the drift visible.
export class PreviewUnsupportedError extends Error {
  constructor() {
    super("attachment type not supported for inline preview");
    this.name = "PreviewUnsupportedError";
  }
}

/**
 * Per-call override for the workspace a request targets.
 *
 * `authHeaders()` normally stamps `X-Workspace-Slug` from the global
 * current-workspace singleton, and the server resolves the workspace from that
 * header BEFORE any `workspace_id` query param. Anything that acts on a
 * workspace the user is not currently "in" — notably the create-workspace flow,
 * which provisions a workspace it has not navigated to yet — must say so
 * explicitly, or a concurrent writer of that singleton silently redirects the
 * write to the wrong workspace.
 */
function workspaceHeader(
  slug?: string,
): Record<string, string> | undefined {
  return slug ? { "X-Workspace-Slug": slug } : undefined;
}

function rolePolicyBindingToWire(binding: RolePolicyBinding) {
  return {
    duty: binding.duty,
    profile_key: binding.profileKey,
    version: binding.version,
    ...(binding.agentId ? { agent_id: binding.agentId } : {}),
  };
}

export class ApiClient {
  private baseUrl: string;
  private token: string | null = null;
  private logger: Logger;
  private options: ApiClientOptions;

  constructor(baseUrl: string, options?: ApiClientOptions) {
    this.baseUrl = baseUrl;
    this.options = options ?? {};
    this.logger = options?.logger ?? noopLogger;
  }

  getBaseUrl(): string {
    return this.baseUrl;
  }

  setToken(token: string | null) {
    this.token = token;
  }

  private readCsrfToken(): string | null {
    if (typeof document === "undefined") return null;
    const match = document.cookie
      .split("; ")
      .find((c) => c.startsWith("liexiu_csrf="));
    return match ? match.split("=")[1] ?? null : null;
  }

  private authHeaders(): Record<string, string> {
    const headers: Record<string, string> = {};
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    const slug = getCurrentSlug();
    if (slug) headers["X-Workspace-Slug"] = slug;
    const csrf = this.readCsrfToken();
    if (csrf) headers["X-CSRF-Token"] = csrf;
    const id = this.options.identity;
    if (id?.platform) headers["X-Client-Platform"] = id.platform;
    if (id?.version) headers["X-Client-Version"] = id.version;
    if (id?.os) headers["X-Client-OS"] = id.os;
    return headers;
  }

  private handleUnauthorized() {
    this.token = null;
    // Workspace id is owned by the URL-driven workspace-storage singleton
    // (set by [workspaceSlug]/layout.tsx). On 401, the auth flow navigates
    // to /login which leaves the workspace route, and the next workspace
    // entry will overwrite the id. No clear needed here.
    this.options.onUnauthorized?.();
  }

  private async parseErrorMessage(res: Response, fallback: string): Promise<string> {
    try {
      const data = await res.json() as { error?: string };
      if (typeof data.error === "string" && data.error) return data.error;
    } catch {
      // Ignore non-JSON error bodies.
    }
    return fallback;
  }

  // Reads the response body once for both human-readable error message and
  // structured fields. The Response stream can only be consumed once, so
  // both pieces have to come from a single read.
  private async parseErrorBody(res: Response, fallback: string): Promise<{ message: string; body: unknown }> {
    try {
      const data = await res.json() as { error?: string };
      const message = typeof data.error === "string" && data.error ? data.error : fallback;
      return { message, body: data };
    } catch {
      return { message: fallback, body: undefined };
    }
  }

  // Sends the request with the standard headers (auth, CSRF, request id,
  // client identity) and runs the shared error path (401 → handleUnauthorized,
  // structured ApiError, status-aware log level). Returns the raw Response so
  // callers can decide how to decode the body — JSON for the typed `fetch<T>`
  // path, plain text for the attachment-preview proxy, etc.
  private async fetchRaw(
    path: string,
    init?: RequestInit & { extraHeaders?: Record<string, string> },
  ): Promise<Response> {
    const rid = createRequestId();
    const start = Date.now();
    const method = init?.method ?? "GET";

    const headers: Record<string, string> = {
      "X-Request-ID": rid,
      ...this.authHeaders(),
      ...(init?.extraHeaders ?? {}),
      ...((init?.headers as Record<string, string>) ?? {}),
    };

    this.logger.info(`→ ${method} ${path}`, { rid });

    const res = await fetch(`${this.baseUrl}${path}`, {
      ...init,
      headers,
      credentials: "include",
    });

    if (!res.ok) {
      if (res.status === 401) this.handleUnauthorized();
      const { message, body } = await this.parseErrorBody(res, `API error: ${res.status} ${res.statusText}`);
      const logLevel = res.status === 404 ? "warn" : "error";
      this.logger[logLevel](`← ${res.status} ${path}`, { rid, duration: `${Date.now() - start}ms`, error: message });
      throw new ApiError(message, res.status, res.statusText, body);
    }

    this.logger.info(`← ${res.status} ${path}`, { rid, duration: `${Date.now() - start}ms` });
    return res;
  }

  private async fetch<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await this.fetchRaw(path, {
      ...init,
      extraHeaders: { "Content-Type": "application/json" },
    });
    // Handle 204 No Content
    if (res.status === 204) {
      return undefined as T;
    }
    return res.json() as Promise<T>;
  }

  // Auth
  async getBootstrapStatus(): Promise<BootstrapStatus> {
    try {
      const raw = await this.fetch<unknown>("/api/bootstrap/status");
      return parseWithFallback(raw, BootstrapStatusSchema, EMPTY_BOOTSTRAP_STATUS, {
        endpoint: "GET /api/bootstrap/status",
      });
    } catch (err) {
      // Older servers do not expose the local bootstrap endpoint. Treat that
      // as disabled; single-owner clients do not fall back to public signup.
      if (err instanceof ApiError && err.status === 404) {
        return EMPTY_BOOTSTRAP_STATUS;
      }
      throw err;
    }
  }

  async bootstrap(request: BootstrapRequest): Promise<BootstrapResponse> {
    const raw = await this.fetch<unknown>("/api/bootstrap", {
      method: "POST",
      body: JSON.stringify(request),
    });
    const response = parseWithFallback<BootstrapResponse | null>(
      raw,
      BootstrapResponseSchema,
      null,
      { endpoint: "POST /api/bootstrap" },
    );
    if (!response || !response.token || !response.user.id || !response.workspace.id) {
      throw new Error("Invalid bootstrap response");
    }
    return response;
  }

  async startLocalSession(): Promise<LocalSessionResponse> {
    const raw = await this.fetch<unknown>("/api/auth/local-session", {
      method: "POST",
    });
    const response = parseWithFallback<LocalSessionResponse | null>(
      raw,
      LocalSessionResponseSchema,
      null,
      { endpoint: "POST /api/auth/local-session" },
    );
    if (!response || !response.user.id || !response.workspace.id) {
      throw new Error("Invalid personal session response");
    }
    return response;
  }

  async logout(): Promise<void> {
    await this.fetch("/auth/logout", { method: "POST" });
  }

  async issueCliToken(): Promise<{ token: string }> {
    return this.fetch("/api/cli-token", { method: "POST" });
  }

  async getMe(): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me");
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "GET /api/me",
    });
  }

  async updateMe(data: UpdateMeRequest): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me", {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "PATCH /api/me",
    });
  }

  // Issues
  async listIssues(params?: ListIssuesParams): Promise<ListIssuesResponse> {
    const search = new URLSearchParams();
    if (params?.limit) search.set("limit", String(params.limit));
    if (params?.offset) search.set("offset", String(params.offset));
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.q?.trim()) search.set("q", params.q.trim());
    if (params?.status) search.set("status", params.status);
    if (params?.statuses?.length) search.set("statuses", params.statuses.join(","));
    if (params?.priority) search.set("priority", params.priority);
    if (params?.priorities?.length) search.set("priorities", params.priorities.join(","));
    if (params?.assignee_id) search.set("assignee_id", params.assignee_id);
    if (params?.assignee_ids?.length) search.set("assignee_ids", params.assignee_ids.join(","));
    if (params?.assignee_types?.length) search.set("assignee_types", params.assignee_types.join(","));
    if (params?.creator_id) search.set("creator_id", params.creator_id);
    if (params?.project_id) search.set("project_id", params.project_id);
    if (params?.assignee_filters?.length) {
      search.set("assignee_filters", params.assignee_filters.map((f) => `${f.type}:${f.id}`).join(","));
    }
    if (params?.include_no_assignee) search.set("include_no_assignee", "true");
    if (params?.creator_filters?.length) {
      search.set("creator_filters", params.creator_filters.map((f) => `${f.type}:${f.id}`).join(","));
    }
    if (params?.project_ids?.length) search.set("project_ids", params.project_ids.join(","));
    if (params?.include_no_project) search.set("include_no_project", "true");
    if (params?.label_ids?.length) search.set("label_ids", params.label_ids.join(","));
    if (params?.top_level_only) search.set("top_level_only", "true");
    // No `.length` guard on purpose: an empty ids array must still send
    // `ids=` — the server treats a PRESENT-but-empty list as an empty window
    // (nothing running), while an absent param means no restriction.
    if (params?.ids) search.set("ids", params.ids.join(","));
    if (params?.involves_user_id) search.set("involves_user_id", params.involves_user_id);
    if (params?.metadata && Object.keys(params.metadata).length > 0) {
      search.set("metadata", JSON.stringify(params.metadata));
    }
    if (params?.open_only) search.set("open_only", "true");
    if (params?.scheduled) search.set("scheduled", "true");
    if (params?.date_field) search.set("date_field", params.date_field);
    if (params?.date_start) search.set("date_start", params.date_start);
    if (params?.date_end) search.set("date_end", params.date_end);
    if (params?.sort_by) search.set("sort", params.sort_by);
    if (params?.sort_direction) search.set("direction", params.sort_direction);
    // An ids facet can carry hundreds of UUIDs (agents-working filter) —
    // enough to blow the ~8 KB request-line cap of common reverse proxies.
    // Route those windows through the POST twin, which takes the SAME
    // key/value pairs as a JSON body.
    if (params?.ids) {
      const raw = await this.fetch<unknown>("/api/issues/query", {
        method: "POST",
        body: JSON.stringify(Object.fromEntries(search)),
      });
      return parseWithFallback(raw, ListIssuesResponseSchema, EMPTY_LIST_ISSUES_RESPONSE, {
        endpoint: "POST /api/issues/query",
      });
    }
    const path = `/api/issues?${search}`;
    const raw = await this.fetch<unknown>(path);
    return parseWithFallback(raw, ListIssuesResponseSchema, EMPTY_LIST_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues",
    });
  }

  async listGroupedIssues(params: ListGroupedIssuesParams): Promise<GroupedIssuesResponse> {
    const search = new URLSearchParams({ group_by: params.group_by });
    if (params.limit) search.set("limit", String(params.limit));
    if (params.offset) search.set("offset", String(params.offset));
    if (params.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params.statuses?.length) search.set("statuses", params.statuses.join(","));
    if (params.priorities?.length) search.set("priorities", params.priorities.join(","));
    if (params.assignee_types?.length) search.set("assignee_types", params.assignee_types.join(","));
    if (params.assignee_id) search.set("assignee_id", params.assignee_id);
    if (params.assignee_ids?.length) search.set("assignee_ids", params.assignee_ids.join(","));
    if (params.creator_id) search.set("creator_id", params.creator_id);
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.involves_user_id) search.set("involves_user_id", params.involves_user_id);
    if (params.metadata && Object.keys(params.metadata).length > 0) {
      search.set("metadata", JSON.stringify(params.metadata));
    }
    if (params.assignee_filters?.length) {
      search.set("assignee_filters", params.assignee_filters.map((f) => `${f.type}:${f.id}`).join(","));
    }
    if (params.include_no_assignee) search.set("include_no_assignee", "true");
    if (params.creator_filters?.length) {
      search.set("creator_filters", params.creator_filters.map((f) => `${f.type}:${f.id}`).join(","));
    }
    if (params.project_ids?.length) search.set("project_ids", params.project_ids.join(","));
    if (params.include_no_project) search.set("include_no_project", "true");
    if (params.label_ids?.length) search.set("label_ids", params.label_ids.join(","));
    if (params.group_assignee_type) search.set("group_assignee_type", params.group_assignee_type);
    if (params.group_assignee_id) search.set("group_assignee_id", params.group_assignee_id);
    if (params.date_field) search.set("date_field", params.date_field);
    if (params.date_start) search.set("date_start", params.date_start);
    if (params.date_end) search.set("date_end", params.date_end);
    if (params.sort_by) search.set("sort", params.sort_by);
    if (params.sort_direction) search.set("direction", params.sort_direction);
    const raw = await this.fetch<unknown>(`/api/issues/grouped?${search}`);
    return parseWithFallback(raw, GroupedIssuesResponseSchema, EMPTY_GROUPED_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues/grouped",
    });
  }

  async listIssueTableGroups(params: IssueTableGroupsRequest): Promise<IssueTableGroupsResponse> {
    const raw = await this.fetch<unknown>("/api/issues/table/groups", {
      method: "POST",
      body: JSON.stringify(params),
    });
    return parseWithFallback(
      raw,
      IssueTableGroupsResponseSchema,
      EMPTY_ISSUE_TABLE_GROUPS_RESPONSE,
      { endpoint: "POST /api/issues/table/groups" },
    );
  }

  async listIssueTableRows(params: IssueTableRowsRequest): Promise<IssueTableRowsResponse> {
    const raw = await this.fetch<unknown>("/api/issues/table/rows", {
      method: "POST",
      body: JSON.stringify(params),
    });
    return parseWithFallback(
      raw,
      IssueTableRowsResponseSchema,
      EMPTY_ISSUE_TABLE_ROWS_RESPONSE,
      { endpoint: "POST /api/issues/table/rows" },
    );
  }

  async listIssueTableFacets(params: IssueTableFacetsRequest): Promise<IssueTableFacetsResponse> {
    const raw = await this.fetch<unknown>("/api/issues/table/facets", {
      method: "POST",
      body: JSON.stringify(params),
    });
    return parseWithFallback(
      raw,
      IssueTableFacetsResponseSchema,
      EMPTY_ISSUE_TABLE_FACETS_RESPONSE,
      { endpoint: "POST /api/issues/table/facets" },
    );
  }

  async searchIssues(params: { q: string; limit?: number; offset?: number; include_closed?: boolean; signal?: AbortSignal }): Promise<SearchIssuesResponse> {
    const search = new URLSearchParams({ q: params.q });
    if (params.limit !== undefined) search.set("limit", String(params.limit));
    if (params.offset !== undefined) search.set("offset", String(params.offset));
    if (params.include_closed) search.set("include_closed", "true");
    const raw = await this.fetch<unknown>(
      `/api/issues/search?${search}`,
      params.signal ? { signal: params.signal } : undefined,
    );
    return parseWithFallback(raw, SearchIssuesResponseSchema, EMPTY_SEARCH_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues/search",
    });
  }

  async searchProjects(params: { q: string; limit?: number; offset?: number; include_closed?: boolean; signal?: AbortSignal }): Promise<SearchProjectsResponse> {
    const search = new URLSearchParams({ q: params.q });
    if (params.limit !== undefined) search.set("limit", String(params.limit));
    if (params.offset !== undefined) search.set("offset", String(params.offset));
    if (params.include_closed) search.set("include_closed", "true");
    const raw = await this.fetch<unknown>(
      `/api/projects/search?${search}`,
      params.signal ? { signal: params.signal } : undefined,
    );
    return parseWithFallback(raw, SearchProjectsResponseSchema, EMPTY_SEARCH_PROJECTS_RESPONSE, {
      endpoint: "GET /api/projects/search",
    });
  }

  async getIssue(id: string): Promise<Issue> {
    return this.fetch(`/api/issues/${id}`);
  }

  async getMissionProjection(id: string): Promise<MissionProjection> {
    const raw = await this.fetch<unknown>(`/api/missions/${encodeURIComponent(id)}`);
    return parseWithFallback(raw, MissionProjectionSchema, EMPTY_MISSION_PROJECTION, {
      endpoint: "GET /api/missions/{id}",
    });
  }

  async listMissionActivities(id: string, afterSequence = 0, limit = 50): Promise<ActivityPage> {
    const params = new URLSearchParams({
      after_sequence: String(afterSequence),
      limit: String(limit),
    });
    const raw = await this.fetch<unknown>(`/api/missions/${encodeURIComponent(id)}/activities?${params}`);
    return parseWithFallback(raw, ActivityPageSchema, EMPTY_ACTIVITY_PAGE, {
      endpoint: "GET /api/missions/{id}/activities",
    });
  }

  async getMissionRunDetail(id: string, runId: string): Promise<RunDetailProjection> {
    const raw = await this.fetch<unknown>(
      `/api/missions/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`,
    );
    return parseWithFallback(raw, RunDetailProjectionSchema, EMPTY_RUN_DETAIL, {
      endpoint: "GET /api/missions/{id}/runs/{runID}",
    });
  }

  async approveMissionBudget(
    id: string,
    request: ApproveMissionBudgetRequest,
  ): Promise<MissionBudgetApprovalResponse> {
    const raw = await this.fetch<unknown>(
      `/api/missions/${encodeURIComponent(id)}/budget/approve`,
      {
        method: "POST",
        body: JSON.stringify({
          command_id: request.commandId,
          expected_revision: request.expectedRevision,
          grant_tokens: request.grantTokens,
          grant_cost_usd_ticks: request.grantCostUsdTicks,
          reason: request.reason,
        }),
      },
    );
    const result = parseWithFallback<MissionBudgetApprovalResponse | null>(
      raw,
      MissionBudgetApprovalResponseSchema,
      null,
      { endpoint: "POST /api/missions/{id}/budget/approve" },
    );
    if (!result) throw new Error();
    return result;
  }

  async startMission(id: string, request: StartMissionRequest): Promise<MissionLifecycleResponse> {
    return this.mutateMissionLifecycle(id, "start", request);
  }

  async cancelMission(id: string, request: MissionLifecycleRequest): Promise<MissionLifecycleResponse> {
    return this.mutateMissionLifecycle(id, "cancel", request);
  }

  private async mutateMissionLifecycle(
    id: string,
    action: "start" | "cancel",
    request: MissionLifecycleRequest | StartMissionRequest,
  ): Promise<MissionLifecycleResponse> {
    const raw = await this.fetch<unknown>(
      `/api/missions/${encodeURIComponent(id)}/${action}`,
      {
        method: "POST",
        body: JSON.stringify({
          command_id: request.commandId,
          expected_revision: request.expectedRevision,
          reason: request.reason,
          ...(action === "start" && "rolePolicyBindings" in request
            ? { role_policy_bindings: request.rolePolicyBindings.map(rolePolicyBindingToWire) }
            : {}),
        }),
      },
    );
    const result = parseWithFallback<MissionLifecycleResponse | null>(
      raw,
      MissionLifecycleResponseSchema,
      null,
      { endpoint: `POST /api/missions/{id}/${action}` },
    );
    if (!result) throw new Error();
    return result;
  }

  async retryMissionTask(
    id: string,
    request: RetryMissionTaskRequest,
  ): Promise<RetryMissionTaskResponse> {
    const raw = await this.fetch<unknown>(
      `/api/missions/${encodeURIComponent(id)}/tasks/${encodeURIComponent(request.taskNodeId)}/retry`,
      {
        method: "POST",
        body: JSON.stringify({
          command_id: request.commandId,
          expected_revision: request.expectedRevision,
          expected_task_revision: request.expectedTaskRevision,
          reason: request.reason,
        }),
      },
    );
    const result = parseWithFallback<RetryMissionTaskResponse | null>(
      raw,
      RetryMissionTaskResponseSchema,
      null,
      { endpoint: "POST /api/missions/{id}/tasks/{taskNodeID}/retry" },
    );
    if (!result) throw new Error();
    return result;
  }

  async resolveHumanGate(id: string, gateId: string, request: ResolveHumanGateRequest): Promise<ResolveHumanGateResponse> {
    const raw = await this.fetch<unknown>(
      `/api/missions/${encodeURIComponent(id)}/human-gates/${encodeURIComponent(gateId)}/resolve`,
      { method: "POST", body: JSON.stringify({
        command_id: request.commandId, expected_revision: request.expectedRevision,
        expected_task_revision: request.expectedTaskRevision, expected_gate_revision: request.expectedGateRevision,
        resolution: request.resolution, reason: request.reason,
      }) },
    );
    const result = parseWithFallback<ResolveHumanGateResponse | null>(raw, ResolveHumanGateResponseSchema, null, {
      endpoint: "POST /api/missions/{id}/human-gates/{gateId}/resolve",
    });
    if (!result) throw new Error();
    return result;
  }

  async requestPlan(id: string, request: RequestPlanRequest): Promise<PlanCommandResponse> {
    return this.planCommand(id, "plan/request", {
      command_id: request.commandId,
      expected_revision: request.expectedRevision,
      objective: request.objective,
      context_refs: request.contextRefs,
      delivery_criteria: request.deliveryCriteria,
      role_policy_binding: rolePolicyBindingToWire(request.rolePolicyBinding),
    });
  }

  async listRoleProfiles(workspaceId: string): Promise<RoleProfile[]> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${encodeURIComponent(workspaceId)}/role-profiles`);
    const result = parseWithFallback<RoleProfile[] | null>(raw, RoleProfilesResponseSchema, null, {
      endpoint: "GET /api/workspaces/{workspace_id}/role-profiles",
    });
    return result ?? [];
  }

  async editPlanProposal(id: string, artifactId: string, request: EditPlanProposalRequest): Promise<PlanCommandResponse> {
    return this.planCommand(id, `plan-proposals/${encodeURIComponent(artifactId)}/edit`, {
      command_id: request.commandId,
      expected_revision: request.expectedRevision,
      proposal: request.proposal,
    });
  }

  async rejectPlanProposal(id: string, artifactId: string, request: RejectPlanProposalRequest): Promise<PlanCommandResponse> {
    return this.planCommand(id, `plan-proposals/${encodeURIComponent(artifactId)}/reject`, {
      command_id: request.commandId,
      expected_revision: request.expectedRevision,
      reason: request.reason,
    });
  }

  async approvePlanProposal(id: string, artifactId: string, request: { commandId: string; expectedRevision: number }): Promise<PlanCommandResponse> {
    return this.planCommand(id, `plan-proposals/${encodeURIComponent(artifactId)}/approve`, {
      command_id: request.commandId,
      expected_revision: request.expectedRevision,
    });
  }

  private async planCommand(id: string, action: string, body: unknown): Promise<PlanCommandResponse> {
    const raw = await this.fetch<unknown>(`/api/missions/${encodeURIComponent(id)}/${action}`, {
      method: "POST",
      body: JSON.stringify(body),
    });
    const result = parseWithFallback<PlanCommandResponse | null>(raw, PlanCommandResponseSchema, null, {
      endpoint: `POST /api/missions/{id}/${action}`,
    });
    if (!result) throw new Error("invalid plan command response");
    return result;
  }

  async createIssue(data: CreateIssueRequest): Promise<Issue> {
    // Parse through a schema (not a raw cast): the create modal keys its
    // label-attach compatibility fallback off `labels` being absent vs a
    // validated Label[], so an unvalidated wrong shape must not slip through.
    // Unlike list endpoints, a create that returns an unusable body is a
    // FAILED mutation, not a safe-empty read: fall back to null and reject so
    // the modal keeps the draft and shows a failure toast instead of a blank
    // "created" card pointing at an empty issue id. parseWithFallback already
    // logged the schema issues + raw payload; the empty message lets the modal
    // render its localized "failed to create" toast.
    const raw = await this.fetch<unknown>("/api/issues", {
      method: "POST",
      body: JSON.stringify(data),
    });
    const issue = parseWithFallback<Issue | null>(raw, CreateIssueResponseSchema, null, {
      endpoint: "POST /api/issues",
    });
    if (!issue) {
      throw new Error();
    }
    return issue;
  }

  async quickCreateIssue(data: {
    command_id: string;
    prompt: string;
    project_id?: string | null;
  }): Promise<{ mission_id: string; status: "ready" }> {
    const raw = await this.fetch<unknown>("/api/issues/quick-create", {
      method: "POST",
      body: JSON.stringify(data),
    });
    const mission = parseWithFallback<{ mission_id: string; status: "ready" } | null>(
      raw,
      QuickCreateMissionResponseSchema,
      null,
      { endpoint: "POST /api/issues/quick-create" },
    );
    if (!mission) {
      throw new Error();
    }
    return mission;
  }

  async upsertClientUsage(data: ClientUsageRequest): Promise<void> {
    await this.fetch("/api/client-usage", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateIssue(id: string, data: UpdateIssueRequest): Promise<Issue> {
    return this.fetch(`/api/issues/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async moveIssue(id: string, data: MoveIssueRequest): Promise<Issue> {
    return this.fetch(`/api/issues/${id}/move`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async listChildIssues(id: string): Promise<{ issues: Issue[] }> {
    const raw = await this.fetch<unknown>(`/api/issues/${id}/children`);
    return parseWithFallback(raw, ChildIssuesResponseSchema, { issues: [] }, {
      endpoint: "GET /api/issues/:id/children",
    });
  }

  /** Batched variant — returns children for multiple parents in one request.
   *  Avoids an N-request fan-out in Swimlane (one per visible parent lane).
   *  parentIds must be non-empty; pass a sorted, deduplicated list so the
   *  React Query cache key is stable across renders. */
  async listChildrenByParents(parentIds: string[]): Promise<{ issues: Issue[] }> {
    const raw = await this.fetch<unknown>(
      `/api/issues/children?parent_ids=${parentIds.join(",")}`,
    );
    return parseWithFallback(raw, ChildIssuesResponseSchema, { issues: [] }, {
      endpoint: "GET /api/issues/children",
    });
  }

  async getChildIssueProgress(): Promise<{ progress: { parent_issue_id: string; total: number; done: number }[] }> {
    return this.fetch("/api/issues/child-progress");
  }

  async deleteIssue(id: string): Promise<void> {
    await this.fetch(`/api/issues/${id}`, { method: "DELETE" });
  }

  async batchUpdateIssues(issueIds: string[], updates: UpdateIssueRequest): Promise<{ updated: number }> {
    return this.fetch("/api/issues/batch-update", {
      method: "POST",
      body: JSON.stringify({ issue_ids: issueIds, updates }),
    });
  }

  async batchDeleteIssues(issueIds: string[]): Promise<{ deleted: number }> {
    return this.fetch("/api/issues/batch-delete", {
      method: "POST",
      body: JSON.stringify({ issue_ids: issueIds }),
    });
  }

  // Comments
  async listComments(issueId: string): Promise<Comment[]> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/comments`);
    return parseWithFallback(raw, CommentsListSchema, [], {
      endpoint: "GET /api/issues/:id/comments",
    });
  }

  async createComment(
    issueId: string,
    content: string,
    type?: string,
    parentId?: string,
    attachmentIds?: string[],
    suppressAgentIds?: string[],
  ): Promise<Comment> {
    return this.fetch(`/api/issues/${issueId}/comments`, {
      method: "POST",
      body: JSON.stringify({
        content,
        type: type ?? "comment",
        ...(parentId ? { parent_id: parentId } : {}),
        ...(attachmentIds?.length ? { attachment_ids: attachmentIds } : {}),
        ...(suppressAgentIds?.length ? { suppress_agent_ids: suppressAgentIds } : {}),
      }),
    });
  }

  async previewCommentTriggers(issueId: string, content: string, parentId?: string, editingCommentId?: string): Promise<CommentTriggerPreview> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/comments/trigger-preview`, {
      method: "POST",
      body: JSON.stringify({
        content,
        ...(parentId ? { parent_id: parentId } : {}),
        ...(editingCommentId ? { editing_comment_id: editingCommentId } : {}),
      }),
    });
    return parseWithFallback(raw, CommentTriggerPreviewSchema, { agents: [] }, {
      endpoint: "POST /api/issues/:id/comments/trigger-preview",
    });
  }

  /** Dry-run the unified run-enqueue predicate for a prospective issue write
   *  (create / single assign / single status / batch). Returns the runs that
   *  would start; no side effect. The four entry points consult this instead
   *  of re-implementing the rule (MUL-3375). */
  async previewIssueTrigger(params: IssueTriggerPreviewParams): Promise<IssueTriggerPreview> {
    const raw = await this.fetch<unknown>("/api/issues/preview-trigger", {
      method: "POST",
      body: JSON.stringify({
        ...(params.issueIds?.length ? { issue_ids: params.issueIds } : {}),
        ...(params.isCreate ? { is_create: true } : {}),
        ...(params.assigneeType ? { assignee_type: params.assigneeType } : {}),
        ...(params.assigneeId ? { assignee_id: params.assigneeId } : {}),
        ...(params.status ? { status: params.status } : {}),
      }),
    });
    return parseWithFallback(raw, IssueTriggerPreviewSchema, { triggers: [], total_count: 0 }, {
      endpoint: "POST /api/issues/preview-trigger",
    });
  }

  async listTimeline(issueId: string): Promise<TimelineEntry[]> {
    const raw = await this.fetch<unknown>(
      `/api/issues/${issueId}/timeline`,
    );
    return parseWithFallback(raw, TimelineEntriesSchema, EMPTY_TIMELINE_ENTRIES, {
      endpoint: "GET /api/issues/:id/timeline",
    });
  }

  async getAssigneeFrequency(): Promise<AssigneeFrequencyEntry[]> {
    return this.fetch("/api/assignee-frequency");
  }

  async updateComment(commentId: string, content: string, attachmentIds?: string[], suppressAgentIds?: string[]): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}`, {
      method: "PUT",
      body: JSON.stringify({
        content,
        attachment_ids: attachmentIds,
        ...(suppressAgentIds?.length ? { suppress_agent_ids: suppressAgentIds } : {}),
      }),
    });
  }

  async deleteComment(commentId: string): Promise<void> {
    await this.fetch(`/api/comments/${commentId}`, { method: "DELETE" });
  }

  async resolveComment(commentId: string): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}/resolve`, { method: "POST" });
  }

  async unresolveComment(commentId: string): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}/resolve`, { method: "DELETE" });
  }

  // Agents
  async listAgents(params?: { workspace_id?: string; include_archived?: boolean }): Promise<Agent[]> {
    const search = new URLSearchParams();
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.include_archived) search.set("include_archived", "true");
    return this.fetch(`/api/agents?${search}`);
  }

  async getAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/agents/${id}`);
  }

  async createAgent(data: CreateAgentRequest): Promise<Agent> {
    return this.fetch("/api/agents", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateAgent(id: string, data: UpdateAgentRequest): Promise<Agent> {
    return this.fetch(`/api/agents/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async archiveAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/agents/${id}/archive`, { method: "POST" });
  }

  /**
   * Returns the plaintext `custom_env` map for an agent. Admits the
   * agent's owner or a workspace owner/admin (MUL-5438); calls from
   * agent-actor sessions get a 403. Every successful call writes an
   * `agent_env_revealed` activity_log row server-side. MUL-2600.
   */
  async getAgentEnv(id: string): Promise<AgentEnvResponse> {
    return this.fetch(`/api/agents/${id}/env`);
  }

  /**
   * Replaces an agent's `custom_env` wholesale. Values equal to
   * `"****"` are preserved server-side (the **** guard) so a partial
   * UI edit doesn't overwrite real secrets with the masked
   * placeholder. Admits the agent's owner or a workspace owner/admin
   * (MUL-5438); agent actors get a 403. Every successful call writes an
   * `agent_env_updated` activity_log row. MUL-2600.
   */
  async updateAgentEnv(id: string, data: UpdateAgentEnvRequest): Promise<AgentEnvResponse> {
    return this.fetch(`/api/agents/${id}/env`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async restoreAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/agents/${id}/restore`, { method: "POST" });
  }

  // Bulk-cancel every active task (queued/dispatched/running) for the agent.
  // Permission: agent owner or workspace admin/owner. Server returns the
  // count of cancelled rows; broadcasts task:cancelled for each so other
  // surfaces can clear their live cards.
  async cancelAgentTasks(id: string): Promise<{ cancelled: number }> {
    return this.fetch(`/api/agents/${id}/cancel-tasks`, { method: "POST" });
  }

  async listRuntimes(
    params?: { workspace_id?: string; owner?: "me" },
    workspaceSlug?: string,
  ): Promise<AgentRuntime[]> {
    const search = new URLSearchParams();
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.owner) search.set("owner", params.owner);
    // workspace_id alone is not enough: the server resolves the workspace from
    // the slug header first, so a caller listing another workspace's runtimes
    // must override the header too.
    return this.fetch(`/api/runtimes?${search}`, {
      headers: workspaceHeader(workspaceSlug),
    });
  }

  async listCloudRuntimeNodes(
    params?: ListCloudRuntimeNodesParams,
  ): Promise<CloudRuntimeNode[]> {
    const search = new URLSearchParams();
    if (params?.limit !== undefined) search.set("limit", String(params.limit));
    if (params?.offset !== undefined) search.set("offset", String(params.offset));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-runtime/nodes${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      CloudRuntimeNodeListSchema,
      EMPTY_CLOUD_RUNTIME_NODE_LIST,
      { endpoint: "GET /api/cloud-runtime/nodes" },
    );
  }

  async createCloudRuntimeNode(
    data: CreateCloudRuntimeNodeRequest,
  ): Promise<CloudRuntimeNode> {
    const res = await this.fetchRaw("/api/cloud-runtime/nodes", {
      method: "POST",
      body: JSON.stringify(data),
      extraHeaders: { "Content-Type": "application/json" },
    });
    const raw = await res.json() as unknown;
    return parseWithFallback(
      raw,
      CloudRuntimeNodeSchema,
      EMPTY_CLOUD_RUNTIME_NODE,
      { endpoint: "POST /api/cloud-runtime/nodes" },
    );
  }

  async deleteCloudRuntimeNode(instanceId: string): Promise<void> {
    await this.fetchRaw("/api/cloud-runtime/nodes", {
      method: "DELETE",
      body: JSON.stringify({ instance_id: instanceId }),
      extraHeaders: { "Content-Type": "application/json" },
    });
  }

  async deleteRuntime(runtimeId: string): Promise<void> {
    await this.fetch(`/api/runtimes/${runtimeId}`, { method: "DELETE" });
  }

  // Confirmed variant of deleteRuntime. The strict DELETE refuses with
  // structured 409 (`code: "runtime_has_active_agents"`, body carries the
  // blocking agents) when active agents are bound; the front-end then opens
  // the confirmation dialog and submits the user-confirmed active agent set
  // here. Server compares the snapshot to the live set inside the transaction
  // and refuses with `code: "runtime_delete_plan_changed"` (same shape, fresh
  // `active_agents`) if they don't match — caller should re-render the agent
  // list and force the user to re-confirm.
  //
  // The agents are UNBOUND, not archived or deleted (MUL-5559): they keep their
  // configuration, chats and task history and need a new runtime to run again.
  // `agents_archived` is the server's deprecated mirror of `agents_unbound`,
  // kept because installed clients read it; prefer `agents_unbound`.
  async unbindAgentsAndDeleteRuntime(
    runtimeId: string,
    expectedActiveAgentIds: string[],
  ): Promise<{
    status: string;
    agents_unbound?: number;
    agents_archived?: number;
    tasks_cancelled: number;
  }> {
    return this.fetch(`/api/runtimes/${runtimeId}/unbind-agents-and-delete`, {
      method: "POST",
      body: JSON.stringify({ expected_active_agent_ids: expectedActiveAgentIds }),
    });
  }

  async updateRuntime(
    runtimeId: string,
    patch: {
      visibility?: "private" | "public";
      /**
       * Custom display name. Pass an empty string to clear it (the server
       * reverts to the default name). Omit to leave it unchanged — a JSON
       * `null` is treated as "unchanged", not "clear". See MUL-4217.
       */
      custom_name?: string;
      /** Apply custom_name to every runtime on the same machine. */
      apply_to_machine?: boolean;
    },
  ): Promise<AgentRuntime> {
    return this.fetch(`/api/runtimes/${runtimeId}`, {
      method: "PATCH",
      body: JSON.stringify(patch),
    });
  }

  // ---------------------------------------------------------------------
  // Custom runtime profiles (MUL-3284). All workspace-scoped: the caller
  // passes the workspace id the same way the runtimes list resolves it.
  // ---------------------------------------------------------------------

  async listRuntimeProfiles(workspaceId: string): Promise<RuntimeProfile[]> {
    const res = await this.fetch<{ runtime_profiles?: RuntimeProfile[] }>(
      `/api/workspaces/${workspaceId}/runtime-profiles`,
    );
    return res.runtime_profiles ?? [];
  }

  async getRuntimeProfile(
    workspaceId: string,
    profileId: string,
  ): Promise<RuntimeProfile> {
    return this.fetch(
      `/api/workspaces/${workspaceId}/runtime-profiles/${profileId}`,
    );
  }

  async createRuntimeProfile(
    workspaceId: string,
    body: CreateRuntimeProfileRequest,
  ): Promise<RuntimeProfile> {
    return this.fetch(`/api/workspaces/${workspaceId}/runtime-profiles`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  async updateRuntimeProfile(
    workspaceId: string,
    profileId: string,
    patch: UpdateRuntimeProfileRequest,
  ): Promise<RuntimeProfile> {
    return this.fetch(
      `/api/workspaces/${workspaceId}/runtime-profiles/${profileId}`,
      {
        method: "PATCH",
        body: JSON.stringify(patch),
      },
    );
  }

  async deleteRuntimeProfile(
    workspaceId: string,
    profileId: string,
  ): Promise<void> {
    await this.fetch(
      `/api/workspaces/${workspaceId}/runtime-profiles/${profileId}`,
      { method: "DELETE" },
    );
  }

  async getRuntimeUsage(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsage[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    // `tz` drives the calendar-day boundary for the trend chart (Viewing
    // layer). Caller-supplied; the backend falls back to user.timezone /
    // UTC if omitted.
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage?${search}`,
    );
    return parseWithFallback<RuntimeUsage[]>(raw, RuntimeUsageListSchema, [], {
      endpoint: "GET /api/runtimes/:id/usage",
    });
  }

  async getRuntimeTaskActivity(
    runtimeId: string,
    params?: { tz?: string },
  ): Promise<RuntimeHourlyActivity[]> {
    // Hour-of-day heatmap follows the viewer's tz, like the other reports on
    // this page. Pass the viewer's IANA zone so the server buckets correctly.
    const search = new URLSearchParams();
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/activity?${search}`,
    );
    return parseWithFallback<RuntimeHourlyActivity[]>(
      raw,
      RuntimeHourlyActivityListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/activity" },
    );
  }

  async getRuntimeUsageByAgent(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsageByAgent[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage/by-agent?${search}`,
    );
    return parseWithFallback<RuntimeUsageByAgent[]>(
      raw,
      RuntimeUsageByAgentListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/usage/by-agent" },
    );
  }

  async getRuntimeUsageByHour(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsageByHour[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage/by-hour?${search}`,
    );
    return parseWithFallback<RuntimeUsageByHour[]>(
      raw,
      RuntimeUsageByHourListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/usage/by-hour" },
    );
  }

  // ---------------------------------------------------------------------------
  // Workspace dashboard — three independent rollups for `/{slug}/dashboard`.
  // Each accepts an optional `project_id` to narrow the scope to one project.
  // Cost is computed client-side from the model pricing table (same contract
  // as the per-runtime endpoints above).
  // ---------------------------------------------------------------------------

  async getDashboardUsageDaily(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardUsageDaily[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/usage/daily?${search}`);
    return parseWithFallback<DashboardUsageDaily[]>(
      raw,
      DashboardUsageDailyListSchema,
      [],
      { endpoint: "GET /api/dashboard/usage/daily" },
    );
  }

  async getDashboardUsageByAgent(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardUsageByAgent[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/usage/by-agent?${search}`);
    return parseWithFallback<DashboardUsageByAgent[]>(
      raw,
      DashboardUsageByAgentListSchema,
      [],
      { endpoint: "GET /api/dashboard/usage/by-agent" },
    );
  }

  async getDashboardAgentRunTime(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardAgentRunTime[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    // `tz` aligns the "last N days" cutoff with the viewer's calendar,
    // matching the per-agent token card.
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/agent-runtime?${search}`);
    return parseWithFallback<DashboardAgentRunTime[]>(
      raw,
      DashboardAgentRunTimeListSchema,
      [],
      { endpoint: "GET /api/dashboard/agent-runtime" },
    );
  }

  async getDashboardRunTimeDaily(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardRunTimeDaily[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    // `tz` cuts the day buckets in the viewer's calendar so Time / Tasks
    // align with the Cost / Tokens charts.
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/runtime/daily?${search}`);
    return parseWithFallback<DashboardRunTimeDaily[]>(
      raw,
      DashboardRunTimeDailyListSchema,
      [],
      { endpoint: "GET /api/dashboard/runtime/daily" },
    );
  }

  async getDashboardFailuresDaily(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardFailureDaily[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    // `tz` cuts the day buckets in the viewer's calendar so the Errors chart
    // shares an x-axis with the other four metrics.
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/failures/daily?${search}`);
    return parseWithFallback<DashboardFailureDaily[]>(
      raw,
      DashboardFailureDailyListSchema,
      [],
      { endpoint: "GET /api/dashboard/failures/daily" },
    );
  }

  async getDashboardFailuresByAgent(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardFailureByAgent[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/failures/by-agent?${search}`);
    return parseWithFallback<DashboardFailureByAgent[]>(
      raw,
      DashboardFailureByAgentListSchema,
      [],
      { endpoint: "GET /api/dashboard/failures/by-agent" },
    );
  }

  async initiateUpdate(
    runtimeId: string,
    targetVersion: string,
  ): Promise<RuntimeUpdate> {
    return this.fetch(`/api/runtimes/${runtimeId}/update`, {
      method: "POST",
      body: JSON.stringify({ target_version: targetVersion }),
    });
  }

  async getUpdateResult(
    runtimeId: string,
    updateId: string,
  ): Promise<RuntimeUpdate> {
    return this.fetch(`/api/runtimes/${runtimeId}/update/${updateId}`);
  }

  // Both discovery endpoints feed a UI state machine (poll while
  // pending/running, then render or fail), so the response is validated rather
  // than cast: an unparseable body degrades to an explicit "failed" record that
  // shows the discovery error and keeps manual model entry usable, instead of a
  // fabricated empty catalog or an endless spinner (MUL-5444).
  async initiateListModels(runtimeId: string): Promise<RuntimeModelListRequest> {
    const raw = await this.fetch<unknown>(`/api/runtimes/${runtimeId}/models`, {
      method: "POST",
    });
    return parseWithFallback<RuntimeModelListRequest>(
      raw,
      RuntimeModelListRequestSchema,
      { ...MALFORMED_RUNTIME_MODEL_LIST_REQUEST, runtime_id: runtimeId },
      { endpoint: "POST /api/runtimes/{id}/models" },
    );
  }

  async getListModelsResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeModelListRequest> {
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/models/${requestId}`,
    );
    return parseWithFallback<RuntimeModelListRequest>(
      raw,
      RuntimeModelListRequestSchema,
      {
        ...MALFORMED_RUNTIME_MODEL_LIST_REQUEST,
        id: requestId,
        runtime_id: runtimeId,
      },
      { endpoint: "GET /api/runtimes/{id}/models/{requestId}" },
    );
  }

  async initiateListLocalSkills(
    runtimeId: string,
  ): Promise<RuntimeLocalSkillListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills`, {
      method: "POST",
    });
  }

  async getListLocalSkillsResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeLocalSkillListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/${requestId}`);
  }

  async initiateImportLocalSkill(
    runtimeId: string,
    data: CreateRuntimeLocalSkillImportRequest,
  ): Promise<RuntimeLocalSkillImportRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/import`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async getImportLocalSkillResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeLocalSkillImportRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/import/${requestId}`);
  }

  async listAgentTasks(agentId: string): Promise<AgentTask[]> {
    return this.fetch(`/api/agents/${agentId}/tasks`);
  }

  // Workspace-scoped agent task snapshot: every active task
  // (queued/dispatched/running) plus each agent's most recent terminal task.
  // Powers the front-end's "active wins, else latest terminal" presence
  // derivation; one fetch backs every per-agent presence read in the app.
  // Workspace is resolved server-side from the X-Workspace-Slug header.
  async getAgentTaskSnapshot(): Promise<AgentTask[]> {
    return this.fetch(`/api/agent-task-snapshot`);
  }

  // Independent workspace-level projection. Unlike the task snapshot, this
  // already deduplicates running agents and returns only the display fields
  // consumers need. Callers may narrow the projection by task source and, for
  // issue work, the authenticated member's My Issues relation.
  // `parentIssueId` narrows the projection to that issue's direct children,
  // which is how the sub-issue header on issue detail reads the same source
  // as the Issues list header. The server rejects combining it with `scope`,
  // so callers pass one or the other.
  async getWorkspaceWorkingAgents(
    type?: WorkspaceWorkingAgentType,
    mineRelation?: WorkspaceWorkingAgentMineRelation,
    parentIssueId?: string,
  ): Promise<WorkspaceWorkingAgent[]> {
    const search = new URLSearchParams();
    if (type) search.set("type", type);
    if (mineRelation) {
      search.set("scope", "mine");
      search.set("relation", mineRelation);
    } else if (parentIssueId) {
      search.set("parent", parentIssueId);
    }
    const query = search.toString();
    return this.fetch(`/api/working-agents${query ? `?${query}` : ""}`);
  }

  // Per-agent daily activity for the last 30 days, anchored on
  // completed_at. One workspace-wide fetch backs both the Agents-list
  // sparkline (uses trailing 7 buckets) and the agent detail "Last 30
  // days" panel (uses all 30).
  async getWorkspaceAgentActivity30d(): Promise<AgentActivityBucket[]> {
    return this.fetch(`/api/agent-activity-30d`);
  }

  // Per-agent 30-day total run count for the Agents-list RUNS column.
  async getWorkspaceAgentRunCounts(): Promise<AgentRunCount[]> {
    return this.fetch(`/api/agent-run-counts`);
  }

  async getActiveTasksForIssue(issueId: string): Promise<{ tasks: AgentTask[] }> {
    return this.fetch(`/api/issues/${issueId}/active-task`);
  }

  async listTaskMessages(taskId: string): Promise<TaskMessagePayload[]> {
    return this.fetch(`/api/tasks/${taskId}/messages`);
  }

  async listTasksByIssue(issueId: string): Promise<AgentTask[]> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/task-runs`);
    return parseWithFallback<AgentTask[]>(raw, AgentTaskListSchema, [], {
      endpoint: "GET /api/issues/:id/task-runs",
    });
  }

  async getIssueUsage(issueId: string): Promise<IssueUsageSummary> {
    return this.fetch(`/api/issues/${issueId}/usage`);
  }

  async cancelTask(issueId: string, taskId: string): Promise<AgentTask> {
    return this.fetch(`/api/issues/${issueId}/tasks/${taskId}/cancel`, {
      method: "POST",
    });
  }

  async rerunIssue(issueId: string, taskId?: string): Promise<AgentTask> {
    return this.fetch(`/api/issues/${issueId}/rerun`, {
      method: "POST",
      body: JSON.stringify(taskId ? { task_id: taskId } : {}),
    });
  }

  // App Config
  async getConfig(): Promise<AppConfigResponse> {
    const raw = await this.fetch<unknown>("/api/config");
    return parseWithFallback<AppConfigResponse>(raw, AppConfigSchema, EMPTY_APP_CONFIG, {
      endpoint: "GET /api/config",
    });
  }

  // Workspaces
  async listWorkspaces(): Promise<Workspace[]> {
    const workspace = await this.fetch<Workspace>("/api/workspaces/canonical");
    // Transitional internal cache shape: downstream path/realtime consumers
    // still expect an array, but the server returns exactly one authoritative
    // workspace and exposes no list or selection semantics.
    return [workspace];
  }

  async getWorkspace(id: string): Promise<Workspace> {
    return this.fetch(`/api/workspaces/${id}`);
  }

  async updateWorkspace(id: string, data: { name?: string; description?: string; context?: string; settings?: Record<string, unknown>; repos?: WorkspaceRepo[]; issue_prefix?: string; avatar_url?: string }): Promise<Workspace> {
    return this.fetch(`/api/workspaces/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  // Members
  async listMembers(workspaceId: string): Promise<MemberWithUser[]> {
    return this.fetch(`/api/workspaces/${workspaceId}/members`);
  }

  // Skills
  async listSkills(): Promise<SkillSummary[]> {
    return this.fetch("/api/skills");
  }

  async getSkill(id: string): Promise<Skill> {
    return this.fetch(`/api/skills/${id}`);
  }

  async createSkill(data: CreateSkillRequest): Promise<Skill> {
    return this.fetch("/api/skills", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateSkill(id: string, data: UpdateSkillRequest): Promise<Skill> {
    return this.fetch(`/api/skills/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteSkill(id: string): Promise<void> {
    await this.fetch(`/api/skills/${id}`, { method: "DELETE" });
  }

  async importSkill(data: { url: string }): Promise<Skill> {
    return this.fetch("/api/skills/import", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  // Re-downloads the skill from its stored config.origin source, replacing
  // name/description/content/files in place while preserving the skill id and
  // its agent bindings.
  async refreshSkill(id: string): Promise<Skill> {
    const raw = await this.fetch<unknown>(`/api/skills/${id}/refresh`, {
      method: "POST",
    });
    return parseWithFallback(raw, SkillSchema, EMPTY_SKILL, {
      endpoint: "POST /api/skills/:id/refresh",
    });
  }

  async listAgentSkills(agentId: string): Promise<SkillSummary[]> {
    return this.fetch(`/api/agents/${agentId}/skills`);
  }

  async setAgentSkills(agentId: string, data: SetAgentSkillsRequest): Promise<void> {
    await this.fetch(`/api/agents/${agentId}/skills`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  // Incremental attach: POST /skills/add only inserts the given ids (the
  // server upserts with ON CONFLICT DO NOTHING), so callers don't need to
  // read the agent's current skill set first.
  async addAgentSkills(agentId: string, data: SetAgentSkillsRequest): Promise<void> {
    await this.fetch(`/api/agents/${agentId}/skills/add`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

	async setAgentSkillEnabled(agentId: string, skillId: string, enabled: boolean): Promise<void> {
		await this.fetch(`/api/agents/${agentId}/skills/${skillId}/enabled`, {
			method: "PUT",
			body: JSON.stringify({ enabled }),
		});
	}

  async setAgentRuntimeSkillEnabled(
    agentId: string,
    data: SetAgentRuntimeSkillEnabledRequest,
  ): Promise<void> {
    await this.fetch(`/api/agents/${agentId}/runtime-skills/enabled`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

	async removeAgentSkill(agentId: string, skillId: string): Promise<void> {
		await this.fetch(`/api/agents/${agentId}/skills/${skillId}`, {
			method: "DELETE",
		});
	}

  // Personal Access Tokens
  async listPersonalAccessTokens(): Promise<PersonalAccessToken[]> {
    return this.fetch("/api/tokens");
  }

  async createPersonalAccessToken(data: CreatePersonalAccessTokenRequest): Promise<CreatePersonalAccessTokenResponse> {
    return this.fetch("/api/tokens", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async revokePersonalAccessToken(id: string): Promise<void> {
    await this.fetch(`/api/tokens/${id}`, { method: "DELETE" });
  }

  // File Upload & Attachments
  async uploadFile(
    file: File,
    opts?: { issueId?: string; commentId?: string },
    // Optional abort signal so a module-level upload coordinator (MUL-5181)
    // can cancel an in-flight upload on logout. When aborted, `fetch` rejects
    // with an AbortError, which the coordinator distinguishes from a real
    // failure via `signal.aborted` / `err.name === "AbortError"`.
    signal?: AbortSignal,
  ): Promise<Attachment> {
    const formData = new FormData();
    formData.append("file", file);
    if (opts?.issueId) formData.append("issue_id", opts.issueId);
    if (opts?.commentId) formData.append("comment_id", opts.commentId);

    const rid = createRequestId();
    const start = Date.now();
    this.logger.info("→ POST /api/upload-file", { rid });

    const res = await fetch(`${this.baseUrl}/api/upload-file`, {
      method: "POST",
      headers: this.authHeaders(),
      body: formData,
      credentials: "include",
      signal,
    });

    if (!res.ok) {
      if (res.status === 401) this.handleUnauthorized();
      const message = await this.parseErrorMessage(res, `Upload failed: ${res.status}`);
      this.logger.error(`← ${res.status} /api/upload-file`, { rid, duration: `${Date.now() - start}ms`, error: message });
      throw new Error(message);
    }

    this.logger.info(`← ${res.status} /api/upload-file`, { rid, duration: `${Date.now() - start}ms` });
    const raw = (await res.json()) as unknown;
    return parseWithFallback(raw, AttachmentResponseSchema, EMPTY_ATTACHMENT, {
      endpoint: "POST /api/upload-file",
    });
  }

  // Task cancellation is part of the generic agent task surface.

  async cancelTaskById(taskId: string): Promise<CancelTaskResponse> {
    return this.fetch(`/api/tasks/${taskId}/cancel`, { method: "POST" });
  }

  async listAttachments(issueId: string): Promise<Attachment[]> {
    return this.fetch(`/api/issues/${issueId}/attachments`);
  }

  // Fetches a fresh attachment metadata record. The server re-signs
  // `download_url` on every call (30 min expiry), so the click-time
  // download flow uses this endpoint to avoid handing the user a stale
  // signed URL cached in TanStack Query.
  async getAttachment(id: string): Promise<Attachment> {
    const raw = await this.fetch<unknown>(`/api/attachments/${id}`);
    return parseWithFallback(raw, AttachmentResponseSchema, EMPTY_ATTACHMENT, {
      endpoint: "GET /api/attachments/{id}",
    });
  }

  async deleteAttachment(id: string): Promise<void> {
    await this.fetch(`/api/attachments/${id}`, { method: "DELETE" });
  }

  // Fetches the raw bytes of a text-previewable attachment.
  //
  // The endpoint sidesteps CloudFront CORS (not configured on the CDN) and
  // bypasses Content-Disposition: attachment for the `text/*` family, both
  // of which would otherwise prevent the renderer from getting the body.
  // The server always replies with `text/plain; charset=utf-8` for safety;
  // the original MIME ships back in the `X-Original-Content-Type` header so
  // the preview dispatcher can choose between markdown / html / plain code.
  //
  // Routes through `fetchRaw` so it inherits the standard auth headers,
  // 401 → handleUnauthorized recovery, request-id logging, and ApiError
  // shape. 413 / 415 are translated to typed `Preview*Error` instances so
  // the modal can render specific fallbacks instead of generic failure.
  async getAttachmentTextContent(
    id: string,
  ): Promise<{ text: string; originalContentType: string }> {
    let res: Response;
    try {
      res = await this.fetchRaw(`/api/attachments/${id}/content`);
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 413) throw new PreviewTooLargeError();
        if (err.status === 415) throw new PreviewUnsupportedError();
      }
      throw err;
    }
    return {
      text: await res.text(),
      originalContentType: res.headers.get("X-Original-Content-Type") ?? "",
    };
  }

  // Fetches the raw bytes of an attachment through the unified download
  // endpoint.
  //
  // This is the last-resort inline-media path for deployments where the
  // server has no natively-loadable URL to offer. `GET /api/attachments/{id}`
  // only upgrades `download_url` to a signed storage URL under CloudFront
  // signing or presign mode; in **proxy** mode (self-hosted MinIO or any
  // storage endpoint on an internal host, which the default `auto` mode
  // classifies as proxy) it returns the auth-gated API path again. Clients
  // that cannot ride the session cookie on a native `<img>` resource fetch —
  // Desktop's file:// renderer, the mobile webview, split-origin web — get
  // the bytes here and render them from an object URL instead.
  //
  // Routes through `fetchRaw` so it inherits the standard auth headers,
  // 401 → handleUnauthorized recovery, request-id logging and ApiError shape.
  // Callers must only reach for this once the metadata refresh has shown
  // there is no signed URL: in the other modes the endpoint 302s to storage,
  // where CORS is not configured for a JS fetch.
  async getAttachmentBlob(id: string): Promise<Blob> {
    const res = await this.fetchRaw(`/api/attachments/${id}/download`);
    return res.blob();
  }

  // Projects
  async listProjects(params?: { status?: string }): Promise<ListProjectsResponse> {
    const search = new URLSearchParams();
    if (params?.status) search.set("status", params.status);
    return this.fetch(`/api/projects?${search}`);
  }

  async getProject(id: string): Promise<Project> {
    return this.fetch(`/api/projects/${id}`);
  }

  async getProjectCommandCenter(id: string): Promise<ProjectCommandCenterProjection> {
    const raw = await this.fetch<unknown>(
      `/api/projects/${encodeURIComponent(id)}/command-center`,
    );
    const projection = parseProjectCommandCenterProjection(raw);
    if (!projection) {
      throw new Error("Invalid Project Command Center response");
    }
    return projection;
  }

  async createProject(data: CreateProjectRequest): Promise<Project> {
    return this.fetch("/api/projects", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateProject(id: string, data: UpdateProjectRequest): Promise<Project> {
    return this.fetch(`/api/projects/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteProject(id: string): Promise<void> {
    await this.fetch(`/api/projects/${id}`, { method: "DELETE" });
  }

  // Project resources
  async listProjectResources(
    projectId: string,
  ): Promise<ListProjectResourcesResponse> {
    return this.fetch(`/api/projects/${projectId}/resources`);
  }

  async createProjectResource(
    projectId: string,
    data: CreateProjectResourceRequest,
  ): Promise<ProjectResource> {
    return this.fetch(`/api/projects/${projectId}/resources`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateProjectResource(
    projectId: string,
    resourceId: string,
    data: UpdateProjectResourceRequest,
  ): Promise<ProjectResource> {
    return this.fetch(`/api/projects/${projectId}/resources/${resourceId}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteProjectResource(
    projectId: string,
    resourceId: string,
  ): Promise<void> {
    await this.fetch(`/api/projects/${projectId}/resources/${resourceId}`, {
      method: "DELETE",
    });
  }

  // Labels
  async listLabels(resourceType: LabelResourceType = "issue"): Promise<ListLabelsResponse> {
    const raw = await this.fetch<unknown>(`/api/labels?resource_type=${resourceType}`);
    return parseWithFallback(raw, ListLabelsResponseSchema, EMPTY_LIST_LABELS_RESPONSE, {
      endpoint: "GET /api/labels",
    });
  }

  async getLabel(id: string): Promise<Label> {
    const raw = await this.fetch<unknown>(`/api/labels/${id}`);
    return parseWithFallback(raw, LabelSchema, EMPTY_LABEL, {
      endpoint: "GET /api/labels/{id}",
    });
  }

  async createLabel(data: CreateLabelRequest): Promise<Label> {
    const raw = await this.fetch<unknown>(`/api/labels`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, LabelSchema, EMPTY_LABEL, {
      endpoint: "POST /api/labels",
    });
  }

  async updateLabel(id: string, data: UpdateLabelRequest): Promise<Label> {
    const raw = await this.fetch<unknown>(`/api/labels/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, LabelSchema, EMPTY_LABEL, {
      endpoint: "PUT /api/labels/{id}",
    });
  }

  async deleteLabel(id: string): Promise<void> {
    await this.fetch(`/api/labels/${id}`, { method: "DELETE" });
  }

  /**
   * Quick actions catalog — one projection for every caller.
   *
   * The server hides nothing beyond `private` ownership; whether the caller
   * may RUN an action is answered by runQuickAction, not here. There is
   * deliberately no "runnable only" mode: filtering the sidebar by permission
   * made two people looking at one issue see different sidebars with no
   * explanation.
   *
   * A backend predating quick actions 404s here; treat that as an empty
   * catalog so the sidebar section and settings tab simply do not render.
   */
  async listQuickActions(opts?: { includeArchived?: boolean }): Promise<ListQuickActionsResponse> {
    const suffix = opts?.includeArchived === true ? "?include_archived=true" : "";
    let raw: unknown;
    try {
      raw = await this.fetch<unknown>(`/api/quick-actions${suffix}`);
    } catch (error) {
      if (error instanceof Error && "status" in error && (error as { status?: number }).status === 404) {
        return EMPTY_LIST_QUICK_ACTIONS_RESPONSE;
      }
      throw error;
    }
    return parseWithFallback(raw, ListQuickActionsResponseSchema, EMPTY_LIST_QUICK_ACTIONS_RESPONSE, {
      endpoint: "GET /api/quick-actions",
    });
  }

  async createQuickAction(data: CreateQuickActionRequest): Promise<QuickAction> {
    const raw = await this.fetch<unknown>(`/api/quick-actions`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, QuickActionSchema, EMPTY_QUICK_ACTION, {
      endpoint: "POST /api/quick-actions",
    });
  }

  async updateQuickAction(id: string, data: UpdateQuickActionRequest): Promise<QuickAction> {
    const raw = await this.fetch<unknown>(`/api/quick-actions/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, QuickActionSchema, EMPTY_QUICK_ACTION, {
      endpoint: "PATCH /api/quick-actions/{id}",
    });
  }

  async deleteQuickAction(id: string): Promise<void> {
    await this.fetch<void>(`/api/quick-actions/${id}`, { method: "DELETE" });
  }

  /**
   * Run a quick action against one issue. The response is a Comment carrying
   * `trigger_outcomes` — the same shape POST /comments returns — so callers
   * reuse one result handler and inherit `queued` / `coalesced` / `deferred` /
   * `blocked` instead of a parallel vocabulary that would drift.
   */
  async runQuickAction(issueId: string, quickActionId: string): Promise<Comment> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/quick-actions/${quickActionId}/run`, {
      method: "POST",
    });
    return parseWithFallback(raw, CommentSchema, EMPTY_COMMENT, {
      endpoint: "POST /api/issues/{id}/quick-actions/{quickActionId}/run",
    });
  }

  /**
   * What a quick action WOULD post, without posting it. Backs the composer
   * hand-off (⌥-click and the `/` menu) so the user can edit before sending.
   * Returns "" when the response cannot be read — callers must treat an empty
   * string as "insert nothing" rather than clearing the composer.
   */
  async renderQuickAction(issueId: string, quickActionId: string): Promise<string> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/quick-actions/${quickActionId}/render`, {
      method: "POST",
    });
    const parsed = parseWithFallback(raw, QuickActionRenderSchema, { content: "" }, {
      endpoint: "POST /api/issues/{id}/quick-actions/{quickActionId}/render",
    });
    return parsed.content;
  }

  async listLabelsForIssue(issueId: string): Promise<IssueLabelsResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/labels`);
    return parseWithFallback(raw, ResourceLabelsResponseSchema, EMPTY_RESOURCE_LABELS_RESPONSE, {
      endpoint: "GET /api/issues/{id}/labels",
    });
  }

  async attachLabel(issueId: string, labelId: string): Promise<IssueLabelsResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/labels`, {
      method: "POST",
      body: JSON.stringify({ label_id: labelId }),
    });
    return parseWithFallback(raw, ResourceLabelsResponseSchema, EMPTY_RESOURCE_LABELS_RESPONSE, {
      endpoint: "POST /api/issues/{id}/labels",
    });
  }

  async detachLabel(issueId: string, labelId: string): Promise<IssueLabelsResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/labels/${labelId}`, {
      method: "DELETE",
    });
    return parseWithFallback(raw, ResourceLabelsResponseSchema, EMPTY_RESOURCE_LABELS_RESPONSE, {
      endpoint: "DELETE /api/issues/{id}/labels/{labelId}",
    });
  }

  async listLabelsForResource(
    resourceType: "agent" | "skill",
    resourceId: string,
  ): Promise<ResourceLabelsResponse> {
    const raw = await this.fetch<unknown>(`/api/${resourceType === "agent" ? "agents" : "skills"}/${resourceId}/labels`);
    return parseWithFallback(raw, ResourceLabelsResponseSchema, EMPTY_RESOURCE_LABELS_RESPONSE, {
      endpoint: `GET /api/${resourceType === "agent" ? "agents" : "skills"}/{id}/labels`,
    });
  }

  async attachLabelToResource(
    resourceType: "agent" | "skill",
    resourceId: string,
    labelId: string,
  ): Promise<ResourceLabelsResponse> {
    const raw = await this.fetch<unknown>(`/api/${resourceType === "agent" ? "agents" : "skills"}/${resourceId}/labels`, {
      method: "POST",
      body: JSON.stringify({ label_id: labelId }),
    });
    return parseWithFallback(raw, ResourceLabelsResponseSchema, EMPTY_RESOURCE_LABELS_RESPONSE, {
      endpoint: `POST /api/${resourceType === "agent" ? "agents" : "skills"}/{id}/labels`,
    });
  }

  async detachLabelFromResource(
    resourceType: "agent" | "skill",
    resourceId: string,
    labelId: string,
  ): Promise<ResourceLabelsResponse> {
    const raw = await this.fetch<unknown>(`/api/${resourceType === "agent" ? "agents" : "skills"}/${resourceId}/labels/${labelId}`, {
      method: "DELETE",
    });
    return parseWithFallback(raw, ResourceLabelsResponseSchema, EMPTY_RESOURCE_LABELS_RESPONSE, {
      endpoint: `DELETE /api/${resourceType === "agent" ? "agents" : "skills"}/{id}/labels/{labelId}`,
    });
  }

  // GitHub integration
  async getGitHubConnectURL(
    workspaceId: string,
    returnTo?: "github" | "repositories",
  ): Promise<GitHubConnectResponse> {
    const search = new URLSearchParams();
    if (returnTo) search.set("return_to", returnTo);
    const suffix = search.size > 0 ? `?${search.toString()}` : "";
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/github/connect${suffix}`,
    );
    return parseWithFallback(
      raw,
      GitHubConnectResponseSchema,
      EMPTY_GITHUB_CONNECT_RESPONSE,
      { endpoint: "GET /api/workspaces/:id/github/connect" },
    );
  }

  async listGitHubInstallations(workspaceId: string): Promise<ListGitHubInstallationsResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/github/installations`,
    );
    return parseWithFallback(
      raw,
      ListGitHubInstallationsResponseSchema,
      EMPTY_LIST_GITHUB_INSTALLATIONS_RESPONSE,
      { endpoint: "GET /api/workspaces/:id/github/installations" },
    );
  }

  async listGitHubInstallationRepositories(
    workspaceId: string,
    installationId: string,
    params: { page?: number; per_page?: number } = {},
  ): Promise<ListGitHubRepositoriesResponse> {
    const search = new URLSearchParams();
    if (params.page !== undefined) search.set("page", String(params.page));
    if (params.per_page !== undefined) search.set("per_page", String(params.per_page));
    const suffix = search.size > 0 ? `?${search.toString()}` : "";
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/github/installations/${installationId}/repositories${suffix}`,
    );
    return parseWithFallback(
      raw,
      ListGitHubRepositoriesResponseSchema,
      EMPTY_LIST_GITHUB_REPOSITORIES_RESPONSE,
      { endpoint: "GET /api/workspaces/:id/github/installations/:installationId/repositories" },
    );
  }

  async deleteGitHubInstallation(workspaceId: string, installationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/github/installations/${installationId}`, {
      method: "DELETE",
    });
  }

  async listIssuePullRequests(issueId: string): Promise<{ pull_requests: GitHubPullRequest[] }> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/pull-requests`);
    return parseWithFallback(
      raw,
      IssuePullRequestsResponseSchema,
      EMPTY_ISSUE_PULL_REQUESTS_RESPONSE,
      { endpoint: "GET /api/issues/:id/pull-requests" },
    );
  }

  // VCS integration (Forgejo / Gitea / GitLab)
  async listVCSConnections(workspaceId: string): Promise<ListVCSConnectionsResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/vcs/connections`);
  }

  async connectVCS(
    workspaceId: string,
    body: ConnectVCSRequest,
  ): Promise<ConnectVCSResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/vcs/connections`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  async deleteVCSConnection(workspaceId: string, connectionId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/vcs/connections/${connectionId}`, {
      method: "DELETE",
    });
  }

  async rotateVCSWebhook(
    workspaceId: string,
    connectionId: string,
  ): Promise<ConnectVCSResponse> {
    return this.fetch(
      `/api/workspaces/${workspaceId}/vcs/connections/${connectionId}/rotate-webhook`,
      { method: "POST" },
    );
  }

}
