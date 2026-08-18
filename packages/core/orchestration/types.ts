export type MissionStatus =
  | "draft"
  | "ready"
  | "running"
  | "blocked"
  | "completed"
  | "failed"
  | "cancelled";

export type PlanSource = "manual" | "proposal" | "fixed_template";

export type TaskNodeStatus =
  | "pending"
  | "ready"
  | "assigned"
  | "running"
  | "review"
  | "rework"
  | "blocked"
  | "completed"
  | "failed"
  | "cancelled";

export type OrchestrationDuty = "planner" | "executor" | "reviewer" | "integrator";
export interface RoleProfile {
  id: string;
  workspaceId: string;
  profileKey: string;
  version: number;
  duty: OrchestrationDuty;
  name: string;
  description: string;
  config: unknown;
  createdAt: string;
}

export interface RolePolicyBinding {
  duty: OrchestrationDuty;
  profileKey: string;
  version: number;
  agentId?: string;
}

export interface RolePolicySnapshot {
  id: string;
  workspaceId: string;
  missionId: string;
  schemaVersion: number;
  duty: OrchestrationDuty;
  roleProfileId: string;
  roleProfileKey: string;
  roleProfileVersion: number;
  profileName: string;
  profileDescription: string;
  config: unknown;
  agentId?: string;
  contentHash: string;
  frozenBy: string;
  frozenAt: string;
}
export type AssignmentStatus = "active" | "fulfilled" | "superseded" | "revoked";
export type RunStatus = "queued" | "dispatched" | "running" | "succeeded" | "failed" | "cancelled";
export type ReviewDecision = "approved" | "changes_requested" | "rejected";
export type MissionBudgetStatus =
  | "unlimited"
  | "ok"
  | "approved"
  | "approval_required"
  | "budget_exceeded";

export interface PlanLimits {
  maxParallelRuns: number;
  maxTaskAttempts: number;
  maxReworkCycles: number;
}

export interface MissionProgress {
  completed: number;
  total: number;
  percent: number;
}

export interface MissionBudgetProjection {
  status: MissionBudgetStatus;
  gate?: string;
  maxTokens?: number;
  maxCostUsdTicks?: number;
  consumedTokens: number;
  reservedTokens: number;
  consumedCostUsdTicks: number;
  reservedCostUsdTicks: number;
  grantTokens: number;
  grantCostUsdTicks: number;
  approvedBy?: string;
  approvedAt?: string;
}

export interface BudgetEstimate {
  tokens: number;
  costUsdTicks: number;
}

export interface MissionProjectionSummary {
  id: string;
  title: string;
  description: string;
  status: MissionStatus;
  currentPhase: string;
  progress: MissionProgress;
  limits: PlanLimits;
  budget: MissionBudgetProjection;
  revision: number;
  lastSequence: number;
  createdAt: string;
  updatedAt: string;
}

export type HumanGateKind = "reviewer_unavailable" | "rework_limit_exceeded";
export interface HumanGateProjection {
  id: string;
  taskNodeId: string;
  artifactId: string;
  sourceRunId: string;
  kind: HumanGateKind;
  status: string;
  reason: string;
  context: unknown;
  revision: number;
  createdAt: string;
}

export interface AssignmentProjection {
  id: string;
  taskNodeId?: string;
  duty: OrchestrationDuty;
  agentId: string;
  runtimeId: string;
  status: AssignmentStatus;
  sequence: number;
  supersedesId?: string;
  createdAt: string;
  endedAt?: string;
}

export interface RunProjection {
  id: string;
  taskNodeId?: string;
  assignmentId: string;
  agentTaskId?: string;
  purpose: string;
  attempt: number;
  status: RunStatus;
  input: unknown;
  retryOfId?: string;
  failureKind?: string;
  failureMessage?: string;
  dispatchDeadlineAt: string;
  timeoutSeconds: number;
  startedAt?: string;
  finishedAt?: string;
  createdAt: string;
}

export interface ArtifactProjection {
  id: string;
  taskNodeId?: string;
  runId: string;
  kind: string;
  version: number;
  uri: string;
  contentHash?: string;
  summary: string;
  metadata: unknown;
  createdAt: string;
}

export interface ReviewVerdictProjection {
  id: string;
  taskNodeId: string;
  reviewRunId: string;
  artifactId: string;
  decision: ReviewDecision;
  evidence: unknown;
  requestedChanges: unknown[];
  createdAt: string;
}

export interface TaskNodeProjection {
  id: string;
  key: string;
  title: string;
  description: string;
  duty: OrchestrationDuty;
  status: TaskNodeStatus;
  dependencyIds: string[];
  acceptanceCriteria: string[];
  artifactKinds: string[];
  blockReason?: string;
  reworkCount: number;
  revision: number;
  budgetEstimate?: BudgetEstimate;
  activeAssignment?: AssignmentProjection;
  latestRun?: RunProjection;
  latestArtifact?: ArtifactProjection;
  latestVerdict?: ReviewVerdictProjection;
}

export interface TeamMemberProjection {
  agentId: string;
  agentName: string;
  avatarUrl?: string;
  duty: OrchestrationDuty;
  runtimeId: string;
  runtimeName: string;
  runtimeStatus: string;
  provider: string;
  model?: string;
  capabilities: unknown;
  currentNodeIds: string[];
}

export interface ActivityProjection {
  id: string;
  taskNodeId?: string;
  runId?: string;
  type: string;
  actorType: string;
  actorId?: string;
  subjectType: string;
  subjectId: string;
  causationId: string;
  correlationId: string;
  payloadVersion: number;
  payload: unknown;
  sequence: number;
  occurredAt: string;
}

export interface ActivityWindow {
  items: ActivityProjection[];
  firstSequence: number;
  lastSequence: number;
  hasPrevious: boolean;
}

export interface MissionProjection {
  mission: MissionProjectionSummary;
  nodes: TaskNodeProjection[];
  team: TeamMemberProjection[];
  activities: ActivityWindow;
  planning: PlanningProjection;
  rolePolicySnapshots: RolePolicySnapshot[];
  humanGates: HumanGateProjection[];
}

export type PlanProposalDecision = "pending" | "superseded" | "rejected" | "approved";
export interface PlanProposalProjection {
  id: string;
  runId: string;
  version: number;
  uri: string;
  contentHash?: string;
  summary: string;
  proposal: unknown;
  decision: PlanProposalDecision;
  decisionReason?: string;
  createdAt: string;
}
export interface PlanningProjection {
  assignments: AssignmentProjection[];
  runs: RunProjection[];
  proposals: PlanProposalProjection[];
  source?: PlanSource;
}
export interface PlanCommandResponse {
  missionId: string;
  status: string;
  revision: number;
  artifactId?: string;
  runId?: string;
  replayed: boolean;
}
export interface RequestPlanRequest {
  commandId: string;
  expectedRevision: number;
  objective: string;
  contextRefs: unknown[];
  deliveryCriteria: string[];
  rolePolicyBinding: RolePolicyBinding;
}
export interface EditPlanProposalRequest {
  commandId: string;
  expectedRevision: number;
  proposal: unknown;
}
export interface RejectPlanProposalRequest {
  commandId: string;
  expectedRevision: number;
  reason: string;
}

export interface ActivityPage {
  items: ActivityProjection[];
  afterSequence: number;
  nextAfterSequence: number;
  lastSequence: number;
  hasMore: boolean;
  resetRequired: boolean;
}

export interface ExecutionProjection {
  agentTaskId: string;
  status: string;
  sessionId?: string;
  result?: unknown;
  error?: string;
  createdAt: string;
  startedAt?: string;
  completedAt?: string;
}

export interface TaskMessageProjection {
  sequence: number;
  type: string;
  tool?: string;
  content?: string;
  input?: unknown;
  output?: string;
  createdAt: string;
}

export interface TaskUsageProjection {
  provider: string;
  model: string;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  costUsdTicks?: number;
  createdAt: string;
}

export interface ApproveMissionBudgetRequest {
  commandId: string;
  expectedRevision: number;
  grantTokens: number;
  grantCostUsdTicks: number;
  reason: string;
}

export interface MissionBudgetApprovalResponse {
  missionId: string;
  status: string;
  revision: number;
  createdRunIds: string[];
  replayed: boolean;
}

export interface MissionLifecycleRequest {
  commandId: string;
  expectedRevision: number;
  reason?: string;
}

export interface StartMissionRequest extends MissionLifecycleRequest {
  rolePolicyBindings: RolePolicyBinding[];
}

export interface MissionLifecycleResponse {
  missionId: string;
  status: string;
  revision: number;
  affectedRunIds: string[];
  replayed: boolean;
}

export interface RetryMissionTaskRequest {
  taskNodeId: string;
  commandId: string;
  expectedRevision: number;
  expectedTaskRevision: number;
  reason?: string;
}

export interface RetryMissionTaskResponse {
  missionId: string;
  taskNodeId: string;
  status: string;
  revision: number;
  createdRunIds: string[];
  replayed: boolean;
}

export interface ResolveHumanGateRequest {
  commandId: string;
  expectedRevision: number;
  expectedTaskRevision: number;
  expectedGateRevision: number;
  resolution: "retry";
  reason?: string;
}

export interface ResolveHumanGateResponse {
  missionId: string;
  taskNodeId: string;
  gateId: string;
  status: string;
  revision: number;
  taskRevision: number;
  gateRevision: number;
  createdRunIds: string[];
  replayed: boolean;
}

export interface RunLineageProjection {
  assignments: AssignmentProjection[];
  runs: RunProjection[];
}

export interface RunDetailProjection {
  missionId: string;
  node: TaskNodeProjection;
  run: RunProjection;
  assignment: AssignmentProjection;
  agent?: TeamMemberProjection;
  execution?: ExecutionProjection;
  messages: TaskMessageProjection[];
  usage: TaskUsageProjection[];
  artifacts: ArtifactProjection[];
  reviews: ReviewVerdictProjection[];
  lineage: RunLineageProjection;
}
