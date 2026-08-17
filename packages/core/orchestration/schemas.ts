import { z } from "zod";
import type {
  ActivityPage,
  ArtifactProjection,
  AssignmentProjection,
  MissionProjection,
  MissionBudgetApprovalResponse,
  MissionLifecycleResponse,
  RetryMissionTaskResponse,
  ReviewVerdictProjection,
  RunDetailProjection,
  RunProjection,
  TaskNodeProjection,
  TeamMemberProjection,
} from "./types";

const timestamp = z.string().default("");

const planLimitsWireSchema = z.object({
  max_parallel_runs: z.number().default(1),
  max_task_attempts: z.number().default(1),
  max_rework_cycles: z.number().default(1),
}).loose();

const missionBudgetWireSchema = z.object({
  status: z.string().default("unlimited"),
  gate: z.string().optional(),
  max_tokens: z.number().nullable().optional(),
  max_cost_usd_ticks: z.number().nullable().optional(),
  consumed_tokens: z.number().default(0),
  reserved_tokens: z.number().default(0),
  consumed_cost_usd_ticks: z.number().default(0),
  reserved_cost_usd_ticks: z.number().default(0),
  grant_tokens: z.number().default(0),
  grant_cost_usd_ticks: z.number().default(0),
  approved_by: z.string().optional(),
  approved_at: z.string().optional(),
}).loose();

const budgetEstimateWireSchema = z.object({
  tokens: z.number().default(0),
  cost_usd_ticks: z.number().default(0),
}).loose();

const assignmentWireSchema = z.object({
  id: z.string(),
  task_node_id: z.string(),
  role: z.string().default("executor"),
  agent_id: z.string(),
  runtime_id: z.string(),
  status: z.string().default("revoked"),
  sequence: z.number().default(1),
  supersedes_id: z.string().optional(),
  created_at: timestamp,
  ended_at: z.string().optional(),
}).loose();

const runWireSchema = z.object({
  id: z.string(),
  task_node_id: z.string(),
  assignment_id: z.string(),
  agent_task_id: z.string().optional(),
  purpose: z.string().default("execute"),
  attempt: z.number().default(1),
  status: z.string().default("queued"),
  input: z.unknown().default({}),
  retry_of_id: z.string().optional(),
  failure_kind: z.string().optional(),
  failure_message: z.string().optional(),
  dispatch_deadline_at: timestamp,
  timeout_seconds: z.number().default(0),
  started_at: z.string().optional(),
  finished_at: z.string().optional(),
  created_at: timestamp,
}).loose();

const artifactWireSchema = z.object({
  id: z.string(),
  task_node_id: z.string(),
  run_id: z.string(),
  kind: z.string().default("file"),
  version: z.number().default(1),
  uri: z.string().default(""),
  content_hash: z.string().optional(),
  summary: z.string().default(""),
  metadata: z.unknown().default({}),
  created_at: timestamp,
}).loose();

const verdictWireSchema = z.object({
  id: z.string(),
  task_node_id: z.string(),
  review_run_id: z.string(),
  artifact_id: z.string(),
  decision: z.string().default("rejected"),
  evidence: z.unknown().default({}),
  requested_changes: z.array(z.unknown()).default([]),
  created_at: timestamp,
}).loose();

const nodeWireSchema = z.object({
  id: z.string(),
  key: z.string().default(""),
  title: z.string().default(""),
  description: z.string().default(""),
  role: z.string().default("executor"),
  status: z.string().default("pending"),
  dependency_ids: z.array(z.string()).default([]),
  acceptance_criteria: z.array(z.string()).default([]),
  artifact_kinds: z.array(z.string()).default([]),
  block_reason: z.string().optional(),
  rework_count: z.number().default(0),
  revision: z.number().default(0),
  budget_estimate: budgetEstimateWireSchema.optional(),
  active_assignment: assignmentWireSchema.optional(),
  latest_run: runWireSchema.optional(),
  latest_artifact: artifactWireSchema.optional(),
  latest_verdict: verdictWireSchema.optional(),
}).loose();

const teamMemberWireSchema = z.object({
  agent_id: z.string(),
  agent_name: z.string().default(""),
  avatar_url: z.string().optional(),
  role: z.string().default("executor"),
  runtime_id: z.string(),
  runtime_name: z.string().default(""),
  runtime_status: z.string().default("offline"),
  provider: z.string().default(""),
  model: z.string().optional(),
  capabilities: z.unknown().default([]),
  current_node_ids: z.array(z.string()).default([]),
}).loose();

const activityWireSchema = z.object({
  id: z.string(),
  task_node_id: z.string().optional(),
  run_id: z.string().optional(),
  type: z.string().default(""),
  actor_type: z.string().default("orchestrator"),
  actor_id: z.string().optional(),
  subject_type: z.string().default("mission"),
  subject_id: z.string().default(""),
  causation_id: z.string().default(""),
  correlation_id: z.string().default(""),
  payload_version: z.number().default(1),
  payload: z.unknown().default({}),
  sequence: z.number().default(0),
  occurred_at: timestamp,
}).loose();

function assignmentFromWire(value: z.infer<typeof assignmentWireSchema>): AssignmentProjection {
  return {
    id: value.id,
    taskNodeId: value.task_node_id,
    role: value.role as AssignmentProjection["role"],
    agentId: value.agent_id,
    runtimeId: value.runtime_id,
    status: value.status as AssignmentProjection["status"],
    sequence: value.sequence,
    supersedesId: value.supersedes_id,
    createdAt: value.created_at,
    endedAt: value.ended_at,
  };
}

function runFromWire(value: z.infer<typeof runWireSchema>): RunProjection {
  return {
    id: value.id,
    taskNodeId: value.task_node_id,
    assignmentId: value.assignment_id,
    agentTaskId: value.agent_task_id,
    purpose: value.purpose,
    attempt: value.attempt,
    status: value.status as RunProjection["status"],
    input: value.input,
    retryOfId: value.retry_of_id,
    failureKind: value.failure_kind,
    failureMessage: value.failure_message,
    dispatchDeadlineAt: value.dispatch_deadline_at,
    timeoutSeconds: value.timeout_seconds,
    startedAt: value.started_at,
    finishedAt: value.finished_at,
    createdAt: value.created_at,
  };
}

function artifactFromWire(value: z.infer<typeof artifactWireSchema>): ArtifactProjection {
  return {
    id: value.id,
    taskNodeId: value.task_node_id,
    runId: value.run_id,
    kind: value.kind,
    version: value.version,
    uri: value.uri,
    contentHash: value.content_hash,
    summary: value.summary,
    metadata: value.metadata,
    createdAt: value.created_at,
  };
}

function verdictFromWire(value: z.infer<typeof verdictWireSchema>): ReviewVerdictProjection {
  return {
    id: value.id,
    taskNodeId: value.task_node_id,
    reviewRunId: value.review_run_id,
    artifactId: value.artifact_id,
    decision: value.decision as ReviewVerdictProjection["decision"],
    evidence: value.evidence,
    requestedChanges: value.requested_changes,
    createdAt: value.created_at,
  };
}

function teamMemberFromWire(value: z.infer<typeof teamMemberWireSchema>): TeamMemberProjection {
  return {
    agentId: value.agent_id,
    agentName: value.agent_name,
    avatarUrl: value.avatar_url,
    role: value.role as TeamMemberProjection["role"],
    runtimeId: value.runtime_id,
    runtimeName: value.runtime_name,
    runtimeStatus: value.runtime_status,
    provider: value.provider,
    model: value.model,
    capabilities: value.capabilities,
    currentNodeIds: value.current_node_ids,
  };
}

function nodeFromWire(value: z.infer<typeof nodeWireSchema>): TaskNodeProjection {
  return {
    id: value.id,
    key: value.key,
    title: value.title,
    description: value.description,
    role: value.role as TaskNodeProjection["role"],
    status: value.status as TaskNodeProjection["status"],
    dependencyIds: value.dependency_ids,
    acceptanceCriteria: value.acceptance_criteria,
    artifactKinds: value.artifact_kinds,
    blockReason: value.block_reason,
    reworkCount: value.rework_count,
    revision: value.revision,
    budgetEstimate: value.budget_estimate
      ? { tokens: value.budget_estimate.tokens, costUsdTicks: value.budget_estimate.cost_usd_ticks }
      : undefined,
    activeAssignment: value.active_assignment ? assignmentFromWire(value.active_assignment) : undefined,
    latestRun: value.latest_run ? runFromWire(value.latest_run) : undefined,
    latestArtifact: value.latest_artifact ? artifactFromWire(value.latest_artifact) : undefined,
    latestVerdict: value.latest_verdict ? verdictFromWire(value.latest_verdict) : undefined,
  };
}

function activityFromWire(value: z.infer<typeof activityWireSchema>) {
  return {
    id: value.id,
    taskNodeId: value.task_node_id,
    runId: value.run_id,
    type: value.type,
    actorType: value.actor_type,
    actorId: value.actor_id,
    subjectType: value.subject_type,
    subjectId: value.subject_id,
    causationId: value.causation_id,
    correlationId: value.correlation_id,
    payloadVersion: value.payload_version,
    payload: value.payload,
    sequence: value.sequence,
    occurredAt: value.occurred_at,
  };
}

export const MissionProjectionSchema = z.object({
  mission: z.object({
    id: z.string(),
    title: z.string().default(""),
    description: z.string().default(""),
    status: z.string().default("failed"),
    current_phase: z.string().default("planning"),
    progress: z.object({ completed: z.number().default(0), total: z.number().default(0), percent: z.number().default(0) }).loose(),
    limits: planLimitsWireSchema,
    budget: missionBudgetWireSchema,
    revision: z.number().default(0),
    last_sequence: z.number().default(0),
    created_at: timestamp,
    updated_at: timestamp,
  }).loose(),
  nodes: z.array(nodeWireSchema).default([]),
  team: z.array(teamMemberWireSchema).default([]),
  activities: z.object({
    items: z.array(activityWireSchema).default([]),
    first_sequence: z.number().default(0),
    last_sequence: z.number().default(0),
    has_previous: z.boolean().default(false),
  }).loose(),
}).loose().transform((value): MissionProjection => ({
  mission: {
    id: value.mission.id,
    title: value.mission.title,
    description: value.mission.description,
    status: value.mission.status as MissionProjection["mission"]["status"],
    currentPhase: value.mission.current_phase,
    progress: value.mission.progress,
    limits: {
      maxParallelRuns: value.mission.limits.max_parallel_runs,
      maxTaskAttempts: value.mission.limits.max_task_attempts,
      maxReworkCycles: value.mission.limits.max_rework_cycles,
    },
    budget: {
      status: value.mission.budget.status as MissionProjection["mission"]["budget"]["status"],
      gate: value.mission.budget.gate,
      maxTokens: value.mission.budget.max_tokens ?? undefined,
      maxCostUsdTicks: value.mission.budget.max_cost_usd_ticks ?? undefined,
      consumedTokens: value.mission.budget.consumed_tokens,
      reservedTokens: value.mission.budget.reserved_tokens,
      consumedCostUsdTicks: value.mission.budget.consumed_cost_usd_ticks,
      reservedCostUsdTicks: value.mission.budget.reserved_cost_usd_ticks,
      grantTokens: value.mission.budget.grant_tokens,
      grantCostUsdTicks: value.mission.budget.grant_cost_usd_ticks,
      approvedBy: value.mission.budget.approved_by,
      approvedAt: value.mission.budget.approved_at,
    },
    revision: value.mission.revision,
    lastSequence: value.mission.last_sequence,
    createdAt: value.mission.created_at,
    updatedAt: value.mission.updated_at,
  },
  nodes: value.nodes.map(nodeFromWire),
  team: value.team.map(teamMemberFromWire),
  activities: {
    items: value.activities.items.map(activityFromWire),
    firstSequence: value.activities.first_sequence,
    lastSequence: value.activities.last_sequence,
    hasPrevious: value.activities.has_previous,
  },
}));

export const ActivityPageSchema = z.object({
  items: z.array(activityWireSchema).default([]),
  after_sequence: z.number().default(0),
  next_after_sequence: z.number().default(0),
  last_sequence: z.number().default(0),
  has_more: z.boolean().default(false),
  reset_required: z.boolean().default(false),
}).loose().transform((value): ActivityPage => ({
  items: value.items.map(activityFromWire),
  afterSequence: value.after_sequence,
  nextAfterSequence: value.next_after_sequence,
  lastSequence: value.last_sequence,
  hasMore: value.has_more,
  resetRequired: value.reset_required,
}));

const executionWireSchema = z.object({
  agent_task_id: z.string(),
  status: z.string().default(""),
  session_id: z.string().optional(),
  result: z.unknown().optional(),
  error: z.string().optional(),
  created_at: timestamp,
  started_at: z.string().optional(),
  completed_at: z.string().optional(),
}).loose();

export const RunDetailProjectionSchema = z.object({
  mission_id: z.string(),
  node: nodeWireSchema,
  run: runWireSchema,
  assignment: assignmentWireSchema,
  agent: teamMemberWireSchema.optional(),
  execution: executionWireSchema.optional(),
  messages: z.array(z.object({
    sequence: z.number().default(0), type: z.string().default(""), tool: z.string().optional(),
    content: z.string().optional(), input: z.unknown().optional(), output: z.string().optional(), created_at: timestamp,
  }).loose()).default([]),
  usage: z.array(z.object({
    provider: z.string().default(""), model: z.string().default(""), input_tokens: z.number().default(0),
    output_tokens: z.number().default(0), cache_read_tokens: z.number().default(0), cache_write_tokens: z.number().default(0),
    cost_usd_ticks: z.number().nullable().optional(), created_at: timestamp,
  }).loose()).default([]),
  artifacts: z.array(artifactWireSchema).default([]),
  reviews: z.array(verdictWireSchema).default([]),
  lineage: z.object({ assignments: z.array(assignmentWireSchema).default([]), runs: z.array(runWireSchema).default([]) }).loose(),
}).loose().transform((value): RunDetailProjection => {
  const node = nodeFromWire(value.node);
  return {
    missionId: value.mission_id,
    node,
    run: runFromWire(value.run),
    assignment: assignmentFromWire(value.assignment),
    agent: value.agent ? teamMemberFromWire(value.agent) : undefined,
    execution: value.execution ? {
      agentTaskId: value.execution.agent_task_id,
      status: value.execution.status,
      sessionId: value.execution.session_id,
      result: value.execution.result,
      error: value.execution.error,
      createdAt: value.execution.created_at,
      startedAt: value.execution.started_at,
      completedAt: value.execution.completed_at,
    } : undefined,
    messages: value.messages.map((message) => ({
      sequence: message.sequence, type: message.type, tool: message.tool, content: message.content,
      input: message.input, output: message.output, createdAt: message.created_at,
    })),
    usage: value.usage.map((usage) => ({
      provider: usage.provider, model: usage.model, inputTokens: usage.input_tokens, outputTokens: usage.output_tokens,
      cacheReadTokens: usage.cache_read_tokens, cacheWriteTokens: usage.cache_write_tokens, createdAt: usage.created_at,
      costUsdTicks: usage.cost_usd_ticks ?? undefined,
    })),
    artifacts: value.artifacts.map(artifactFromWire),
    reviews: value.reviews.map(verdictFromWire),
    lineage: {
      assignments: value.lineage.assignments.map(assignmentFromWire),
      runs: value.lineage.runs.map(runFromWire),
    },
  };
});

export const MissionBudgetApprovalResponseSchema = z.object({
  mission_id: z.string().min(1),
  status: z.string(),
  revision: z.number(),
  created_run_ids: z.array(z.string()).default([]),
  replayed: z.boolean().default(false),
}).loose().transform((value): MissionBudgetApprovalResponse => ({
  missionId: value.mission_id,
  status: value.status,
  revision: value.revision,
  createdRunIds: value.created_run_ids,
  replayed: value.replayed,
}));

export const MissionLifecycleResponseSchema = z.object({
  mission_id: z.string().min(1),
  status: z.string(),
  revision: z.number(),
  affected_run_ids: z.array(z.string()).default([]),
  replayed: z.boolean().default(false),
}).loose().transform((value): MissionLifecycleResponse => ({
  missionId: value.mission_id,
  status: value.status,
  revision: value.revision,
  affectedRunIds: value.affected_run_ids,
  replayed: value.replayed,
}));

export const RetryMissionTaskResponseSchema = z.object({
  mission_id: z.string().min(1),
  task_node_id: z.string().min(1),
  status: z.string(),
  revision: z.number(),
  created_run_ids: z.array(z.string()).default([]),
  replayed: z.boolean().default(false),
}).loose().transform((value): RetryMissionTaskResponse => ({
  missionId: value.mission_id,
  taskNodeId: value.task_node_id,
  status: value.status,
  revision: value.revision,
  createdRunIds: value.created_run_ids,
  replayed: value.replayed,
}));

export const EMPTY_MISSION_PROJECTION: MissionProjection = {
  mission: {
    id: "", title: "", description: "", status: "failed", currentPhase: "planning",
    progress: { completed: 0, total: 0, percent: 0 },
    limits: { maxParallelRuns: 1, maxTaskAttempts: 1, maxReworkCycles: 1 },
    budget: {
      status: "unlimited", consumedTokens: 0, reservedTokens: 0,
      consumedCostUsdTicks: 0, reservedCostUsdTicks: 0, grantTokens: 0, grantCostUsdTicks: 0,
    },
    revision: 0, lastSequence: 0, createdAt: "", updatedAt: "",
  },
  nodes: [], team: [], activities: { items: [], firstSequence: 0, lastSequence: 0, hasPrevious: false },
};

export const EMPTY_ACTIVITY_PAGE: ActivityPage = {
  items: [], afterSequence: 0, nextAfterSequence: 0, lastSequence: 0, hasMore: false, resetRequired: true,
};

export const EMPTY_RUN_DETAIL: RunDetailProjection = {
  missionId: "",
  node: {
    id: "", key: "", title: "", description: "", role: "executor", status: "pending",
    dependencyIds: [], acceptanceCriteria: [], artifactKinds: [], reworkCount: 0, revision: 0,
  },
  run: {
    id: "", taskNodeId: "", assignmentId: "", purpose: "execute", attempt: 1, status: "queued",
    input: {}, dispatchDeadlineAt: "", timeoutSeconds: 0, createdAt: "",
  },
  assignment: {
    id: "", taskNodeId: "", role: "executor", agentId: "", runtimeId: "", status: "revoked",
    sequence: 1, createdAt: "",
  },
  messages: [], usage: [], artifacts: [], reviews: [], lineage: { assignments: [], runs: [] },
};
