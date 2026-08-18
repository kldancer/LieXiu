import type { ActivityProjection } from "@liexiu/core/orchestration";

export const VISUAL_EVENT_KINDS = [
  "mission.created",
  "mission.plan_requested",
  "mission.plan_accepted",
  "agent.assigned",
  "agent.started",
  "task.ready",
  "task.retry_requested",
  "agent.blocked",
  "agent.reviewing",
  "agent.completed",
  "run.queued",
  "run.started",
  "run.succeeded",
  "run.failed",
  "run.cancelled",
  "artifact.created",
  "review.approved",
  "review.changes_requested",
  "review.rejected",
  "mission.started",
  "mission.blocked",
  "mission.completed",
  "mission.failed",
  "mission.cancelled",
  "budget.approval_required",
  "budget.exceeded",
  "budget.approved",
  "human_gate.required",
  "human_gate.resolved",
  "plan_proposal.edited",
  "plan_proposal.rejected",
  "mailbox.sent",
  "mailbox.consumed",
  "mailbox.expired",
  "mailbox.cancelled",
] as const;

export type VisualEventKind = (typeof VISUAL_EVENT_KINDS)[number];
export type VisualEventPriority = "low" | "normal" | "high" | "critical";
export type VisualTargetType = "mission" | "task" | "run" | "artifact" | "mailbox";

export interface VisualEventTarget {
  type: VisualTargetType;
  id: string;
}

/**
 * Renderer-neutral, bounded input for the World scene and Replay.
 * Activity payloads deliberately do not cross this boundary.
 */
export interface VisualEvent {
  key: string;
  activityId: string;
  sequence: number;
  kind: VisualEventKind;
  target: VisualEventTarget;
  priority: VisualEventPriority;
}

const EVENT_KIND_BY_ACTIVITY: Readonly<Record<string, VisualEventKind>> = {
  "mission.created": "mission.created",
  "mission.plan_requested": "mission.plan_requested",
  "mission.plan_accepted": "mission.plan_accepted",
  "task.assigned": "agent.assigned",
  "task.started": "agent.started",
  "task.ready": "task.ready",
  "task.retry_requested": "task.retry_requested",
  "task.blocked": "agent.blocked",
  "task.review_requested": "agent.reviewing",
  "task.completed": "agent.completed",
  "task.rework_requested": "agent.blocked",
  "task.failed": "agent.blocked",
  "task.cancelled": "agent.blocked",
  "run.queued": "run.queued",
  "run.started": "run.started",
  "run.succeeded": "run.succeeded",
  "run.failed": "run.failed",
  "run.cancelled": "run.cancelled",
  "artifact.created": "artifact.created",
  "review.approved": "review.approved",
  "review.changes_requested": "review.changes_requested",
  "review.rejected": "review.rejected",
  "mission.started": "mission.started",
  "mission.blocked": "mission.blocked",
  "mission.completed": "mission.completed",
  "mission.failed": "mission.failed",
  "mission.cancelled": "mission.cancelled",
  "budget.approval_required": "budget.approval_required",
  "budget.exceeded": "budget.exceeded",
  "budget.approved": "budget.approved",
  "human_gate.required": "human_gate.required",
  "human_gate.resolved": "human_gate.resolved",
  "plan_proposal.edited": "plan_proposal.edited",
  "plan_proposal.rejected": "plan_proposal.rejected",
  "mailbox.message_sent": "mailbox.sent",
  "mailbox.message_consumed": "mailbox.consumed",
  "mailbox.message_expired": "mailbox.expired",
  "mailbox.message_cancelled": "mailbox.cancelled",
};

const TARGET_TYPE_BY_SUBJECT: Readonly<Record<string, VisualTargetType>> = {
  mission: "mission",
  task_node: "task",
  run: "run",
  artifact: "artifact",
  mailbox_message: "mailbox",
};

const CRITICAL_KINDS = new Set<VisualEventKind>([
  "agent.blocked",
  "run.failed",
  "review.rejected",
  "mission.blocked",
  "mission.failed",
  "budget.approval_required",
  "budget.exceeded",
  "human_gate.required",
  "plan_proposal.rejected",
]);
const HIGH_KINDS = new Set<VisualEventKind>([
  "mission.plan_accepted",
  "agent.completed",
  "run.succeeded",
  "review.approved",
  "review.changes_requested",
  "mission.completed",
  "mission.cancelled",
  "artifact.created",
  "budget.approved",
  "human_gate.resolved",
  "plan_proposal.edited",
]);
const LOW_KINDS = new Set<VisualEventKind>([
  "mailbox.sent",
  "mailbox.consumed",
  "mailbox.expired",
  "mailbox.cancelled",
]);

export function visualEventKey(activity: Pick<ActivityProjection, "id">): string {
  return `activity:${activity.id}`;
}

export function activityToVisualEvent(activity: ActivityProjection): VisualEvent | null {
  if (!isActivity(activity)) return null;
  const kind = EVENT_KIND_BY_ACTIVITY[activity.type];
  const targetType = TARGET_TYPE_BY_SUBJECT[activity.subjectType];
  if (!kind || !targetType) return null;
  return {
    key: visualEventKey(activity),
    activityId: activity.id,
    sequence: activity.sequence,
    kind,
    target: { type: targetType, id: activity.subjectId },
    priority: priorityForKind(kind),
  };
}

/** Maps a projection window into a deterministic, sequence-deduplicated event list. */
export function activitiesToVisualEvents(activities: readonly ActivityProjection[]): VisualEvent[] {
  const candidates = activities
    .map((activity) => ({ activity, event: activityToVisualEvent(activity) }))
    .filter((item): item is { activity: ActivityProjection; event: VisualEvent } => item.event !== null)
    .sort((left, right) => compareActivities(left.activity, right.activity));
  const bySequence = new Map<number, VisualEvent>();
  for (const candidate of candidates) {
    if (!bySequence.has(candidate.event.sequence)) bySequence.set(candidate.event.sequence, candidate.event);
  }
  return [...bySequence.values()].sort((left, right) => left.sequence - right.sequence || left.key.localeCompare(right.key));
}

export const projectActivitiesToVisualEvents = activitiesToVisualEvents;

function priorityForKind(kind: VisualEventKind): VisualEventPriority {
  if (CRITICAL_KINDS.has(kind)) return "critical";
  if (HIGH_KINDS.has(kind)) return "high";
  if (LOW_KINDS.has(kind)) return "low";
  return "normal";
}

function compareActivities(left: ActivityProjection, right: ActivityProjection): number {
  return left.sequence - right.sequence
    || left.id.localeCompare(right.id)
    || left.type.localeCompare(right.type)
    || left.subjectType.localeCompare(right.subjectType)
    || left.subjectId.localeCompare(right.subjectId);
}

function isActivity(value: ActivityProjection): value is ActivityProjection {
  if (!isRecord(value)) return false;
  return typeof value.id === "string" && value.id.length > 0
    && Number.isSafeInteger(value.sequence) && value.sequence > 0
    && typeof value.type === "string" && typeof value.subjectType === "string"
    && typeof value.subjectId === "string" && value.subjectId.length > 0;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}
