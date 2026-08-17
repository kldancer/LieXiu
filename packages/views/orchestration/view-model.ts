import type {
  ActivityProjection,
  MissionStatus,
  TaskNodeProjection,
  TaskNodeStatus,
  TeamMemberProjection,
} from "@liexiu/core/orchestration";

export type BoardLane = "queued" | "active" | "review" | "done" | "attention";
export type WorldZone = "lobby" | "workshop" | "reviewLab" | "delivery" | "blocked";
export type PixelActorState = "idle" | "walking" | "working" | "reviewing" | "blocked" | "done";
export type PixelActorAction = "none" | "move" | "work" | "inspect" | "celebrate" | "alert";

export interface DagNodeView {
  id: string;
  key: string;
  title: string;
  status: TaskNodeStatus;
  dependencyKeys: string[];
  depth: number;
}

export interface PixelActorView {
  agent: TeamMemberProjection;
  node?: TaskNodeProjection;
  zone: WorldZone;
  state: PixelActorState;
  slot: number;
  paletteIndex: number;
  action: PixelActorAction;
}

export const BOARD_LANES: BoardLane[] = ["queued", "active", "review", "done", "attention"];
export const WORLD_ZONES: WorldZone[] = ["lobby", "workshop", "reviewLab", "delivery", "blocked"];

export function boardLaneForStatus(status: TaskNodeStatus): BoardLane {
  switch (status) {
    case "pending":
    case "ready":
      return "queued";
    case "assigned":
    case "running":
    case "rework":
      return "active";
    case "review":
      return "review";
    case "completed":
      return "done";
    case "blocked":
    case "failed":
    case "cancelled":
    default:
      return "attention";
  }
}

export function worldZoneForStatus(status: TaskNodeStatus): WorldZone {
  switch (status) {
    case "assigned":
    case "running":
    case "rework":
      return "workshop";
    case "review":
      return "reviewLab";
    case "completed":
      return "delivery";
    case "blocked":
    case "failed":
    case "cancelled":
      return "blocked";
    case "pending":
    case "ready":
    default:
      return "lobby";
  }
}

export function pixelActorState(status: TaskNodeStatus | undefined, missionStatus: MissionStatus): PixelActorState {
  if (missionStatus === "completed") return "done";
  switch (status) {
    case "assigned":
      return "walking";
    case "running":
    case "rework":
      return "working";
    case "review":
      return "reviewing";
    case "blocked":
    case "failed":
    case "cancelled":
      return "blocked";
    case "completed":
      return "done";
    case "pending":
    case "ready":
    default:
      return "idle";
  }
}

export function nodesById(nodes: TaskNodeProjection[]): Map<string, TaskNodeProjection> {
  return new Map(nodes.map((node) => [node.id, node]));
}

/** Deterministic, cycle-safe DAG columns derived only from TaskNode edges. */
export function buildDagLayout(nodes: TaskNodeProjection[]): DagNodeView[][] {
  const ordered = [...nodes].sort(compareNodes);
  const known = new Set(ordered.map((node) => node.id));
  const depth = new Map<string, number>();
  let remaining = ordered;

  while (remaining.length > 0) {
    const ready = remaining.filter((node) =>
      node.dependencyIds.filter((id) => known.has(id)).every((id) => depth.has(id)),
    );
    if (ready.length === 0) {
      // A malformed cycle remains visible in a deterministic final column.
      const fallbackDepth = Math.max(-1, ...depth.values()) + 1;
      for (const node of remaining) depth.set(node.id, fallbackDepth);
      break;
    }
    for (const node of ready) {
      const dependencies = node.dependencyIds
        .filter((id) => known.has(id))
        .map((id) => depth.get(id) ?? 0);
      depth.set(node.id, dependencies.length === 0 ? 0 : Math.max(...dependencies) + 1);
    }
    const readyIds = new Set(ready.map((node) => node.id));
    remaining = remaining.filter((node) => !readyIds.has(node.id));
  }

  const byId = nodesById(ordered);
  const columns: DagNodeView[][] = [];
  for (const node of ordered) {
    const column = depth.get(node.id) ?? 0;
    columns[column] ??= [];
    columns[column].push({
      id: node.id,
      key: node.key,
      title: node.title,
      status: node.status,
      dependencyKeys: node.dependencyIds
        .map((id) => byId.get(id)?.key ?? id)
        .sort((left, right) => left.localeCompare(right)),
      depth: column,
    });
  }
  return columns;
}

/** Stable actor placement: refreshes and input ordering produce the same map. */
export function buildPixelActors(
  team: TeamMemberProjection[],
  nodes: TaskNodeProjection[],
  missionStatus: MissionStatus,
  activities: ActivityProjection[] = [],
): PixelActorView[] {
  const nodeLookup = nodesById(nodes);
  const zoneCounts = new Map<WorldZone, number>();
  const latestActivityByAgent = new Map<string, ActivityProjection>();
  for (const activity of [...activities].sort((left, right) => left.sequence - right.sequence)) {
    if (activity.actorType === "agent" && activity.actorId) {
      latestActivityByAgent.set(activity.actorId, activity);
    }
  }
  return [...team]
    .sort((left, right) =>
      `${left.role}\u0000${left.agentName}\u0000${left.agentId}`.localeCompare(
        `${right.role}\u0000${right.agentName}\u0000${right.agentId}`,
      ),
    )
    .map((agent) => {
      const node = [...agent.currentNodeIds]
        .sort()
        .map((id) => nodeLookup.get(id))
        .find(Boolean);
      const zone = missionStatus === "completed"
        ? "delivery"
        : node
          ? worldZoneForStatus(node.status)
          : "lobby";
      const slot = zoneCounts.get(zone) ?? 0;
      zoneCounts.set(zone, slot + 1);
      return {
        agent,
        node,
        zone,
        state: pixelActorState(node?.status, missionStatus),
        slot,
        paletteIndex: stableHash(agent.agentId) % 5,
        action: pixelActionForActivity(latestActivityByAgent.get(agent.agentId)?.type),
      };
    });
}

export function pixelActionForActivity(type: string | undefined): PixelActorAction {
  const normalized = type?.toLowerCase() ?? "";
  if (/failed|blocked|cancelled/.test(normalized)) return "alert";
  if (/completed|succeeded|approved|delivered/.test(normalized)) return "celebrate";
  if (/review|verdict|inspect/.test(normalized)) return "inspect";
  if (/started|assigned|dispatch/.test(normalized)) return "move";
  if (/message|artifact|usage|progress|working/.test(normalized)) return "work";
  return "none";
}

function compareNodes(left: TaskNodeProjection, right: TaskNodeProjection) {
  return `${left.key}\u0000${left.id}`.localeCompare(`${right.key}\u0000${right.id}`);
}

function stableHash(value: string) {
  let hash = 0;
  for (const character of value) hash = ((hash << 5) - hash + character.charCodeAt(0)) >>> 0;
  return hash;
}
