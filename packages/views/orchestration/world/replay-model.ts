import type { WorldModel } from "./world-model";
import { VISUAL_EVENT_KINDS, type VisualEvent } from "./visual-events";

export type ReplayStatus = "paused" | "playing";

export interface ReplayFilter {
  actorId?: string;
  taskId?: string;
  runId?: string;
}

export interface ReplayState {
  /** Sequence represented by the immutable Snapshot before replay starts. */
  snapshotSequence: number;
  /** The latest sequence included in the replay cursor. */
  cursor: number;
  rate: number;
  status: ReplayStatus;
  filter: ReplayFilter;
}

export interface ReplayModel extends ReplayState {
  readonly snapshot: WorldModel;
  readonly events: readonly VisualEvent[];
}

export type ReplayAction =
  | { type: "play" }
  | { type: "pause" }
  | { type: "set_rate"; rate: number }
  | { type: "seek"; cursor: number }
  | { type: "set_filter"; filter: ReplayFilter }
  | { type: "tick"; elapsedMs: number };

const DEFAULT_RATE = 1;
const MIN_RATE = 0.25;
const MAX_RATE = 4;
const SEQUENCE_STEPS_PER_SECOND = 1;

/**
 * Creates a deterministic, renderer-free Replay model. The Snapshot is never
 * mutated and event payloads are not accepted at this boundary.
 */
export function createReplayModel(
  snapshot: WorldModel,
  events: readonly VisualEvent[],
  options: Partial<Pick<ReplayState, "snapshotSequence" | "cursor" | "rate" | "status" | "filter">> = {},
): ReplayModel {
  const snapshotSequence = safeSequence(options.snapshotSequence ?? 0);
  const normalizedEvents = normalizeEvents(events, snapshotSequence);
  const lastSequence = normalizedEvents.at(-1)?.sequence ?? snapshotSequence;
  const cursor = clampSequence(options.cursor ?? snapshotSequence, snapshotSequence, lastSequence);
  return {
    snapshot,
    events: normalizedEvents,
    snapshotSequence,
    cursor,
    rate: normalizeRate(options.rate ?? DEFAULT_RATE),
    status: options.status === "playing" ? "playing" : "paused",
    filter: normalizeFilter(options.filter),
  };
}

export function reduceReplay(model: ReplayModel, action: ReplayAction): ReplayModel {
  switch (action.type) {
    case "play":
      return { ...model, status: "playing" };
    case "pause":
      return { ...model, status: "paused" };
    case "set_rate":
      return { ...model, rate: normalizeRate(action.rate) };
    case "seek":
      return { ...model, cursor: clampSequence(action.cursor, model.snapshotSequence, lastSequence(model)) };
    case "set_filter":
      return { ...model, filter: normalizeFilter(action.filter) };
    case "tick":
      return advanceReplay(model, action.elapsedMs);
  }
}

export function playReplay(model: ReplayModel): ReplayModel {
  return reduceReplay(model, { type: "play" });
}

export function pauseReplay(model: ReplayModel): ReplayModel {
  return reduceReplay(model, { type: "pause" });
}

export function seekReplay(model: ReplayModel, cursor: number): ReplayModel {
  return reduceReplay(model, { type: "seek", cursor });
}

export function setReplayRate(model: ReplayModel, rate: number): ReplayModel {
  return reduceReplay(model, { type: "set_rate", rate });
}

export function setReplayFilter(model: ReplayModel, filter: ReplayFilter): ReplayModel {
  return reduceReplay(model, { type: "set_filter", filter });
}

/** Advances by sequence units, scaled by the selected rate. No wall clock is used. */
export function advanceReplay(model: ReplayModel, elapsedMs: number): ReplayModel {
  if (model.status !== "playing" || !Number.isFinite(elapsedMs) || elapsedMs <= 0) return model;
  const maximum = lastSequence(model);
  const next = model.cursor + elapsedMs / 1_000 * SEQUENCE_STEPS_PER_SECOND * model.rate;
  const cursor = clampSequence(next, model.snapshotSequence, maximum);
  return { ...model, cursor, status: cursor >= maximum ? "paused" : model.status };
}

/** Events visible at the current cursor, after the stable filter is applied. */
export function replayEvents(model: ReplayModel): VisualEvent[] {
  return model.events.filter((event) => event.sequence <= model.cursor && matchesFilter(event, model.filter, model.snapshot));
}

export const visibleReplayEvents = replayEvents;

function normalizeEvents(events: readonly VisualEvent[], snapshotSequence: number): VisualEvent[] {
  const candidates = events.filter(isVisualEvent).filter((event) => event.sequence > snapshotSequence).sort(compareEvents);
  const bySequence = new Map<number, VisualEvent>();
  for (const event of candidates) if (!bySequence.has(event.sequence)) bySequence.set(event.sequence, event);
  return [...bySequence.values()];
}

function matchesFilter(event: VisualEvent, filter: ReplayFilter, snapshot: WorldModel): boolean {
  if (filter.taskId && !targetOrRelated(event, "task", filter.taskId, snapshot)) return false;
  if (filter.runId && !targetOrRelated(event, "run", filter.runId, snapshot)) return false;
  if (filter.actorId && !actorMatches(event, filter.actorId, snapshot)) return false;
  return true;
}

function actorMatches(event: VisualEvent, actorId: string, snapshot: WorldModel): boolean {
  const actor = snapshot.actors.find((item) => item.id === actorId || item.agentId === actorId);
  if (!actor) return event.target.type === "mission" && event.target.id === actorId;
  return event.target.type === "mission"
    || (event.target.type === "task" && event.target.id === actor.nodeId)
    || (event.target.type === "run" && event.target.id === actor.runId)
    || (event.target.type === "task" && event.target.id === actor.id)
    || (event.target.type === "run" && event.target.id === actor.id);
}

function targetOrRelated(event: VisualEvent, type: "task" | "run", id: string, snapshot: WorldModel): boolean {
  if (event.target.type === type) return event.target.id === id;
  return snapshot.actors.some((actor) => (type === "task" ? actor.nodeId : actor.runId) === id && event.target.id === actor.id);
}

function compareEvents(left: VisualEvent, right: VisualEvent): number {
  return left.sequence - right.sequence
    || left.key.localeCompare(right.key)
    || left.activityId.localeCompare(right.activityId)
    || left.kind.localeCompare(right.kind)
    || left.target.type.localeCompare(right.target.type)
    || left.target.id.localeCompare(right.target.id);
}

function isVisualEvent(value: VisualEvent): value is VisualEvent {
  return isRecord(value)
    && Number.isSafeInteger(value.sequence) && value.sequence > 0
    && typeof value.key === "string" && value.key.length > 0
    && typeof value.activityId === "string" && value.activityId.length > 0
    && (VISUAL_EVENT_KINDS as readonly string[]).includes(value.kind)
    && isRecord(value.target) && typeof value.target.type === "string" && typeof value.target.id === "string" && value.target.id.length > 0;
}

function normalizeFilter(filter: ReplayFilter | undefined): ReplayFilter {
  if (!filter) return {};
  return {
    ...(typeof filter.actorId === "string" && filter.actorId ? { actorId: filter.actorId } : {}),
    ...(typeof filter.taskId === "string" && filter.taskId ? { taskId: filter.taskId } : {}),
    ...(typeof filter.runId === "string" && filter.runId ? { runId: filter.runId } : {}),
  };
}

function normalizeRate(rate: number): number {
  return Number.isFinite(rate) ? Math.min(MAX_RATE, Math.max(MIN_RATE, rate)) : DEFAULT_RATE;
}

function safeSequence(sequence: number): number {
  return Number.isFinite(sequence) && sequence >= 0 ? sequence : 0;
}

function lastSequence(model: ReplayModel): number {
  return model.events.at(-1)?.sequence ?? model.snapshotSequence;
}

function clampSequence(sequence: number, minimum: number, maximum: number): number {
  const value = Number.isFinite(sequence) ? sequence : minimum;
  return Math.min(maximum, Math.max(minimum, value));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}
