import type { WorldActorModel, WorldArtifactModel, WorldSignalModel, WorldZone } from "./world-model";
import type { VisualEvent, VisualEventPriority } from "./visual-events";

export type WorldPerformanceMode = "default" | "low";

export interface WorldPerformanceBudget {
  actors: number;
  artifacts: number;
  signals: number;
  visualEvents: number;
}

export const WORLD_PERFORMANCE_BUDGETS: Readonly<Record<WorldPerformanceMode, WorldPerformanceBudget>> = {
  default: { actors: 48, artifacts: 64, signals: 96, visualEvents: 128 },
  low: { actors: 24, artifacts: 32, signals: 48, visualEvents: 64 },
};

export interface WorldPerformanceInput {
  actors: readonly WorldActorModel[];
  artifacts: readonly WorldArtifactModel[];
  signals: readonly WorldSignalModel[];
  visualEvents: readonly VisualEvent[];
  /** Omit when the whole fixed map is visible; provide camera-visible Zones to cull offscreen entities. */
  visibleZones?: readonly WorldZone[];
}

export interface WorldPerformanceResult extends WorldPerformanceInput {
  mode: WorldPerformanceMode;
  budget: WorldPerformanceBudget;
  degraded: boolean;
  dropped: { actors: number; artifacts: number; signals: number; visualEvents: number };
}

const PRIORITY: Readonly<Record<VisualEventPriority, number>> = { critical: 0, high: 1, normal: 2, low: 3 };

/** Applies deterministic, renderer-independent backpressure to a world snapshot and event window. */
export function applyWorldPerformanceBudget(
  input: WorldPerformanceInput,
  mode: WorldPerformanceMode = "default",
  budget: WorldPerformanceBudget = WORLD_PERFORMANCE_BUDGETS[mode],
): WorldPerformanceResult {
  const selectedMode = mode === "low" ? "low" : "default";
  const effectiveBudget = { ...WORLD_PERFORMANCE_BUDGETS[selectedMode], ...budget };
  const visibleZones = input.visibleZones ? new Set(input.visibleZones) : undefined;
  const visibleActors = visibleZones ? input.actors.filter((item) => visibleZones.has(item.zone)) : input.actors;
  const visibleArtifacts = visibleZones ? input.artifacts.filter((item) => visibleZones.has(item.zone)) : input.artifacts;
  const visibleSignals = visibleZones ? input.signals.filter((item) => visibleZones.has(item.zone)) : input.signals;
  const actors = retain(visibleActors, effectiveBudget.actors, (item) => item.id);
  const artifacts = retain(visibleArtifacts, effectiveBudget.artifacts, (item) => item.id);
  const signals = retain(visibleSignals, effectiveBudget.signals, (item) => item.id, signalRank);
  const visualEvents = retainUniqueEvents(input.visualEvents, effectiveBudget.visualEvents);
  const dropped = {
    actors: input.actors.length - actors.length,
    artifacts: input.artifacts.length - artifacts.length,
    signals: input.signals.length - signals.length,
    visualEvents: input.visualEvents.length - visualEvents.length,
  };
  return { actors, artifacts, signals, visualEvents, visibleZones: input.visibleZones, mode: selectedMode, budget: effectiveBudget, dropped, degraded: Object.values(dropped).some((count) => count > 0) };
}

export const budgetWorld = applyWorldPerformanceBudget;

function retain<T>(items: readonly T[], limit: number, id: (item: T) => string, rank?: (item: T) => number): T[] {
  const safeLimit = Math.max(0, Math.floor(limit));
  return [...items].sort((left, right) => (rank?.(left) ?? 0) - (rank?.(right) ?? 0) || compareText(id(left), id(right))).slice(0, safeLimit);
}

function retainUniqueEvents(items: readonly VisualEvent[], limit: number): VisualEvent[] {
  const unique = new Map<number, VisualEvent>();
  for (const event of [...items].sort(compareEvents)) if (!unique.has(event.sequence)) unique.set(event.sequence, event);
  return retain([...unique.values()], limit, (event) => event.key, (event) => PRIORITY[event.priority]).sort((a, b) => a.sequence - b.sequence || compareText(a.key, b.key));
}

function signalRank(signal: WorldSignalModel): number { return signal.severity === "critical" ? 0 : signal.severity === "attention" ? 1 : 2; }
function compareEvents(left: VisualEvent, right: VisualEvent): number { return left.sequence - right.sequence || PRIORITY[left.priority] - PRIORITY[right.priority] || compareText(left.key, right.key); }
function compareText(left: string, right: string): number { return left < right ? -1 : left > right ? 1 : 0; }
