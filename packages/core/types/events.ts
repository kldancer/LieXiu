import type { Issue, IssueMetadata } from "./issue";
import type { Agent } from "./agent";
import type { Comment } from "./comment";
import type { TimelineEntry } from "./activity";
import type { Workspace } from "./workspace";
import type { Project } from "./project";
import type { Label } from "./label";

// WebSocket event types (matching Go server protocol/events.go)
export type WSEventType =
  | "issue:created"
  | "issue:updated"
  | "issue_attachments:changed"
  | "issue:deleted"
  | "comment:created"
  | "comment:updated"
  | "comment:deleted"
  | "comment:resolved"
  | "comment:unresolved"
  | "agent:status"
  | "agent:created"
  | "agent:archived"
  | "agent:restored"
  | "task:queued"
  | "task:dispatch"
  | "task:running"
  | "task:waiting_local_directory"
  | "task:progress"
  | "task:completed"
  | "task:failed"
  | "task:message"
  | "task:cancelled"
  | "workspace:updated"
  | "daemon:heartbeat"
  | "daemon:register"
  | "skill:created"
  | "skill:updated"
  | "skill:deleted"
  | "activity:created"
  | "project:created"
  | "project:updated"
  | "project:deleted"
  | "label:created"
  | "label:updated"
  | "label:deleted"
  | "issue_labels:changed"
  | "issue_metadata:changed"
  | "github_installation:created"
  | "github_installation:deleted"
  | "pull_request:linked"
  | "pull_request:updated"
  | "pull_request:unlinked";

export interface WSMessage<T = unknown> {
  type: WSEventType;
  payload: T;
  actor_id?: string;
  actor_type?: string;
}

export interface IssueCreatedPayload {
  issue: Issue;
}

export interface IssueUpdatedPayload {
  issue: Issue;
  // The server stamps issue:updated with which fields actually changed
  // (server/internal/handler/issue.go publish). assignee_changed lets the
  // realtime layer keep filtered myList caches in place on a non-membership
  // change instead of refetching; status_changed lets it reconcile board column
  // counts when a status change lands on an off-screen (unloaded) issue;
  // project_changed lets it drop a moved issue from the old project's filtered
  // list (the client-side cache diff is unreliable after an optimistic local
  // move — MUL-3669 / #4548). Other change flags are present on the wire too and
  // can be surfaced here when needed.
  assignee_changed?: boolean;
  status_changed?: boolean;
  project_changed?: boolean;
}

export interface IssueDeletedPayload {
  issue_id: string;
}

export interface IssueLabelsChangedPayload {
  issue_id: string;
  labels: Label[];
}

export interface IssueAttachmentsChangedPayload {
  issue_id: string;
}

export interface IssueMetadataChangedPayload {
  issue_id: string;
  metadata: IssueMetadata;
}

export interface AgentStatusPayload {
  agent: Agent;
}

export interface AgentCreatedPayload {
  agent: Agent;
}

export interface AgentArchivedPayload {
  agent: Agent;
}

export interface AgentRestoredPayload {
  agent: Agent;
}

export interface CommentCreatedPayload {
  comment: Comment;
}

export interface CommentUpdatedPayload {
  comment: Comment;
}

export interface CommentDeletedPayload {
  comment_id: string;
  issue_id: string;
}

export interface CommentResolvedPayload {
  comment: Comment;
}

export interface CommentUnresolvedPayload {
  comment: Comment;
}

export interface WorkspaceUpdatedPayload {
  workspace: Workspace;
}

export interface ActivityCreatedPayload {
  issue_id: string;
  entry: TimelineEntry;
}

export interface TaskMessagePayload {
  task_id: string;
  issue_id: string;
  seq: number;
  type: "text" | "thinking" | "tool_use" | "tool_result" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
  created_at?: string;
}

export interface TaskQueuedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  status: string;
}

export interface TaskDispatchPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  runtime_id: string;
}

export interface TaskRunningPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  status: string;
}

// task:waiting_local_directory fires when the daemon dequeues a task but
// can't immediately acquire the on-disk path lock — another task on this
// daemon is already executing in the same local_directory. The optional
// `wait_reason` mirrors the server-side hint (path / holder task id), but
// is not yet surfaced end-to-end; the UI today only reads the status.
export interface TaskWaitingLocalDirectoryPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  status: string;
  wait_reason?: string;
}

export interface TaskCompletedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  status: string;
}

export interface TaskFailedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  status: string;
  failure_reason?: string;
  retry_pending?: boolean;
}

export interface TaskCancelledPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  status: string;
}

export interface ProjectCreatedPayload {
  project: Project;
}

export interface ProjectUpdatedPayload {
  project: Project;
}

export interface ProjectDeletedPayload {
  project_id: string;
}

/**
 * Maps every WSEventType to its payload interface. Events whose payload
 * shape isn't formally typed (server emits an object the client doesn't
 * meaningfully consume yet) fall back to `unknown` — callers must narrow
 * before access.
 *
 * Use via `WSEventPayload<E>` rather than indexing the map directly:
 *   const handler = (payload: WSEventPayload<"issue:created">) => { ... };
 *
 * Adding a new event: extend WSEventType first (above), then append a key
 * here. TS will compile-error every WSClient.on("new:event", …) site that
 * forgets the payload shape — that's the whole point.
 */
export interface WSEventPayloadMap {
  "issue:created": IssueCreatedPayload;
  "issue:updated": IssueUpdatedPayload;
  "issue:deleted": IssueDeletedPayload;
  "issue_attachments:changed": IssueAttachmentsChangedPayload;
  "issue_labels:changed": IssueLabelsChangedPayload;
  "comment:created": CommentCreatedPayload;
  "comment:updated": CommentUpdatedPayload;
  "comment:deleted": CommentDeletedPayload;
  "comment:resolved": CommentResolvedPayload;
  "comment:unresolved": CommentUnresolvedPayload;
  "agent:status": AgentStatusPayload;
  "agent:created": AgentCreatedPayload;
  "agent:archived": AgentArchivedPayload;
  "agent:restored": AgentRestoredPayload;
  "task:queued": TaskQueuedPayload;
  "task:dispatch": TaskDispatchPayload;
  "task:running": TaskRunningPayload;
  "task:waiting_local_directory": TaskWaitingLocalDirectoryPayload;
  "task:completed": TaskCompletedPayload;
  "task:failed": TaskFailedPayload;
  "task:message": TaskMessagePayload;
  "task:cancelled": TaskCancelledPayload;
  "task:progress": unknown;
  "workspace:updated": WorkspaceUpdatedPayload;
  "activity:created": ActivityCreatedPayload;
  "project:created": ProjectCreatedPayload;
  "project:updated": ProjectUpdatedPayload;
  "project:deleted": ProjectDeletedPayload;
  // No formal payload interfaces yet — server emits domain objects clients
  // currently consume as opaque triggers (refetch on receipt).
  "daemon:heartbeat": unknown;
  "daemon:register": unknown;
  "skill:created": unknown;
  "skill:updated": unknown;
  "skill:deleted": unknown;
  "label:created": unknown;
  "label:updated": unknown;
  "label:deleted": unknown;
  "github_installation:created": unknown;
  "github_installation:deleted": unknown;
  "pull_request:linked": unknown;
  "pull_request:updated": unknown;
  "pull_request:unlinked": unknown;
}

/**
 * Payload type for a given event. Lookup against WSEventPayloadMap with
 * `unknown` as the safety net — if a future WSEventType is added without
 * a map entry, callers see `unknown` (forced narrow) rather than `any`
 * (silent unsafe access).
 */
export type WSEventPayload<E extends WSEventType> =
  E extends keyof WSEventPayloadMap ? WSEventPayloadMap[E] : unknown;
