"use client";

import { useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  ArrowLeft,
  Bot,
  ClipboardList,
  Coins,
  LoaderCircle,
  Play,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  Sparkles,
} from "lucide-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@liexiu/core/hooks";
import { useWorkspacePaths } from "@liexiu/core/paths";
import {
  approveMissionBudgetMutationOptions,
  cancelMissionMutationOptions,
  missionActivitiesOptions,
  missionProjectionOptions,
  roleProfilesOptions,
  missionRunDetailOptions,
  retryMissionTaskMutationOptions,
  requestPlanMutationOptions,
  editPlanProposalMutationOptions,
  rejectPlanProposalMutationOptions,
  approvePlanProposalMutationOptions,
  shouldRefreshMissionSnapshot,
  startMissionMutationOptions,
  type ApproveMissionBudgetRequest,
  type MissionBudgetProjection,
  type MissionProjection,
  type RunDetailProjection,
  type TaskNodeProjection,
  type RoleProfile,
  type RolePolicyBinding,
  type HumanGateProjection,
} from "@liexiu/core/orchestration";
import { agentTaskSnapshotOptions, buildAgentRuntimeDiagnostics, type AgentRuntimeDiagnostic } from "@liexiu/core/agents";
import { agentListOptions } from "@liexiu/core/workspace";
import { runtimeListOptions } from "@liexiu/core/runtimes";
import { generateUUID } from "@liexiu/core/utils";
import { api } from "@liexiu/core/api";
import { Badge } from "@liexiu/ui/components/ui/badge";
import { Button } from "@liexiu/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@liexiu/ui/components/ui/card";
import { Input } from "@liexiu/ui/components/ui/input";
import { Label as FieldLabel } from "@liexiu/ui/components/ui/label";
import { Progress } from "@liexiu/ui/components/ui/progress";
import { Skeleton } from "@liexiu/ui/components/ui/skeleton";
import { Textarea } from "@liexiu/ui/components/ui/textarea";
import { cn } from "@liexiu/ui/lib/utils";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";
import { AgentWorld } from "./agent-world";
import { MissionBoard } from "./mission-board";
import { MissionReplay } from "./mission-replay";
import { useMissionRunSelection } from "./mission-run-selection";
import { useMissionViewMode, type MissionViewMode } from "./mission-view-mode";
import { RunDetailPanel } from "./run-detail-panel";
import type { VisualEvent } from "./world/visual-events";

export function MissionPage({ missionId }: { missionId: string }) {
  const { t } = useT("orchestration");
  const workspaceId = useWorkspaceId();
  const workspacePaths = useWorkspacePaths();
  const navigation = useNavigation();
  const sourceProjectId = navigation.searchParams.get("project") ?? "";
  const projectionQuery = useQuery(
    missionProjectionOptions(workspaceId, missionId),
  );
  const refetchProjection = projectionQuery.refetch;
  const projection = projectionQuery.data;
  const roleProfilesQuery = useQuery(roleProfilesOptions(workspaceId));
  const roleProfiles = roleProfilesQuery.data ?? [];
  const agentsQuery = useQuery(agentListOptions(workspaceId));
  const runtimesQuery = useQuery(runtimeListOptions(workspaceId));
  const agentTasksQuery = useQuery(agentTaskSnapshotOptions(workspaceId));
  const diagnostics = useMemo(() => buildAgentRuntimeDiagnostics({
    agents: agentsQuery.data ?? [],
    runtimes: runtimesQuery.data ?? [],
    snapshot: agentTasksQuery.data ?? [],
    now: Date.now(),
  }), [agentTasksQuery.data, agentsQuery.data, runtimesQuery.data]);
  const missionActive = projection
    ? !["completed", "failed", "cancelled"].includes(projection.mission.status)
    : false;
  const activityQuery = useQuery(
    missionActivitiesOptions(
      workspaceId,
      missionId,
      projection?.mission.lastSequence ?? 0,
      missionActive,
    ),
  );

  useEffect(() => {
    const page = activityQuery.data;
    if (!page || !projection) return;
    // Activity is only an invalidation signal. Business state is always
    // rebuilt from the canonical snapshot, so a cursor gap, server restart,
    // reconnect, or a newly observed sequence cannot make the three views
    // compute different local truth.
    if (shouldRefreshMissionSnapshot(projection.mission.lastSequence, page)) {
      void refetchProjection();
    }
  }, [activityQuery.data, projection, refetchProjection]);
  const availableRunIds = useMemo(
    () =>
      new Set(
        projectionQuery.data?.nodes.flatMap((node) =>
          node.latestRun ? [node.latestRun.id] : [],
        ) ?? [],
      ),
    [projectionQuery.data],
  );
  const { selectedRunId, selectRun } = useMissionRunSelection(availableRunIds);
  const { mode, setMode } = useMissionViewMode();
  const detailQuery = useQuery(
    missionRunDetailOptions(workspaceId, missionId, selectedRunId),
  );
  const budgetApproval = useMutation(approveMissionBudgetMutationOptions(missionId));
  const startMission = useMutation(startMissionMutationOptions(missionId));
  const cancelMission = useMutation(cancelMissionMutationOptions(missionId));
  const retryTask = useMutation(retryMissionTaskMutationOptions(missionId));
  const pendingProposal = projection?.planning.proposals.find((item) => item.decision === "pending");
  const requestPlan = useMutation(requestPlanMutationOptions(missionId));
  const editProposal = useMutation(editPlanProposalMutationOptions(missionId, pendingProposal?.id ?? ""));
  const rejectProposal = useMutation(rejectPlanProposalMutationOptions(missionId, pendingProposal?.id ?? ""));
  const approveProposal = useMutation(approvePlanProposalMutationOptions(missionId, pendingProposal?.id ?? ""));
  const resolveGate = useMutation({ mutationFn: async ({ gate, reason }: { gate: HumanGateProjection; reason?: string }) => {
    const node = projectionQuery.data?.nodes.find((item) => item.id === gate.taskNodeId);
    if (!node) throw new Error("Human Gate task is unavailable");
    return api.resolveHumanGate(missionId, gate.id, { commandId: generateUUID(), expectedRevision: projectionQuery.data!.mission.revision, expectedTaskRevision: node.revision, expectedGateRevision: gate.revision, resolution: "retry", reason });
  } });

  if (projectionQuery.isLoading) {
    return <MissionPageSkeleton label={t(($) => $.page.loading)} />;
  }

  if (projectionQuery.isError || !projectionQuery.data?.mission.id) {
    return (
      <div className="flex min-h-full items-center justify-center p-6">
        <Card className="w-full max-w-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <AlertTriangle className="size-4 text-destructive" />
              {t(($) => $.page.error_title)}
            </CardTitle>
            <CardDescription>{t(($) => $.page.error_hint)}</CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => void projectionQuery.refetch()}>
              <RefreshCw className="size-4" />
              {t(($) => $.page.retry)}
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {sourceProjectId ? (
        <div className="shrink-0 border-b bg-background px-4 py-2 md:px-6">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              const params = new URLSearchParams();
              if (navigation.searchParams.get("projectView") === "command-center") {
                params.set("view", "command-center");
              }
              const query = params.toString();
              navigation.push(`${workspacePaths.projectDetail(sourceProjectId)}${query ? `?${query}` : ""}`);
            }}
          >
            <ArrowLeft className="size-4" />
            {t(($) => $.page.back_to_project)}
          </Button>
        </div>
      ) : null}
      <MissionWorkspace
      projection={projectionQuery.data}
      selectedRunId={selectedRunId}
      onSelectRun={selectRun}
      viewMode={mode}
      onViewModeChange={setMode}
      onRefresh={() => void projectionQuery.refetch()}
      isRefreshing={projectionQuery.isFetching}
      onApproveBudget={async (request) => {
        await budgetApproval.mutateAsync(request);
        await projectionQuery.refetch();
      }}
      budgetApproving={budgetApproval.isPending}
      budgetApprovalError={budgetApproval.error instanceof Error ? budgetApproval.error.message : undefined}
      onResolveHumanGate={async (gate, reason) => {
        await resolveGate.mutateAsync({ gate, reason });
        await projectionQuery.refetch();
      }}
      humanGatePending={resolveGate.isPending}
      humanGateError={resolveGate.error instanceof Error ? resolveGate.error.message : undefined}
      roleProfiles={roleProfiles}
      onStart={async (bindings) => {
        await startMission.mutateAsync({
          commandId: generateUUID(),
          expectedRevision: projectionQuery.data.mission.revision,
          rolePolicyBindings: bindings,
        });
        await projectionQuery.refetch();
      }}
      onCancel={async () => {
        await cancelMission.mutateAsync({
          commandId: generateUUID(),
          expectedRevision: projectionQuery.data.mission.revision,
          reason: "Owner cancelled from mission workspace",
        });
        await projectionQuery.refetch();
      }}
      onRetryTask={async (node) => {
        await retryTask.mutateAsync({
          taskNodeId: node.id,
          commandId: generateUUID(),
          expectedRevision: projectionQuery.data.mission.revision,
          expectedTaskRevision: node.revision,
          reason: "Owner retry from mission workspace",
        });
        await projectionQuery.refetch();
      }}
      lifecyclePending={startMission.isPending || cancelMission.isPending || retryTask.isPending}
      lifecycleError={
        [startMission.error, cancelMission.error, retryTask.error].find((value) => value instanceof Error)?.message
      }
      onRequestPlan={async (objective, deliveryCriteria, binding) => {
        await requestPlan.mutateAsync({
          commandId: generateUUID(),
          expectedRevision: projectionQuery.data.mission.revision,
          objective,
          contextRefs: [],
          deliveryCriteria,
          rolePolicyBinding: binding,
        });
        await projectionQuery.refetch();
      }}
      onEditProposal={async (value) => {
        if (!pendingProposal) return;
        await editProposal.mutateAsync({
          commandId: generateUUID(),
          expectedRevision: projectionQuery.data.mission.revision,
          proposal: value,
        });
        await projectionQuery.refetch();
      }}
      onRejectProposal={async (reason) => {
        if (!pendingProposal) return;
        await rejectProposal.mutateAsync({
          commandId: generateUUID(),
          expectedRevision: projectionQuery.data.mission.revision,
          reason,
        });
        await projectionQuery.refetch();
      }}
      onApproveProposal={async () => {
        if (!pendingProposal) return;
        await approveProposal.mutateAsync({
          commandId: generateUUID(),
          expectedRevision: projectionQuery.data.mission.revision,
        });
        await projectionQuery.refetch();
      }}
      proposalPending={requestPlan.isPending || editProposal.isPending || rejectProposal.isPending || approveProposal.isPending}
      proposalError={
        [requestPlan.error, editProposal.error, rejectProposal.error, approveProposal.error]
          .find((value) => value instanceof Error)?.message
      }
      detail={detailQuery.data}
      detailLoading={detailQuery.isLoading && selectedRunId.length > 0}
      detailError={detailQuery.isError}
      diagnostics={{
        items: diagnostics,
        loading: agentsQuery.isLoading || runtimesQuery.isLoading || agentTasksQuery.isLoading,
        error: agentsQuery.isError || runtimesQuery.isError || agentTasksQuery.isError,
        onRefresh: () => void Promise.all([agentsQuery.refetch(), runtimesQuery.refetch(), agentTasksQuery.refetch()]),
      }}
      />
    </div>
  );
}

export interface MissionWorkspaceProps {
  projection: MissionProjection;
  selectedRunId: string;
  onSelectRun: (runId: string) => void;
  onRefresh?: () => void;
  isRefreshing?: boolean;
  onApproveBudget?: (request: ApproveMissionBudgetRequest) => Promise<void>;
  budgetApproving?: boolean;
  budgetApprovalError?: string;
  onResolveHumanGate?: (gate: HumanGateProjection, reason?: string) => Promise<void>;
  humanGatePending?: boolean;
  humanGateError?: string;
  detail?: RunDetailProjection;
  detailLoading?: boolean;
  detailError?: boolean;
  roleProfiles?: RoleProfile[];
  onStart?: (bindings: RolePolicyBinding[]) => Promise<void>;
  onCancel?: () => Promise<void>;
  onRetryTask?: (node: TaskNodeProjection) => Promise<void>;
  lifecyclePending?: boolean;
  lifecycleError?: string;
  onRequestPlan?: (objective: string, deliveryCriteria: string[], binding: RolePolicyBinding) => Promise<void>;
  onEditProposal?: (proposal: unknown) => Promise<void>;
  onRejectProposal?: (reason: string) => Promise<void>;
  onApproveProposal?: () => Promise<void>;
  proposalPending?: boolean;
  proposalError?: string;
  diagnostics?: AgentDiagnosticsProps;
  viewMode?: MissionViewMode;
  onViewModeChange?: (mode: MissionViewMode) => void;
}

export interface AgentDiagnosticsProps {
  items: AgentRuntimeDiagnostic[];
  loading: boolean;
  error: boolean;
  onRefresh?: () => void;
}

/**
 * Pure three-view projection surface. Keeping data ownership above this
 * component makes it impossible for the board and world to drift by issuing
 * independent reads for the same Mission.
 */
export function MissionWorkspace({
  projection,
  selectedRunId,
  onSelectRun,
  onRefresh,
  isRefreshing = false,
  detail,
  detailLoading = false,
  detailError = false,
  onApproveBudget,
  budgetApproving = false,
  budgetApprovalError,
  onResolveHumanGate,
  humanGatePending = false,
  humanGateError,
  onStart,
  onCancel,
  onRetryTask,
  lifecyclePending = false,
  lifecycleError,
  onRequestPlan,
  onEditProposal,
  onRejectProposal,
  onApproveProposal,
  proposalPending = false,
  proposalError,
  roleProfiles = [],
  diagnostics,
  viewMode,
  onViewModeChange,
}: MissionWorkspaceProps) {
  const { t } = useT("orchestration");
  const { mission } = projection;
  const [localMode, setLocalMode] = useState<MissionViewMode>("world");
  const [replayVisualEvents, setReplayVisualEvents] = useState<readonly VisualEvent[]>([]);
  const [worldMotionPaused, setWorldMotionPaused] = useState(false);
  const [worldLowPerformance, setWorldLowPerformance] = useState(false);
  const mode = viewMode ?? localMode;
  const setMode = onViewModeChange ?? setLocalMode;
  const proposals = projection.planning.proposals;
  const planningInProgress = projection.planning.assignments.some((item) => item.status === "active");
  const [selectedProfiles, setSelectedProfiles] = useState<Record<string, string>>({});
  const effectiveRoleProfiles = useMemo(() => {
    const profiles = [...roleProfiles];
    for (const snapshot of projection.rolePolicySnapshots) {
      if (!profiles.some((profile) => profile.profileKey === snapshot.roleProfileKey && profile.version === snapshot.roleProfileVersion)) {
        profiles.push({
          id: snapshot.roleProfileId, workspaceId: snapshot.workspaceId, profileKey: snapshot.roleProfileKey,
          version: snapshot.roleProfileVersion, duty: snapshot.duty, name: snapshot.profileName,
          description: snapshot.profileDescription, config: snapshot.config, createdAt: snapshot.frozenAt,
        });
      }
    }
    return profiles;
  }, [projection.rolePolicySnapshots, roleProfiles]);
  useEffect(() => {
    if (projection.rolePolicySnapshots.length === 0) return;
    setSelectedProfiles((current) => {
      const next = { ...current };
      for (const snapshot of projection.rolePolicySnapshots) {
        next[snapshot.duty] = `${snapshot.roleProfileKey}:${snapshot.roleProfileVersion}`;
      }
      return next;
    });
  }, [projection.rolePolicySnapshots]);
  const profileFor = (duty: RolePolicyBinding["duty"]) => effectiveRoleProfiles.find((profile) => profile.duty === duty && `${profile.profileKey}:${profile.version}` === selectedProfiles[duty]);
  const plannerProfile = profileFor("planner");
  const executionProfiles = (["executor", "reviewer", "integrator"] as const).map(profileFor);
  const bindingsReady = executionProfiles.every(Boolean);
  const updateProfile = (duty: RolePolicyBinding["duty"], value: string) => setSelectedProfiles((current) => ({ ...current, [duty]: value }));
  const profileSelect = (duty: RolePolicyBinding["duty"]) => {
    const matches = effectiveRoleProfiles.filter((profile) => profile.duty === duty);
    const frozen = projection.rolePolicySnapshots.some((snapshot) => snapshot.duty === duty);
    return <div className="space-y-1.5" key={duty}>
      <FieldLabel htmlFor={`mission-role-profile-${duty}`}>{t(($) => $.planning.duty_profile, { duty })}</FieldLabel>
      <select id={`mission-role-profile-${duty}`} className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-body" value={selectedProfiles[duty] ?? ""} onChange={(event) => updateProfile(duty, event.target.value)} disabled={frozen}>
        <option value="">{t(($) => $.planning.select_profile)}</option>
        {matches.map((profile) => <option key={`${profile.profileKey}:${profile.version}`} value={`${profile.profileKey}:${profile.version}`}>{t(($) => $.planning.profile_option, { name: profile.name, key: profile.profileKey, version: profile.version })}</option>)}
      </select>
      {matches.length === 0 ? <p className="text-caption text-destructive">{t(($) => $.planning.no_matching_profile, { duty })}</p> : null}
    </div>;
  };

  return (
    <main className="flex min-h-0 flex-1 flex-col bg-background">
      <header className="shrink-0 border-b bg-background/95 px-4 py-4 backdrop-blur md:px-6">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="mb-1 flex items-center gap-2 text-caption font-medium uppercase tracking-wider text-muted-foreground">
              <Sparkles className="size-3.5" />
              {t(($) => $.page.eyebrow)}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate text-title-lg font-semibold tracking-tight">
                {mission.title}
              </h1>
              <Badge variant={mission.status === "failed" ? "destructive" : "secondary"}>
                {mission.status}
              </Badge>
            </div>
            {mission.description ? (
              <p className="mt-1 max-w-3xl text-body text-muted-foreground">
                {mission.description}
              </p>
            ) : null}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {mission.status === "ready" && onStart ? (
              <Button size="sm" onClick={() => void onStart(executionProfiles.map((profile, index) => ({ duty: ["executor", "reviewer", "integrator"][index] as RolePolicyBinding["duty"], profileKey: profile!.profileKey, version: profile!.version })))} disabled={lifecyclePending || !bindingsReady}>
                <Play className="size-4" />
                {t(($) => $.page.start)}
              </Button>
            ) : null}
            {["draft", "ready", "running", "blocked"].includes(mission.status) && onCancel ? (
              <Button variant="destructive" size="sm" onClick={() => void onCancel()} disabled={lifecyclePending}>
                {t(($) => $.page.cancel)}
              </Button>
            ) : null}
            {onRefresh ? (
              <Button variant="outline" size="sm" onClick={onRefresh} disabled={isRefreshing}>
                <RefreshCw className={cn("size-4", isRefreshing && "animate-spin")} />
                {t(($) => $.page.refresh)}
              </Button>
            ) : null}
          </div>
        </div>
        {lifecycleError ? <p className="mt-2 text-caption text-destructive" role="alert">{lifecycleError}</p> : null}
        <div className="mt-4 grid gap-2 md:grid-cols-[minmax(12rem,1fr)_auto_auto] md:items-center">
          <Progress
            value={mission.progress.percent}
            aria-label={t(($) => $.page.progress, {
              completed: mission.progress.completed,
              total: mission.progress.total,
            })}
          />
          <span className="text-caption tabular-nums text-muted-foreground">
            {t(($) => $.page.progress, {
              completed: mission.progress.completed,
              total: mission.progress.total,
            })}
          </span>
          <div className="flex flex-wrap gap-x-3 gap-y-1 text-caption text-muted-foreground">
            <span>{t(($) => $.page.phase, { phase: mission.currentPhase })}</span>
            <span>{t(($) => $.page.sequence, { sequence: mission.lastSequence })}</span>
          </div>
        </div>
        <MissionBudgetPanel
          budget={mission.budget}
          revision={mission.revision}
          onApprove={onApproveBudget}
          approving={budgetApproving}
          error={budgetApprovalError}
        />
        <HumanGatePanel gates={projection.humanGates} nodes={projection.nodes} onResolve={onResolveHumanGate} pending={humanGatePending} error={humanGateError} />
        {mission.status === "ready" ? (
          <Card className="mt-4 border-primary/30 bg-primary/[0.03]">
            <CardHeader className="pb-3"><CardTitle className="text-body">{t(($) => $.planning.title)}</CardTitle><CardDescription>{t(($) => $.planning.hint)}</CardDescription></CardHeader>
            <CardContent className="grid gap-3 pt-0 md:grid-cols-3">{(["executor", "reviewer", "integrator"] as const).map(profileSelect)}</CardContent>
          </Card>
        ) : null}
        <PlanningGate
          proposals={proposals}
          source={projection.planning.source}
          planningInProgress={planningInProgress}
          canRequest={mission.status === "draft"}
          onRequest={onRequestPlan}
          onEdit={onEditProposal}
          onReject={onRejectProposal}
          onApprove={onApproveProposal}
          pending={proposalPending}
          error={proposalError}
          roleProfileFields={profileSelect("planner")}
          plannerProfile={plannerProfile}
        />
      </header>

      <div className="shrink-0 px-4 pt-4 md:px-6">
        <AgentRuntimeDiagnosticsPanel diagnostics={diagnostics} />
      </div>

      <div className="flex shrink-0 gap-2 px-4 pt-4 md:px-6" aria-label={t(($) => $.world.view_mode)}>
        <Button type="button" size="sm" variant={mode === "world" ? "default" : "outline"} aria-pressed={mode === "world"} onClick={() => setMode("world")}>
          {t(($) => $.world.view_world)}
        </Button>
        <Button type="button" size="sm" variant={mode === "replay" ? "default" : "outline"} aria-pressed={mode === "replay"} onClick={() => setMode("replay")}>
          {t(($) => $.world.view_replay)}
        </Button>
      </div>

      <div className="grid min-h-0 flex-1 gap-4 overflow-auto p-4 md:p-6 xl:grid-cols-[minmax(18rem,0.9fr)_minmax(26rem,1.35fr)_minmax(20rem,1fr)] xl:overflow-hidden">
        <MissionBoard
          projection={projection}
          selectedRunId={selectedRunId}
          onSelectRun={onSelectRun}
        />
        <div className="min-h-0 space-y-4 overflow-auto">
          {mode === "replay" ? <MissionReplay
            projection={projection}
            onSelectRun={onSelectRun}
            renderWorld={false}
            onVisibleEventsChange={setReplayVisualEvents}
            labels={{
              play: t(($) => $.world.replay_play),
              pause: t(($) => $.world.replay_pause),
              sequence: t(($) => $.world.replay_sequence),
              actor: t(($) => $.world.replay_actor),
              task: t(($) => $.world.replay_task),
              run: t(($) => $.world.replay_run),
              all: t(($) => $.world.replay_all),
              events: t(($) => $.world.replay_events),
              rateLabels: { 0.5: "0.5×", 1: "1×", 2: "2×", 4: "4×" },
            }}
          /> : null}
          <AgentWorld
            key="mission-world-renderer"
            projection={projection}
            onSelectRun={onSelectRun}
            visualEventsOverride={mode === "replay" ? replayVisualEvents : undefined}
            compactActivity={mode === "replay"}
            motionPaused={worldMotionPaused}
            onMotionPausedChange={setWorldMotionPaused}
            lowPerformance={worldLowPerformance}
            onLowPerformanceChange={setWorldLowPerformance}
          />
        </div>
        <RunDetailPanel
          selectedRunId={selectedRunId}
          projection={projection}
          detail={detail}
          loading={detailLoading}
          error={detailError}
          onSelectRun={onSelectRun}
          onRetryTask={onRetryTask}
          lifecyclePending={lifecyclePending}
        />
      </div>
    </main>
  );
}

function AgentRuntimeDiagnosticsPanel({ diagnostics }: { diagnostics?: AgentDiagnosticsProps }) {
  const { t } = useT("orchestration");
  if (!diagnostics) return null;
  return (
    <Card data-testid="agent-runtime-diagnostics">
      <CardHeader className="border-b pb-3">
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2 text-body"><Bot className="size-4" />{t(($) => $.diagnostics.title)}</CardTitle>
            <CardDescription>{t(($) => $.diagnostics.hint)}</CardDescription>
          </div>
          {diagnostics.onRefresh ? <Button variant="outline" size="sm" onClick={diagnostics.onRefresh} disabled={diagnostics.loading}><RefreshCw className="size-4" />{t(($) => $.diagnostics.refresh)}</Button> : null}
        </div>
      </CardHeader>
      <CardContent className="pt-4">
        {diagnostics.loading ? <p className="text-caption text-muted-foreground" role="status">{t(($) => $.diagnostics.loading)}</p> : null}
        {!diagnostics.loading && diagnostics.error ? <p className="text-caption text-destructive" role="alert">{t(($) => $.diagnostics.error)}</p> : null}
        {!diagnostics.loading && !diagnostics.error && diagnostics.items.length === 0 ? <p className="text-caption text-muted-foreground">{t(($) => $.diagnostics.empty)}</p> : null}
        {!diagnostics.loading && !diagnostics.error && diagnostics.items.length > 0 ? (
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {diagnostics.items.map((item) => <AgentRuntimeDiagnosticCard key={item.agent.id} item={item} />)}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function AgentRuntimeDiagnosticCard({ item }: { item: AgentRuntimeDiagnostic }) {
  const { t } = useT("orchestration");
  const capabilityText = item.capabilities?.length ? item.capabilities.join(", ") : t(($) => $.diagnostics.unknown);
  return (
    <article className="rounded-lg border bg-background p-3 text-caption" data-agent-diagnostic-id={item.agent.id}>
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0"><h3 className="truncate font-medium">{item.agent.name}</h3><p className="truncate text-muted-foreground">{item.runtime?.name ?? t(($) => $.diagnostics.unbound)}</p></div>
        <Badge variant={item.available ? "default" : "outline"}>{item.available ? t(($) => $.diagnostics.available) : t(($) => $.diagnostics.unavailable)}</Badge>
      </div>
      <dl className="mt-3 grid grid-cols-2 gap-x-3 gap-y-1 text-muted-foreground">
        <dt>{t(($) => $.diagnostics.binding)}</dt><dd className="text-right text-foreground">{item.runtimeBound ? t(($) => $.diagnostics.bound) : t(($) => $.diagnostics.unbound)}</dd>
        <dt>{t(($) => $.diagnostics.runtime)}</dt><dd className="text-right text-foreground">{item.runtimeOnline ? t(($) => $.diagnostics.online) : t(($) => $.diagnostics.offline)}</dd>
        <dt>{t(($) => $.diagnostics.heartbeat)}</dt><dd className="truncate text-right text-foreground">{item.runtime?.last_seen_at ?? "—"}</dd>
        <dt>{t(($) => $.diagnostics.capacity)}</dt><dd className="text-right font-mono text-foreground">{item.used} / {item.limit}</dd>
        <dt>{t(($) => $.diagnostics.capabilities)}</dt><dd className="truncate text-right text-foreground" title={capabilityText}>{capabilityText}</dd>
        <dt>{t(($) => $.diagnostics.permission)}</dt><dd className="text-right text-foreground">{item.permissionMode}</dd>
        <dt>{t(($) => $.diagnostics.visibility)}</dt><dd className="text-right text-foreground">{item.runtimeVisibility}</dd>
      </dl>
    </article>
  );
}

function PlanningGate({
  proposals,
  source,
  planningInProgress,
  canRequest,
  onRequest,
  onEdit,
  onReject,
  onApprove,
  pending,
  error,
  roleProfileFields,
  plannerProfile,
}: {
  proposals: MissionProjection["planning"]["proposals"];
  source: MissionProjection["planning"]["source"];
  planningInProgress: boolean;
  canRequest: boolean;
  onRequest?: (objective: string, deliveryCriteria: string[], binding: RolePolicyBinding) => Promise<void>;
  onEdit?: (value: unknown) => Promise<void>;
  onReject?: (reason: string) => Promise<void>;
  onApprove?: () => Promise<void>;
  pending: boolean;
  error?: string;
  roleProfileFields?: React.ReactNode;
  plannerProfile?: RoleProfile;
}) {
  const { t } = useT("orchestration");
  const pendingProposal = proposals.find((item) => item.decision === "pending");
  const defaultProposalID = pendingProposal?.id ?? proposals.at(-1)?.id ?? "";
  const [selectedID, setSelectedID] = useState(defaultProposalID);
  const [compareID, setCompareID] = useState("");
  const [text, setText] = useState("");
  const [reason, setReason] = useState("");
  const [objective, setObjective] = useState("");
  const [criteria, setCriteria] = useState("");
  const [localError, setLocalError] = useState("");

  const selected = proposals.find((item) => item.id === selectedID) ?? proposals.at(-1);
  const compared = proposals.find((item) => item.id === compareID);
  const editable = selected?.decision === "pending" && selected.id === pendingProposal?.id;

  useEffect(() => {
    if (defaultProposalID && !proposals.some((item) => item.id === selectedID)) {
      setSelectedID(defaultProposalID);
    }
  }, [defaultProposalID, proposals, selectedID]);

  useEffect(() => {
    if (pendingProposal?.id) setSelectedID(pendingProposal.id);
  }, [pendingProposal?.id]);

  useEffect(() => {
    if (compareID && (compareID === selectedID || !proposals.some((item) => item.id === compareID))) {
      setCompareID("");
    }
  }, [compareID, proposals, selectedID]);

  useEffect(() => {
    setText(selected ? JSON.stringify(selected.proposal, null, 2) : "");
    setLocalError("");
  }, [selected]);

  if (!canRequest && proposals.length === 0 && !planningInProgress && !source) return null;

  const edit = async () => {
    try {
      const value: unknown = JSON.parse(text);
      setLocalError("");
      await onEdit?.(value);
    } catch (cause) {
      if (cause instanceof SyntaxError) setLocalError(t(($) => $.planning.invalid_json));
    }
  };

  const request = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const deliveryCriteria = criteria.split("\n").map((item) => item.trim()).filter(Boolean);
    if (!objective.trim() || deliveryCriteria.length === 0 || !plannerProfile) return;
    await onRequest?.(objective.trim(), deliveryCriteria, { duty: "planner", profileKey: plannerProfile.profileKey, version: plannerProfile.version });
  };

  return (
    <Card className="mt-4 border-primary/30 bg-primary/[0.03]">
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-body">
          <ClipboardList className="size-4" />
          {t(($) => $.planning.title)}
          {selected ? <Badge variant="outline">{selected.decision}</Badge> : null}
        </CardTitle>
        <CardDescription>{t(($) => $.planning.hint)}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        {source ? (
          <div className="flex flex-wrap items-center gap-2 rounded-md border bg-background px-3 py-2 text-caption">
            <span className="text-muted-foreground">{t(($) => $.planning.source)}</span>
            <Badge variant={source === "fixed_template" ? "secondary" : "outline"}>
              {t(($) => $.planning.sources[source])}
            </Badge>
            {source === "fixed_template" ? <span className="text-muted-foreground">{t(($) => $.planning.fixed_template_notice)}</span> : null}
          </div>
        ) : null}
        {planningInProgress && !pendingProposal ? (
          <div className="flex items-center gap-2 rounded-md border bg-background px-3 py-2 text-caption text-muted-foreground">
            <LoaderCircle className="size-4 animate-spin" />
            {t(($) => $.planning.in_progress)}
          </div>
        ) : null}

        {canRequest && !planningInProgress && !pendingProposal ? (
          <form className="grid gap-3" onSubmit={(event) => void request(event)}>
            <div className="space-y-1.5">
              <FieldLabel htmlFor="mission-plan-objective">{t(($) => $.planning.objective)}</FieldLabel>
              <Textarea
                id="mission-plan-objective"
                value={objective}
                onChange={(event) => setObjective(event.target.value)}
                placeholder={t(($) => $.planning.objective_placeholder)}
                disabled={pending}
                rows={2}
                required
              />
            </div>
            <div className="space-y-1.5">
              <FieldLabel htmlFor="mission-plan-criteria">{t(($) => $.planning.criteria)}</FieldLabel>
              <Textarea
                id="mission-plan-criteria"
                value={criteria}
                onChange={(event) => setCriteria(event.target.value)}
                placeholder={t(($) => $.planning.criteria_placeholder)}
                disabled={pending}
                rows={3}
                required
              />
            </div>
            {roleProfileFields}
            <Button className="w-fit" size="sm" type="submit" disabled={pending || !objective.trim() || !criteria.trim() || !plannerProfile}>
              {pending ? t(($) => $.planning.requesting) : t(($) => $.planning.request)}
            </Button>
          </form>
        ) : null}

        {selected ? (
          <div className="space-y-3 border-t pt-3">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <FieldLabel htmlFor="mission-plan-version">{t(($) => $.planning.version)}</FieldLabel>
                <select
                  id="mission-plan-version"
                  className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-body shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                  value={selected.id}
                  onChange={(event) => setSelectedID(event.target.value)}
                  disabled={pending}
                >
                  {proposals.map((item) => (
                    <option key={item.id} value={item.id}>
                      {t(($) => $.planning.version_option, { version: item.version, decision: item.decision })}
                    </option>
                  ))}
                </select>
              </div>
              <div className="space-y-1.5">
                <FieldLabel htmlFor="mission-plan-compare">{t(($) => $.planning.compare)}</FieldLabel>
                <select
                  id="mission-plan-compare"
                  className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-body shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                  value={compareID}
                  onChange={(event) => setCompareID(event.target.value)}
                  disabled={pending || proposals.length < 2}
                >
                  <option value="">{t(($) => $.planning.no_compare)}</option>
                  {proposals.filter((item) => item.id !== selected.id).map((item) => (
                    <option key={item.id} value={item.id}>
                      {t(($) => $.planning.version_option, { version: item.version, decision: item.decision })}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.planning.proposal_meta, { version: selected.version, identity: selected.contentHash ?? selected.id })}
            </p>
            <div className={cn("grid gap-3", compared && "lg:grid-cols-2")}>
              <Textarea
                aria-label={t(($) => $.planning.proposal)}
                value={text}
                onChange={(event) => setText(event.target.value)}
                disabled={pending || !editable}
                rows={10}
              />
              {compared ? (
                <Textarea
                  aria-label={t(($) => $.planning.compared_proposal)}
                  value={JSON.stringify(compared.proposal, null, 2)}
                  readOnly
                  rows={10}
                />
              ) : null}
            </div>
            {selected.decisionReason ? (
              <p className="text-caption text-muted-foreground">{selected.decisionReason}</p>
            ) : null}
            {editable ? (
              <>
                <Textarea
                  aria-label={t(($) => $.planning.rejection_reason)}
                  placeholder={t(($) => $.planning.rejection_placeholder)}
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                  disabled={pending}
                  rows={2}
                />
                <div className="flex flex-wrap gap-2">
                  <Button size="sm" onClick={() => void edit()} disabled={pending}>{t(($) => $.planning.save)}</Button>
                  <Button size="sm" variant="outline" onClick={() => void onApprove?.()} disabled={pending}>{t(($) => $.planning.approve)}</Button>
                  <Button size="sm" variant="destructive" onClick={() => void onReject?.(reason.trim())} disabled={pending || !reason.trim()}>{t(($) => $.planning.reject)}</Button>
                </div>
              </>
            ) : null}
          </div>
        ) : null}

        {localError || error ? (
          <p role="alert" className="text-caption text-destructive">{localError || error}</p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function HumanGatePanel({
  gates, nodes, onResolve, pending, error,
}: {
  gates: HumanGateProjection[];
  nodes: TaskNodeProjection[];
  onResolve?: (gate: HumanGateProjection, reason?: string) => Promise<void>;
  pending: boolean;
  error?: string;
}) {
  const { t } = useT("orchestration");
  const pendingGates = gates.filter((gate) => gate.status === "pending");
  if (pendingGates.length === 0) return null;
  return <Card className="mt-4 border-destructive/30 bg-destructive/[0.03]">
    <CardHeader className="pb-3"><CardTitle className="flex items-center gap-2 text-body"><ShieldCheck className="size-4" />{t(($) => $.human_gate.title)}</CardTitle><CardDescription>{t(($) => $.human_gate.hint)}</CardDescription></CardHeader>
    <CardContent className="space-y-3 pt-0">
      {pendingGates.map((gate) => {
        const node = nodes.find((item) => item.id === gate.taskNodeId);
        return <div key={gate.id} className="rounded-lg border bg-background/70 p-3">
          <div className="flex flex-wrap items-center justify-between gap-2"><div><p className="font-medium">{node?.key ?? gate.taskNodeId} · {node?.title ?? t(($) => $.human_gate.unknown_task)}</p><p className="text-caption text-muted-foreground">{t(($) => $.human_gate.kind[gate.kind])}</p></div><Badge variant="destructive">{t(($) => $.human_gate.pending)}</Badge></div>
          {gate.reason ? <p className="mt-2 text-caption text-muted-foreground">{gate.reason}</p> : null}
          <Button className="mt-3" size="sm" variant="outline" disabled={pending || !onResolve || !node} onClick={() => void onResolve?.(gate, t(($) => $.human_gate.retry_reason))}><RotateCcw className="size-4" />{pending ? t(($) => $.human_gate.resolving) : t(($) => $.human_gate.retry)}</Button>
        </div>;
      })}
      {error ? <p className="text-caption text-destructive" role="alert">{error}</p> : null}
    </CardContent>
  </Card>;
}

function MissionBudgetPanel({
  budget,
  revision,
  onApprove,
  approving,
  error,
}: {
  budget: MissionBudgetProjection;
  revision: number;
  onApprove?: (request: ApproveMissionBudgetRequest) => Promise<void>;
  approving: boolean;
  error?: string;
}) {
  const { t } = useT("orchestration");
  const [grantTokens, setGrantTokens] = useState(() => suggestedGrant(budget.maxTokens, budget.consumedTokens, budget.reservedTokens));
  const [grantCost, setGrantCost] = useState(() => suggestedGrant(budget.maxCostUsdTicks, budget.consumedCostUsdTicks, budget.reservedCostUsdTicks));
  const [reason, setReason] = useState("");
  const statusLabels = {
    unlimited: t(($) => $.budget.status.unlimited),
    ok: t(($) => $.budget.status.ok),
    approved: t(($) => $.budget.status.approved),
    approval_required: t(($) => $.budget.status.approval_required),
    budget_exceeded: t(($) => $.budget.status.budget_exceeded),
  };
  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!onApprove) return;
    const tokens = Number.parseInt(grantTokens || "0", 10);
    const costUsdTicks = Number.parseInt(grantCost || "0", 10);
    if (!Number.isFinite(tokens) || !Number.isFinite(costUsdTicks) || tokens < 0 || costUsdTicks < 0) return;
    void onApprove({
      commandId: generateUUID(),
      expectedRevision: revision,
      grantTokens: tokens,
      grantCostUsdTicks: costUsdTicks,
      reason: reason.trim(),
    });
  };

  return (
    <Card className="mt-4 border-primary/30 bg-primary/[0.03]">
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-body">
          <ShieldCheck className="size-4" />
          {t(($) => $.budget.title)}
          <Badge variant={budget.status === "budget_exceeded" ? "destructive" : "outline"} className="ml-auto">
            {statusLabels[budget.status]}
          </Badge>
        </CardTitle>
        <CardDescription>{t(($) => $.budget.hint)}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        <div className="grid gap-2 text-caption sm:grid-cols-4">
          <BudgetMetric label={t(($) => $.budget.consumed)} value={formatBudget(budget.consumedTokens)} suffix="tokens" />
          <BudgetMetric label={t(($) => $.budget.reserved)} value={formatBudget(budget.reservedTokens)} suffix="tokens" />
          <BudgetMetric label={t(($) => $.budget.grant)} value={formatBudget(budget.grantTokens)} suffix="tokens" />
          <BudgetMetric label={t(($) => $.budget.cost)} value={formatBudget(budget.consumedCostUsdTicks)} suffix="ticks" />
        </div>
        {budget.gate ? <p className="text-caption text-muted-foreground">{t(($) => $.budget.gate, { gate: budget.gate })}</p> : null}
        {budget.status === "approval_required" && onApprove ? (
          <form className="grid gap-3 border-t pt-3 md:grid-cols-2" onSubmit={submit}>
            <div className="space-y-1.5">
              <FieldLabel htmlFor="mission-budget-grant-tokens">{t(($) => $.budget.grant_tokens)}</FieldLabel>
              <Input id="mission-budget-grant-tokens" type="number" min="0" step="1" value={grantTokens} onChange={(event) => setGrantTokens(event.target.value)} required />
            </div>
            <div className="space-y-1.5">
              <FieldLabel htmlFor="mission-budget-grant-cost">{t(($) => $.budget.grant_cost)}</FieldLabel>
              <Input id="mission-budget-grant-cost" type="number" min="0" step="1" value={grantCost} onChange={(event) => setGrantCost(event.target.value)} required />
            </div>
            <div className="space-y-1.5 md:col-span-2">
              <FieldLabel htmlFor="mission-budget-reason">{t(($) => $.budget.reason)}</FieldLabel>
              <Textarea id="mission-budget-reason" value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t(($) => $.budget.reason_placeholder)} rows={2} />
            </div>
            <div className="flex flex-wrap items-center gap-3 md:col-span-2">
              <Button type="submit" size="sm" disabled={approving}>
                <Coins className="size-4" />
                {approving ? t(($) => $.budget.approving) : t(($) => $.budget.approve)}
              </Button>
              {error ? <p className="text-caption text-destructive" role="alert">{error}</p> : null}
            </div>
          </form>
        ) : null}
      </CardContent>
    </Card>
  );
}

function BudgetMetric({ label, value, suffix }: { label: string; value: string; suffix: string }) {
  return (
    <div className="rounded-md border bg-background/70 px-2.5 py-2">
      <p className="text-muted-foreground">{label}</p>
      <p className="mt-0.5 font-mono tabular-nums">{value} <span className="text-muted-foreground">{suffix}</span></p>
    </div>
  );
}

function suggestedGrant(max: number | undefined, consumed: number, reserved: number) {
  return max === undefined ? "" : String(Math.max(max - consumed - reserved, 0));
}

function formatBudget(value: number) {
  return value.toLocaleString();
}

function MissionPageSkeleton({ label }: { label: string }) {
  return (
    <div className="flex min-h-full flex-col gap-4 p-4 md:p-6" aria-label={label}>
      <Skeleton className="h-24 w-full" />
      <div className="grid flex-1 gap-4 xl:grid-cols-3">
        <Skeleton className="min-h-96" />
        <Skeleton className="min-h-96" />
        <Skeleton className="min-h-96" />
      </div>
    </div>
  );
}
