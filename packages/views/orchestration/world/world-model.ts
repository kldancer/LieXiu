import type {
  MissionProjection,
  MissionStatus,
  OrchestrationDuty,
  RunProjection,
  TaskNodeProjection,
  TeamMemberProjection,
} from "@liexiu/core/orchestration";

export const WORLD_ZONES = [
  "planning_observatory",
  "execution_workshop",
  "review_archive",
  "integration_forge",
  "blocked_corner",
  "delivery_plaza",
] as const;

export type WorldZone = (typeof WORLD_ZONES)[number];
export type WorldDuty = OrchestrationDuty;
export type WorldActorStatus = "idle" | "running" | "blocked" | "offline" | "delivered";
export type WorldArtifactStatus = "produced" | "approved" | "changes_requested" | "rejected" | "delivered";
export type WorldSignalKind = "collaboration" | "review" | "blocked" | "offline" | "budget" | "human_gate";
export type WorldSignalSeverity = "info" | "attention" | "critical";

export interface WorldZoneModel {
  id: WorldZone;
  actorIds: string[];
}

export interface WorldActorModel {
  id: string;
  agentId: string;
  runtimeId: string;
  name: string;
  duty: WorldDuty;
  zone: WorldZone;
  status: WorldActorStatus;
  nodeId?: string;
  runId?: string;
  slot: number;
}

export interface WorldArtifactModel {
  id: string;
  runId: string;
  nodeId?: string;
  kind: string;
  version: number;
  zone: WorldZone;
  status: WorldArtifactStatus;
  slot: number;
}

/** A bounded pointer to a canonical fact. Display text and Activity payloads stay in React. */
export interface WorldSignalModel {
  id: string;
  kind: WorldSignalKind;
  severity: WorldSignalSeverity;
  zone: WorldZone;
  actorId?: string;
  nodeId?: string;
  runId?: string;
  artifactId?: string;
  activityId?: string;
  sequence?: number;
  gateId?: string;
  slot: number;
}

export interface WorldModel {
  missionId: string;
  revision: number;
  missionStatus: MissionStatus;
  zones: WorldZoneModel[];
  actors: WorldActorModel[];
  artifacts: WorldArtifactModel[];
  signals: WorldSignalModel[];
}

const DUTY_ORDER: readonly WorldDuty[] = ["planner", "executor", "reviewer", "integrator"];
const ONLINE_RUNTIME_STATUSES = new Set(["online", "ready", "running", "active", "connected"]);

/**
 * Pure Projection -> WorldModel mapping. The result contains only stable domain
 * IDs and semantic state; it deliberately has no renderer or map coordinates.
 */
export function buildWorldModel(projection: MissionProjection | null | undefined): WorldModel {
  const mission = projection?.mission;
  const nodes = Array.isArray(projection?.nodes) ? projection.nodes : [];
  const team = Array.isArray(projection?.team) ? projection.team : [];
  const runs = Array.isArray(projection?.planning?.runs) ? projection.planning.runs : [];
  const assignments = Array.isArray(projection?.planning?.assignments) ? projection.planning.assignments : [];
  const nodeById = new Map(nodes.filter(isNode).map((node) => [node.id, node]));
  const runById = new Map(runs.filter(isRun).map((run) => [run.id, run]));
  const assignedNodeIds = new Map<string, string[]>();

  for (const assignment of assignments) {
    if (!isRecord(assignment) || typeof assignment.agentId !== "string" || typeof assignment.taskNodeId !== "string") continue;
    if (assignment.status !== "active") continue;
    const ids = assignedNodeIds.get(assignment.agentId) ?? [];
    ids.push(assignment.taskNodeId);
    assignedNodeIds.set(assignment.agentId, ids);
  }

  const actors = team.filter(isTeamMember).map((member): WorldActorModel | undefined => {
    const duty = normalizeDuty(member.duty);
    if (!duty) return undefined;
    const node = selectNode(member, nodeById, assignedNodeIds.get(member.agentId));
    const run = selectRun(node, runById);
    const status = actorStatus(mission?.status, mission?.budget?.status, member, node, run);
    const zone = zoneFor(duty, status, node?.status);
    const actor: WorldActorModel = {
      id: `${member.agentId}:${duty}:${member.runtimeId}`,
      agentId: member.agentId,
      runtimeId: member.runtimeId,
      name: typeof member.agentName === "string" && member.agentName ? member.agentName : member.agentId,
      duty,
      zone,
      status,
      slot: 0,
    };
    if (node) actor.nodeId = node.id;
    if (run) actor.runId = run.id;
    return actor;
  }).filter((actor): actor is WorldActorModel => actor !== undefined).sort(compareActors);

  const slotByZone = new Map<WorldZone, number>();
  for (const actor of actors) {
    actor.slot = slotByZone.get(actor.zone) ?? 0;
    slotByZone.set(actor.zone, actor.slot + 1);
  }

  const zones = WORLD_ZONES.map((id) => ({
    id,
    actorIds: actors.filter((actor) => actor.zone === id).map((actor) => actor.id),
  }));
  const artifacts = buildArtifacts(nodes, mission?.status);
  const signals = buildSignals(projection, actors, nodes, mission?.status, mission?.budget?.status);
  return {
    missionId: typeof mission?.id === "string" ? mission.id : "",
    revision: typeof mission?.revision === "number" && Number.isFinite(mission.revision) ? mission.revision : 0,
    missionStatus: normalizeMissionStatus(mission?.status),
    zones,
    actors,
    artifacts,
    signals,
  };
}

export const worldModelFromProjection = buildWorldModel;

function selectNode(member: TeamMemberProjection, nodeById: Map<string, TaskNodeProjection>, assignedIds: string[] | undefined) {
  const ids = [...(Array.isArray(member.currentNodeIds) ? member.currentNodeIds.filter((id): id is string => typeof id === "string") : []), ...(assignedIds ?? [])];
  return [...new Set(ids)].map((id) => nodeById.get(id)).filter((node): node is TaskNodeProjection => Boolean(node)).sort(compareNodes)[0];
}

function selectRun(node: TaskNodeProjection | undefined, runById: Map<string, RunProjection>) {
  if (!node?.latestRun) return undefined;
  return runById.get(node.latestRun.id) ?? node.latestRun;
}

function actorStatus(
  missionStatus: MissionStatus | undefined,
  budgetStatus: string | undefined,
  member: TeamMemberProjection,
  node: TaskNodeProjection | undefined,
  run: RunProjection | undefined,
): WorldActorStatus {
  if (missionStatus === "completed" || node?.status === "completed" || run?.status === "succeeded") return "delivered";
  if (missionStatus === "blocked" || budgetStatus === "budget_exceeded" || ["blocked", "failed", "cancelled"].includes(node?.status ?? "") || run?.status === "failed" || run?.status === "cancelled") return "blocked";
  if (!ONLINE_RUNTIME_STATUSES.has(normalizeString(member.runtimeStatus))) return "offline";
  if (["running", "dispatched"].includes(run?.status ?? "") || ["assigned", "running", "review", "rework"].includes(node?.status ?? "")) return "running";
  return "idle";
}

function zoneFor(duty: WorldDuty, status: WorldActorStatus, nodeStatus: string | undefined): WorldZone {
  if (status === "delivered") return "delivery_plaza";
  if (status === "blocked" || status === "offline") return "blocked_corner";
  if (nodeStatus === "review" || duty === "reviewer") return "review_archive";
  if (duty === "integrator") return "integration_forge";
  if (duty === "executor" || ["assigned", "running", "rework"].includes(nodeStatus ?? "")) return "execution_workshop";
  return "planning_observatory";
}

function buildArtifacts(nodes: TaskNodeProjection[], missionStatus: MissionStatus | undefined): WorldArtifactModel[] {
  const artifacts = nodes.flatMap((node): WorldArtifactModel[] => {
    const artifact = node.latestArtifact;
    if (!artifact || typeof artifact.id !== "string" || typeof artifact.runId !== "string") return [];
    const decision = node.latestVerdict?.artifactId === artifact.id ? node.latestVerdict.decision : undefined;
    const status: WorldArtifactStatus = missionStatus === "completed" || node.status === "completed"
      ? "delivered"
      : decision === "approved" || decision === "changes_requested" || decision === "rejected"
        ? decision
        : "produced";
    const zone: WorldZone = status === "delivered"
      ? "delivery_plaza"
      : status === "approved" && node.duty === "integrator"
        ? "integration_forge"
        : "review_archive";
    return [{
      id: artifact.id,
      runId: artifact.runId,
      nodeId: node.id,
      kind: artifact.kind,
      version: Number.isSafeInteger(artifact.version) ? artifact.version : 0,
      zone,
      status,
      slot: 0,
    }];
  }).sort((left, right) => compareText(left.id, right.id));
  const slotByZone = new Map<WorldZone, number>();
  for (const artifact of artifacts) {
    artifact.slot = slotByZone.get(artifact.zone) ?? 0;
    slotByZone.set(artifact.zone, artifact.slot + 1);
  }
  return artifacts;
}

function buildSignals(
  projection: MissionProjection | null | undefined,
  actors: WorldActorModel[],
  nodes: TaskNodeProjection[],
  missionStatus: MissionStatus | undefined,
  budgetStatus: string | undefined,
): WorldSignalModel[] {
  const signals: WorldSignalModel[] = [];
  for (const actor of actors) {
    if (actor.status === "offline") {
      signals.push({ id: `offline:${actor.id}`, kind: "offline", severity: "critical", zone: "blocked_corner", actorId: actor.id, nodeId: actor.nodeId, runId: actor.runId, slot: 0 });
    }
  }
  for (const node of nodes) {
    if (["blocked", "failed", "cancelled"].includes(node.status)) {
      signals.push({ id: `blocked:${node.id}`, kind: "blocked", severity: "critical", zone: "blocked_corner", nodeId: node.id, runId: node.latestRun?.id, artifactId: node.latestArtifact?.id, slot: 0 });
    }
    if (node.status === "review" || node.latestVerdict) {
      signals.push({
        id: node.latestVerdict?.id ? `review:${node.latestVerdict.id}` : `review:${node.id}`,
        kind: "review",
        severity: node.latestVerdict?.decision === "approved" ? "info" : "attention",
        zone: "review_archive",
        nodeId: node.id,
        runId: node.latestVerdict?.reviewRunId ?? node.latestRun?.id,
        artifactId: node.latestVerdict?.artifactId ?? node.latestArtifact?.id,
        slot: 0,
      });
    }
  }
  const missionId = typeof projection?.mission?.id === "string" ? projection.mission.id : "";
  if (budgetStatus && budgetStatus !== "ok") {
    signals.push({ id: `budget:${missionId}:${budgetStatus}`, kind: "budget", severity: budgetStatus === "budget_exceeded" ? "critical" : "attention", zone: "blocked_corner", slot: 0 });
  }
  if (missionStatus === "blocked" && !signals.some((signal) => signal.kind === "blocked")) {
    signals.push({ id: `blocked:${missionId}`, kind: "blocked", severity: "critical", zone: "blocked_corner", slot: 0 });
  }
  for (const gate of Array.isArray(projection?.humanGates) ? projection.humanGates : []) {
    if (!isRecord(gate) || typeof gate.id !== "string" || ["resolved", "closed", "cancelled"].includes(normalizeString(gate.status))) continue;
    signals.push({
      id: `human-gate:${gate.id}`,
      kind: "human_gate",
      severity: "critical",
      zone: gate.kind === "rework_limit_exceeded" ? "blocked_corner" : "planning_observatory",
      nodeId: typeof gate.taskNodeId === "string" ? gate.taskNodeId : undefined,
      runId: typeof gate.sourceRunId === "string" ? gate.sourceRunId : undefined,
      artifactId: typeof gate.artifactId === "string" ? gate.artifactId : undefined,
      gateId: gate.id,
      slot: 0,
    });
  }
  for (const activity of Array.isArray(projection?.activities?.items) ? projection.activities.items : []) {
    if (!isRecord(activity) || activity.subjectType !== "mailbox_message" || !/^mailbox\.message_(sent|consumed|expired|cancelled)$/.test(String(activity.type))) continue;
    if (typeof activity.id !== "string" || typeof activity.subjectId !== "string" || !Number.isSafeInteger(activity.sequence)) continue;
    const actor = actors.find((candidate) => candidate.agentId === activity.actorId || candidate.runId === activity.runId || candidate.nodeId === activity.taskNodeId);
    signals.push({
      id: `collaboration:${activity.subjectId}:${activity.sequence}`,
      kind: "collaboration",
      severity: activity.type === "mailbox.message_expired" || activity.type === "mailbox.message_cancelled" ? "attention" : "info",
      zone: actor?.zone ?? "execution_workshop",
      actorId: actor?.id,
      nodeId: typeof activity.taskNodeId === "string" ? activity.taskNodeId : undefined,
      runId: typeof activity.runId === "string" ? activity.runId : undefined,
      activityId: activity.id,
      sequence: activity.sequence,
      slot: 0,
    });
  }
  signals.sort((left, right) => (left.sequence ?? 0) - (right.sequence ?? 0) || compareText(left.id, right.id));
  const slotByZone = new Map<WorldZone, number>();
  for (const signal of signals) {
    signal.slot = slotByZone.get(signal.zone) ?? 0;
    slotByZone.set(signal.zone, signal.slot + 1);
  }
  return signals;
}

function normalizeDuty(value: unknown): WorldDuty | undefined {
  return DUTY_ORDER.includes(value as WorldDuty) ? (value as WorldDuty) : undefined;
}

function normalizeMissionStatus(value: unknown): MissionStatus {
  return ["draft", "ready", "running", "blocked", "completed", "failed", "cancelled"].includes(value as string)
    ? (value as MissionStatus)
    : "draft";
}

function normalizeString(value: unknown): string { return typeof value === "string" ? value.trim().toLowerCase() : ""; }
function compareText(left: string, right: string): number { return left < right ? -1 : left > right ? 1 : 0; }
function compareNodes(left: TaskNodeProjection, right: TaskNodeProjection): number { return compareText(left.id, right.id); }
function compareActors(left: WorldActorModel, right: WorldActorModel): number {
  return DUTY_ORDER.indexOf(left.duty) - DUTY_ORDER.indexOf(right.duty) || compareText(left.id, right.id);
}
function isRecord(value: unknown): value is Record<string, unknown> { return typeof value === "object" && value !== null; }
function isNode(value: unknown): value is TaskNodeProjection { return isRecord(value) && typeof value.id === "string" && typeof value.status === "string"; }
function isRun(value: unknown): value is RunProjection { return isRecord(value) && typeof value.id === "string" && typeof value.status === "string"; }
function isTeamMember(value: unknown): value is TeamMemberProjection { return isRecord(value) && typeof value.agentId === "string" && value.agentId.length > 0; }
