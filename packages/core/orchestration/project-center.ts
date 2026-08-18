import { z } from "zod";

const id = z.string().min(1);
const revision = z.number().int().nonnegative().safe();
const count = z.number().int().nonnegative().safe();
const timestamp = z.string().datetime({ offset: true });
const missionStatus = z.enum(["draft", "ready", "running", "blocked", "completed", "failed", "cancelled"]);
const duty = z.enum(["planner", "executor", "reviewer", "integrator"]);
const budgetStatus = z.enum(["unlimited", "ok", "approved", "approval_required", "budget_exceeded"]);
const projectStatus = z.enum(["planned", "in_progress", "paused", "completed", "cancelled"]);
const runtimeStatus = z.enum(["online", "offline"]);
const attentionKind = z.enum(["mission_blocked", "budget_approval", "budget_exceeded", "review_pending", "run_failed", "dispatch_timeout", "run_timeout", "plan_proposal_pending", "human_gate", "runtime_offline"]);
const severity = z.enum(["critical", "high", "attention"]);
const subjectType = z.enum(["mission", "task_node", "run", "artifact", "human_gate", "runtime"]);
const actionKind = z.enum(["inspect", "approve_budget", "approve_plan", "reject_plan", "retry_task", "resolve_human_gate", "reassign_task", "cancel_mission"]);
const risk = z.enum(["low", "medium", "high"]);
const requiredPermission = z.enum(["project:read", "mission:command"]);

const budgetWire = z.object({
  status: budgetStatus, gate: z.string().optional(), max_tokens: z.number().int().nonnegative().safe().optional(),
  max_cost_usd_ticks: z.number().int().nonnegative().safe().optional(), consumed_tokens: count,
  reserved_tokens: count, consumed_cost_usd_ticks: count, reserved_cost_usd_ticks: count, grant_tokens: count,
  grant_cost_usd_ticks: count, approved_by: id.optional(), approved_at: timestamp.optional(),
}).strict();

const missionWire = z.object({
  id, title: z.string(), status: missionStatus, current_phase: z.string(),
  progress: z.object({ completed: count, total: count, percent: z.number().int().min(0).max(100).safe() }).strict(),
  budget: budgetWire, revision, last_sequence: revision, updated_at: timestamp,
  pending_human_gates: count, pending_reviews: count, pending_plan_proposals: count, offline_agents: count,
  active_runs: count, queued_runs: count,
}).strict();

const actionWire = z.object({ kind: actionKind, enabled: z.boolean(), risk, reason_code: id, required_permission: requiredPermission }).strict();
const attentionWire = z.object({
  id, mission_id: id, kind: attentionKind, severity, subject_type: subjectType, subject_id: id,
  task_node_id: id.optional(), run_id: id.optional(), artifact_id: id.optional(), gate_id: id.optional(),
  mission_revision: revision, task_revision: revision.optional(), gate_revision: revision.optional(), actions: z.array(actionWire),
}).strict();

const capacityEntryWire = z.object({
  id, name: z.string(), status: runtimeStatus, duties: z.array(duty), active_mission_ids: z.array(id), active_runs: count, queued_runs: count,
}).strict();
const capacityWire = z.object({ agents: z.array(capacityEntryWire), runtimes: z.array(capacityEntryWire) }).strict();
const totalsWire = z.object({
  mission_count: count, active_missions: count, blocked_missions: count, completed_missions: count, attention_count: count,
  active_runs: count, queued_runs: count, offline_agents: count, pending_human_gates: count, pending_reviews: count,
  consumed_tokens: count, reserved_tokens: count, consumed_cost_usd_ticks: count, reserved_cost_usd_ticks: count,
}).strict();

const wireSchema = z.object({
  project: z.object({ id, title: z.string(), status: projectStatus, updated_at: timestamp }).strict(),
  generated_at: timestamp, truncated: z.boolean(), missions: z.array(missionWire), attention: z.array(attentionWire),
  capacity: capacityWire, totals: totalsWire,
}).strict();

export interface ProjectBudgetSummary {
  status: z.infer<typeof budgetStatus>; gate?: string; maxTokens?: number; maxCostUsdTicks?: number;
  consumedTokens: number; reservedTokens: number; consumedCostUsdTicks: number; reservedCostUsdTicks: number;
  grantTokens: number; grantCostUsdTicks: number; approvedBy?: string; approvedAt?: string;
}
export interface ProjectMissionSummary {
  id: string; title: string; status: z.infer<typeof missionStatus>; currentPhase: string;
  progress: { completed: number; total: number; percent: number }; budget: ProjectBudgetSummary; revision: number; lastSequence: number; updatedAt: string;
  pendingHumanGates: number; pendingReviews: number; pendingPlanProposals: number; offlineAgents: number; activeRuns: number; queuedRuns: number;
}
export interface ProjectOwnerAction { kind: z.infer<typeof actionKind>; enabled: boolean; risk: z.infer<typeof risk>; reasonCode: string; requiredPermission: z.infer<typeof requiredPermission> }
export interface ProjectAttention {
  id: string; missionId: string; kind: z.infer<typeof attentionKind>; severity: z.infer<typeof severity>; subjectType: z.infer<typeof subjectType>; subjectId: string;
  taskNodeId?: string; runId?: string; artifactId?: string; gateId?: string; missionRevision: number; taskRevision?: number; gateRevision?: number; actions: ProjectOwnerAction[];
}
export interface ProjectCapacityEntry { id: string; name: string; status: z.infer<typeof runtimeStatus>; duties: z.infer<typeof duty>[]; activeMissionIds: string[]; activeRuns: number; queuedRuns: number }
export interface ProjectCommandCenterTotals { missionCount: number; activeMissions: number; blockedMissions: number; completedMissions: number; attentionCount: number; activeRuns: number; queuedRuns: number; offlineAgents: number; pendingHumanGates: number; pendingReviews: number; consumedTokens: number; reservedTokens: number; consumedCostUsdTicks: number; reservedCostUsdTicks: number }
export interface ProjectCommandCenterProjection {
  project: { id: string; title: string; status: z.infer<typeof projectStatus>; updatedAt: string };
  generatedAt: string; truncated: boolean; missions: ProjectMissionSummary[]; attention: ProjectAttention[];
  capacity: { agents: ProjectCapacityEntry[]; runtimes: ProjectCapacityEntry[] }; totals: ProjectCommandCenterTotals;
}

function budgetFromWire(value: z.infer<typeof budgetWire>): ProjectBudgetSummary {
  return { status: value.status, gate: value.gate, maxTokens: value.max_tokens, maxCostUsdTicks: value.max_cost_usd_ticks, consumedTokens: value.consumed_tokens, reservedTokens: value.reserved_tokens, consumedCostUsdTicks: value.consumed_cost_usd_ticks, reservedCostUsdTicks: value.reserved_cost_usd_ticks, grantTokens: value.grant_tokens, grantCostUsdTicks: value.grant_cost_usd_ticks, approvedBy: value.approved_by, approvedAt: value.approved_at };
}
function missionFromWire(value: z.infer<typeof missionWire>): ProjectMissionSummary {
  return { id: value.id, title: value.title, status: value.status, currentPhase: value.current_phase, progress: value.progress, budget: budgetFromWire(value.budget), revision: value.revision, lastSequence: value.last_sequence, updatedAt: value.updated_at, pendingHumanGates: value.pending_human_gates, pendingReviews: value.pending_reviews, pendingPlanProposals: value.pending_plan_proposals, offlineAgents: value.offline_agents, activeRuns: value.active_runs, queuedRuns: value.queued_runs };
}
function attentionFromWire(value: z.infer<typeof attentionWire>): ProjectAttention {
  return { id: value.id, missionId: value.mission_id, kind: value.kind, severity: value.severity, subjectType: value.subject_type, subjectId: value.subject_id, taskNodeId: value.task_node_id, runId: value.run_id, artifactId: value.artifact_id, gateId: value.gate_id, missionRevision: value.mission_revision, taskRevision: value.task_revision, gateRevision: value.gate_revision, actions: value.actions.map((action) => ({ kind: action.kind, enabled: action.enabled, risk: action.risk, reasonCode: action.reason_code, requiredPermission: action.required_permission })) };
}
function capacityFromWire(value: z.infer<typeof capacityEntryWire>): ProjectCapacityEntry {
  return { id: value.id, name: value.name, status: value.status, duties: value.duties, activeMissionIds: value.active_mission_ids, activeRuns: value.active_runs, queuedRuns: value.queued_runs };
}

export const ProjectCommandCenterProjectionWireSchema = wireSchema;
export const ProjectCommandCenterProjectionSchema = wireSchema.transform((value): ProjectCommandCenterProjection => ({
  project: { id: value.project.id, title: value.project.title, status: value.project.status, updatedAt: value.project.updated_at },
  generatedAt: value.generated_at, truncated: value.truncated, missions: value.missions.map(missionFromWire), attention: value.attention.map(attentionFromWire),
  capacity: { agents: value.capacity.agents.map(capacityFromWire), runtimes: value.capacity.runtimes.map(capacityFromWire) },
  totals: { missionCount: value.totals.mission_count, activeMissions: value.totals.active_missions, blockedMissions: value.totals.blocked_missions, completedMissions: value.totals.completed_missions, attentionCount: value.totals.attention_count, activeRuns: value.totals.active_runs, queuedRuns: value.totals.queued_runs, offlineAgents: value.totals.offline_agents, pendingHumanGates: value.totals.pending_human_gates, pendingReviews: value.totals.pending_reviews, consumedTokens: value.totals.consumed_tokens, reservedTokens: value.totals.reserved_tokens, consumedCostUsdTicks: value.totals.consumed_cost_usd_ticks, reservedCostUsdTicks: value.totals.reserved_cost_usd_ticks },
}));
export type ProjectCommandCenterWire = z.input<typeof ProjectCommandCenterProjectionWireSchema>;
export function parseProjectCommandCenterProjection(value: unknown): ProjectCommandCenterProjection | null {
  const result = ProjectCommandCenterProjectionSchema.safeParse(value);
  return result.success ? result.data : null;
}
