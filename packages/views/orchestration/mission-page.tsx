"use client";

import { useEffect, useMemo, useState } from "react";
import {
  Activity,
  AlertTriangle,
  Bot,
  Box,
  CheckCircle2,
  ClipboardList,
  Coins,
  GitBranch,
  LoaderCircle,
  MessageSquareText,
  Play,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  Sparkles,
} from "lucide-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@liexiu/core/hooks";
import {
  approveMissionBudgetMutationOptions,
  cancelMissionMutationOptions,
  missionActivitiesOptions,
  missionProjectionOptions,
  missionRunDetailOptions,
  retryMissionTaskMutationOptions,
  shouldRefreshMissionSnapshot,
  startMissionMutationOptions,
  type ApproveMissionBudgetRequest,
  type MissionBudgetProjection,
  type MissionProjection,
  type RunDetailProjection,
  type TaskNodeProjection,
} from "@liexiu/core/orchestration";
import { generateUUID } from "@liexiu/core/utils";
import { Badge } from "@liexiu/ui/components/ui/badge";
import { Button } from "@liexiu/ui/components/ui/button";
import {
  Card,
  CardAction,
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
import agentRoleAtlas from "./agent-role-atlas-v1.png";
import {
  BOARD_LANES,
  WORLD_ZONES,
  boardLaneForStatus,
  buildDagLayout,
  buildPixelActors,
  type BoardLane,
  type PixelActorState,
  type WorldZone,
} from "./view-model";

export function MissionPage({ missionId }: { missionId: string }) {
  const { t } = useT("orchestration");
  const workspaceId = useWorkspaceId();
  const projectionQuery = useQuery(
    missionProjectionOptions(workspaceId, missionId),
  );
  const refetchProjection = projectionQuery.refetch;
  const projection = projectionQuery.data;
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
  const [preferredRunId, setPreferredRunId] = useState("");

  const availableRunIds = useMemo(
    () =>
      new Set(
        projectionQuery.data?.nodes.flatMap((node) =>
          node.latestRun ? [node.latestRun.id] : [],
        ) ?? [],
      ),
    [projectionQuery.data],
  );
  const selectedRunId = preferredRunId.length > 0
    ? preferredRunId
    : (availableRunIds.values().next().value ?? "");
  const detailQuery = useQuery(
    missionRunDetailOptions(workspaceId, missionId, selectedRunId),
  );
  const budgetApproval = useMutation(approveMissionBudgetMutationOptions(missionId));
  const startMission = useMutation(startMissionMutationOptions(missionId));
  const cancelMission = useMutation(cancelMissionMutationOptions(missionId));
  const retryTask = useMutation(retryMissionTaskMutationOptions(missionId));

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
    <MissionWorkspace
      projection={projectionQuery.data}
      selectedRunId={selectedRunId}
      onSelectRun={setPreferredRunId}
      onRefresh={() => void projectionQuery.refetch()}
      isRefreshing={projectionQuery.isFetching}
      onApproveBudget={async (request) => {
        await budgetApproval.mutateAsync(request);
        await projectionQuery.refetch();
      }}
      budgetApproving={budgetApproval.isPending}
      budgetApprovalError={budgetApproval.error instanceof Error ? budgetApproval.error.message : undefined}
      onStart={async () => {
        await startMission.mutateAsync({
          commandId: generateUUID(),
          expectedRevision: projectionQuery.data.mission.revision,
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
      detail={detailQuery.data}
      detailLoading={detailQuery.isLoading && selectedRunId.length > 0}
      detailError={detailQuery.isError}
    />
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
  detail?: RunDetailProjection;
  detailLoading?: boolean;
  detailError?: boolean;
  onStart?: () => Promise<void>;
  onCancel?: () => Promise<void>;
  onRetryTask?: (node: TaskNodeProjection) => Promise<void>;
  lifecyclePending?: boolean;
  lifecycleError?: string;
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
  onStart,
  onCancel,
  onRetryTask,
  lifecyclePending = false,
  lifecycleError,
}: MissionWorkspaceProps) {
  const { t } = useT("orchestration");
  const { mission } = projection;

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
              <Button size="sm" onClick={() => void onStart()} disabled={lifecyclePending}>
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
      </header>

      <div className="grid min-h-0 flex-1 gap-4 overflow-auto p-4 md:p-6 xl:grid-cols-[minmax(18rem,0.9fr)_minmax(26rem,1.35fr)_minmax(20rem,1fr)] xl:overflow-hidden">
        <MissionBoard
          projection={projection}
          selectedRunId={selectedRunId}
          onSelectRun={onSelectRun}
        />
        <AgentWorld projection={projection} />
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

function MissionBoard({
  projection,
  selectedRunId,
  onSelectRun,
}: Pick<MissionWorkspaceProps, "projection" | "selectedRunId" | "onSelectRun">) {
  const { t } = useT("orchestration");
  const laneLabels: Record<BoardLane, string> = {
    queued: t(($) => $.board.queued),
    active: t(($) => $.board.active),
    review: t(($) => $.board.review),
    done: t(($) => $.board.done),
    attention: t(($) => $.board.attention),
  };
  const grouped = useMemo(() => {
    const lanes = new Map<BoardLane, TaskNodeProjection[]>(
      BOARD_LANES.map((lane) => [lane, []]),
    );
    for (const node of projection.nodes) {
      lanes.get(boardLaneForStatus(node.status))?.push(node);
    }
    return lanes;
  }, [projection.nodes]);
  const dag = useMemo(() => buildDagLayout(projection.nodes), [projection.nodes]);

  return (
    <Card className="min-h-[34rem] xl:min-h-0">
      <CardHeader className="border-b">
        <CardTitle className="flex items-center gap-2">
          <ClipboardList className="size-4" />
          {t(($) => $.board.title)}
        </CardTitle>
        <CardDescription>{t(($) => $.board.hint)}</CardDescription>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 overflow-auto">
        <div className="space-y-4">
          <MissionDag columns={dag} />
          {BOARD_LANES.map((lane) => {
            const nodes = grouped.get(lane) ?? [];
            return (
              <section key={lane} aria-labelledby={`lane-${lane}`}>
                <div className="mb-2 flex items-center justify-between">
                  <h2 id={`lane-${lane}`} className="text-caption font-semibold uppercase tracking-wide text-muted-foreground">
                    {laneLabels[lane]}
                  </h2>
                  <Badge variant="outline">{nodes.length}</Badge>
                </div>
                {nodes.length === 0 ? (
                  <div className="rounded-lg border border-dashed px-3 py-2 text-caption text-muted-foreground">
                    {t(($) => $.board.empty)}
                  </div>
                ) : (
                  <div className="space-y-2">
                    {nodes.map((node) => {
                      const runId = node.latestRun?.id ?? "";
                      const selected = runId.length > 0 && runId === selectedRunId;
                      return (
                        <button
                          key={node.id}
                          type="button"
                          disabled={!runId}
                          aria-pressed={selected}
                          aria-label={`${node.key} ${node.title}`}
                          onClick={() => onSelectRun(runId)}
                          className={cn(
                            "w-full rounded-lg border p-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default",
                            selected
                              ? "border-primary bg-primary/10 hover:border-primary hover:bg-primary/10"
                              : "border-border bg-card hover:border-primary/50 hover:bg-accent/40",
                          )}
                        >
                          <div className="flex items-start justify-between gap-2">
                            <div className="min-w-0">
                              <p className="truncate text-body font-medium">
                                <span className="mr-1.5 font-mono text-caption text-muted-foreground">{node.key}</span>
                                {node.title}
                              </p>
                              <p className="mt-1 line-clamp-2 text-caption text-muted-foreground">
                                {node.description || node.role}
                              </p>
                            </div>
                            <Badge variant={node.status === "failed" ? "destructive" : "outline"}>
                              {node.status}
                            </Badge>
                          </div>
                          <div className="mt-2 flex flex-wrap items-center gap-2 text-caption text-muted-foreground">
                            <span>{node.role}</span>
                            <span>·</span>
                            <span>{t(($) => $.board.dependencies, { count: node.dependencyIds.length })}</span>
                            <span>·</span>
                            <span>{node.latestRun ? `Run #${node.latestRun.attempt}` : t(($) => $.board.no_run)}</span>
                          </div>
                        </button>
                      );
                    })}
                  </div>
                )}
              </section>
            );
          })}
        </div>
      </CardContent>
    </Card>
  );
}

function MissionDag({ columns }: { columns: ReturnType<typeof buildDagLayout> }) {
  const { t } = useT("orchestration");
  return (
    <section className="rounded-lg border bg-muted/20 p-3" aria-labelledby="mission-dag-title">
      <div className="mb-3">
        <h2 id="mission-dag-title" className="flex items-center gap-2 text-caption font-semibold uppercase tracking-wide">
          <GitBranch className="size-3.5" />
          {t(($) => $.board.dag_title)}
        </h2>
        <p className="mt-1 text-caption text-muted-foreground">{t(($) => $.board.dag_hint)}</p>
      </div>
      {columns.length === 0 ? (
        <p className="text-caption text-muted-foreground">{t(($) => $.board.empty)}</p>
      ) : (
        <div className="flex gap-3 overflow-x-auto pb-1" data-testid="mission-dag">
          {columns.map((column, depth) => (
            <div key={depth} className="flex min-w-32 flex-1 flex-col gap-2" data-dag-depth={depth}>
              <span className="font-mono text-caption text-muted-foreground">{t(($) => $.board.dag_stage, { stage: depth + 1 })}</span>
              {column.map((node) => (
                <div key={node.id} className="rounded-md border bg-background p-2 text-caption" title={node.title}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate font-mono font-medium">{node.key}</span>
                    <span className="size-2 shrink-0 rounded-full bg-current opacity-60" data-status={node.status} />
                  </div>
                  <p className="mt-1 truncate text-muted-foreground">
                    {node.dependencyKeys.length > 0
                      ? `← ${node.dependencyKeys.join(", ")}`
                      : t(($) => $.board.dag_root)}
                  </p>
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function AgentWorld({ projection }: { projection: MissionProjection }) {
  const { t } = useT("orchestration");
  const zoneLabels: Record<WorldZone, string> = {
    lobby: t(($) => $.world.lobby),
    workshop: t(($) => $.world.workshop),
    reviewLab: t(($) => $.world.reviewLab),
    delivery: t(($) => $.world.delivery),
    blocked: t(($) => $.world.blocked),
  };
  const stateLabels: Record<PixelActorState, string> = {
    idle: t(($) => $.world.idle),
    walking: t(($) => $.world.walking),
    working: t(($) => $.world.working),
    reviewing: t(($) => $.world.reviewing),
    blocked: t(($) => $.world.blocked),
    done: t(($) => $.world.done),
  };
  const actors = useMemo(
    () => buildPixelActors(
      projection.team,
      projection.nodes,
      projection.mission.status,
      projection.activities.items,
    ),
    [projection.activities.items, projection.mission.status, projection.nodes, projection.team],
  );
  const agentsByZone = useMemo(() => {
    const zones = new Map<WorldZone, ReturnType<typeof buildPixelActors>>(
      WORLD_ZONES.map((zone) => [zone, []]),
    );
    for (const actor of actors) {
      zones.get(actor.zone)?.push(actor);
    }
    return zones;
  }, [actors]);

  return (
    <Card className="min-h-[38rem] xl:min-h-0">
      <CardHeader className="border-b">
        <CardTitle className="flex items-center gap-2">
          <Bot className="size-4" />
          {t(($) => $.world.title)}
        </CardTitle>
        <CardDescription>{t(($) => $.world.hint)}</CardDescription>
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto">
        <div className="grid flex-1 auto-rows-fr gap-3 rounded-xl border bg-muted/20 p-3 [background-image:linear-gradient(to_right,var(--border)_1px,transparent_1px),linear-gradient(to_bottom,var(--border)_1px,transparent_1px)] [background-size:24px_24px] sm:grid-cols-2">
          {WORLD_ZONES.map((zone) => {
            const agents = agentsByZone.get(zone) ?? [];
            return (
              <section
                key={zone}
                data-world-zone={zone}
                className={cn(
                  "min-h-32 rounded-lg border border-dashed bg-background/90 p-3 backdrop-blur-sm",
                  zone === "workshop" && "sm:row-span-2",
                )}
                aria-labelledby={`zone-${zone}`}
              >
                <div className="mb-3 flex items-center justify-between gap-2">
                  <h2 id={`zone-${zone}`} className="text-caption font-semibold uppercase tracking-wide text-muted-foreground">
                    {zoneLabels[zone]}
                  </h2>
                  <span className="font-mono text-caption text-muted-foreground">{agents.length}</span>
                </div>
                {agents.length === 0 ? (
                  <p className="text-caption text-muted-foreground">{t(($) => $.world.empty)}</p>
                ) : (
                  <div className="flex flex-wrap gap-3">
                    {agents.map(({ agent, node, state, slot, paletteIndex, action }) => {
                      return (
                        <div
                          key={`${agent.agentId}-${agent.role}`}
                          className="group flex w-24 flex-col items-center text-center"
                          data-agent-id={agent.agentId}
                          data-world-slot={slot}
                        >
                          <PixelActor paletteIndex={paletteIndex} state={state} action={action} role={agent.role} />
                          <p className="mt-1 w-full truncate text-caption font-medium">{agent.agentName}</p>
                          <p className="w-full truncate font-mono text-caption text-muted-foreground">{agent.role}</p>
                          <p className="w-full truncate text-caption text-muted-foreground">{node?.key ?? stateLabels[state]}</p>
                        </div>
                      );
                    })}
                  </div>
                )}
              </section>
            );
          })}
        </div>
        <section className="shrink-0" aria-labelledby="recent-activity">
          <h2 id="recent-activity" className="mb-2 flex items-center gap-2 text-caption font-semibold uppercase tracking-wide text-muted-foreground">
            <Activity className="size-3.5" />
            {t(($) => $.world.recent_activity)}
          </h2>
          {projection.activities.items.length === 0 ? (
            <p className="text-caption text-muted-foreground">{t(($) => $.world.no_activity)}</p>
          ) : (
            <div className="grid gap-2 sm:grid-cols-2">
              {projection.activities.items.slice(-4).reverse().map((item) => (
                <div key={item.id} className="flex items-center gap-2 rounded-lg bg-muted/60 px-2.5 py-2 text-caption">
                  <span className="font-mono text-muted-foreground">#{item.sequence}</span>
                  <span className="truncate">{item.type}</span>
                </div>
              ))}
            </div>
          )}
        </section>
      </CardContent>
    </Card>
  );
}

function PixelActor({
  paletteIndex,
  state,
  action,
  role,
}: {
  paletteIndex: number;
  state: PixelActorState;
  action: string;
  role: string;
}) {
  const atlasUrl = typeof agentRoleAtlas === "string" ? agentRoleAtlas : agentRoleAtlas.src;
  const column = pixelRoleColumn(role, paletteIndex);
  const row = pixelStateRow(state);
  return (
    <div
      data-actor-state={state}
      data-actor-action={action}
      data-actor-role={role}
      className={cn(
        "relative h-12 w-12 overflow-hidden rounded-md border border-foreground/20 bg-muted [image-rendering:pixelated]",
        state === "walking" && "motion-safe:animate-bounce",
        (state === "working" || state === "reviewing") && "motion-safe:animate-pulse",
        action === "celebrate" && "motion-safe:animate-bounce",
        action === "alert" && "ring-2 ring-destructive/70",
        state === "blocked" && "opacity-60 grayscale",
      )}
      style={{
        backgroundImage: `url(${atlasUrl})`,
        backgroundSize: "400% 600%",
        backgroundPosition: `${column * (100 / 3)}% ${row * 20}%`,
        backgroundRepeat: "no-repeat",
      }}
    />
  );
}

function pixelRoleColumn(role: string, paletteIndex: number) {
  const normalized = role.trim().toLowerCase();
  if (normalized.includes("plan")) return 0;
  if (normalized.includes("execut") || normalized.includes("engineer") || normalized.includes("worker")) return 1;
  if (normalized.includes("review") || normalized.includes("audit") || normalized.includes("inspect")) return 2;
  if (normalized.includes("integrat") || normalized.includes("lead") || normalized.includes("coordinat")) return 3;
  return paletteIndex % 4;
}

function pixelStateRow(state: PixelActorState) {
  return ({ idle: 0, walking: 1, working: 2, reviewing: 3, blocked: 4, done: 5 } satisfies Record<PixelActorState, number>)[state];
}

function RunDetailPanel({
  selectedRunId,
  projection,
  detail,
  loading,
  error,
  onSelectRun,
  onRetryTask,
  lifecyclePending,
}: {
  selectedRunId: string;
  projection: MissionProjection;
  detail?: RunDetailProjection;
  loading: boolean;
  error: boolean;
  onSelectRun: (runId: string) => void;
  onRetryTask?: (node: TaskNodeProjection) => Promise<void>;
  lifecyclePending: boolean;
}) {
  const { t } = useT("orchestration");
  const selectedNode = projection.nodes.find((node) => node.latestRun?.id === selectedRunId);

  return (
    <Card className="min-h-[34rem] xl:min-h-0">
      <CardHeader className="border-b">
        <CardTitle className="flex items-center gap-2">
          <GitBranch className="size-4" />
          {t(($) => $.detail.title)}
        </CardTitle>
        <CardDescription>{t(($) => $.detail.hint)}</CardDescription>
        {detail?.run ?? selectedNode?.latestRun ? (
          <CardAction>
            <Badge variant="outline">{(detail?.run ?? selectedNode?.latestRun)?.status}</Badge>
          </CardAction>
        ) : null}
      </CardHeader>
      <CardContent className="min-h-0 flex-1 overflow-auto">
        {!selectedRunId ? (
          <EmptyDetail />
        ) : loading ? (
          <div className="space-y-3" aria-label={t(($) => $.detail.loading)}>
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-32 w-full" />
          </div>
        ) : error || !detail ? (
          <div className="flex min-h-48 flex-col items-center justify-center gap-2 text-center">
            <AlertTriangle className="size-6 text-destructive" />
            <p className="text-body font-medium">{t(($) => $.detail.error)}</p>
          </div>
        ) : (
          <div className="space-y-5">
            <div>
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-title-sm font-semibold">{detail.node.key} · {detail.node.title}</h2>
                <Badge variant="secondary">{t(($) => $.detail.attempt, { attempt: detail.run.attempt })}</Badge>
              </div>
              <p className="mt-1 break-all font-mono text-caption text-muted-foreground">{detail.run.id}</p>
              {(detail.node.status === "failed" || detail.node.status === "blocked") && onRetryTask ? (
                <Button className="mt-3" size="sm" variant="outline" disabled={lifecyclePending} onClick={() => void onRetryTask(detail.node)}>
                  <RotateCcw className="size-4" />
                  {t(($) => $.detail.retry)}
                </Button>
              ) : null}
            </div>

            <DetailSection icon={LoaderCircle} title={t(($) => $.detail.execution)}>
              <KeyValue label={t(($) => $.detail.runtime)} value={detail.agent?.runtimeName ?? detail.assignment.runtimeId} />
              <KeyValue label="agent" value={detail.agent?.agentName ?? detail.assignment.agentId} />
              <KeyValue label="role" value={detail.agent?.role ?? detail.node.role} />
              <KeyValue label="provider / model" value={[detail.agent?.provider, detail.agent?.model].filter(Boolean).join(" / ") || "—"} />
              <KeyValue label="status" value={detail.execution?.status ?? detail.run.status} />
              {detail.run.failureMessage ? <KeyValue label={t(($) => $.detail.failure)} value={detail.run.failureMessage} danger /> : null}
            </DetailSection>

            <DetailSection icon={Box} title={t(($) => $.detail.artifacts)} count={detail.artifacts.length}>
              {detail.artifacts.length === 0 ? <EmptyRow /> : detail.artifacts.map((artifact) => (
                <div key={artifact.id} className="rounded-lg border p-2.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-caption font-medium">{artifact.kind}</span>
                    <Badge variant="outline">{t(($) => $.detail.version, { version: artifact.version })}</Badge>
                  </div>
                  {artifact.summary ? <p className="mt-1 text-caption text-muted-foreground">{artifact.summary}</p> : null}
                  {isWebUrl(artifact.uri) ? (
                    <a className="mt-1 block truncate text-caption text-primary underline-offset-4 hover:underline" href={artifact.uri} target="_blank" rel="noreferrer">
                      {artifact.uri}
                    </a>
                  ) : artifact.uri ? (
                    <p className="mt-1 break-all font-mono text-caption text-muted-foreground">{artifact.uri}</p>
                  ) : null}
                </div>
              ))}
            </DetailSection>

            <DetailSection icon={CheckCircle2} title={t(($) => $.detail.reviews)} count={detail.reviews.length}>
              {detail.reviews.length === 0 ? <EmptyRow /> : detail.reviews.map((review) => (
                <div key={review.id} className="rounded-lg border p-2.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-caption">{review.decision}</span>
                    <span className="text-caption text-muted-foreground">{review.requestedChanges.length}</span>
                  </div>
                  {review.requestedChanges.length > 0 ? (
                    <p className="mt-1 line-clamp-3 text-caption text-muted-foreground">{formatEvidence(review.requestedChanges)}</p>
                  ) : null}
                </div>
              ))}
            </DetailSection>

            <DetailSection icon={GitBranch} title={t(($) => $.detail.lineage)}>
              <div className="flex flex-wrap gap-2">
                <Badge variant="outline">{t(($) => $.detail.assignments, { count: detail.lineage.assignments.length })}</Badge>
                <Badge variant="outline">{t(($) => $.detail.runs, { count: detail.lineage.runs.length })}</Badge>
              </div>
              <div className="flex flex-wrap gap-2">
                {detail.lineage.runs.map((run) => (
                  <Button
                    key={run.id}
                    type="button"
                    size="sm"
                    variant={run.id === selectedRunId ? "secondary" : "outline"}
                    onClick={() => onSelectRun(run.id)}
                    aria-pressed={run.id === selectedRunId}
                  >
                    #{run.attempt} · {run.status}
                  </Button>
                ))}
              </div>
            </DetailSection>

            <DetailSection icon={MessageSquareText} title={t(($) => $.detail.messages)} count={detail.messages.length}>
              {detail.messages.length === 0 ? <EmptyRow /> : detail.messages.slice(-6).map((message) => (
                <div key={`${message.sequence}-${message.createdAt}`} className="rounded-lg bg-muted/60 p-2.5 text-caption">
                  <div className="flex items-center justify-between gap-2 font-mono text-muted-foreground">
                    <span>#{message.sequence} · {message.type}</span>
                    <span>{message.tool}</span>
                  </div>
                  {message.content ? <p className="mt-1 line-clamp-3 whitespace-pre-wrap">{message.content}</p> : null}
                </div>
              ))}
            </DetailSection>

            <DetailSection icon={Activity} title={t(($) => $.detail.usage)} count={detail.usage.length}>
              {detail.usage.length === 0 ? <EmptyRow /> : detail.usage.map((usage) => (
                <div key={`${usage.provider}-${usage.model}-${usage.createdAt}`} className="flex items-center justify-between gap-2 rounded-lg border p-2.5 text-caption">
                  <span className="truncate">{usage.provider} / {usage.model}</span>
                  <span className="shrink-0 tabular-nums text-muted-foreground">
                    {t(($) => $.detail.tokens, { count: usage.inputTokens + usage.outputTokens })}
                  </span>
                </div>
              ))}
            </DetailSection>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function isWebUrl(value: string) {
  return /^https?:\/\//i.test(value);
}

function formatEvidence(value: unknown) {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function DetailSection({ icon: Icon, title, count, children }: { icon: typeof Activity; title: string; count?: number; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="mb-2 flex items-center gap-2 text-caption font-semibold uppercase tracking-wide text-muted-foreground">
        <Icon className="size-3.5" />
        {title}
        {count !== undefined ? <span className="ml-auto tabular-nums">{count}</span> : null}
      </h3>
      <div className="space-y-2">{children}</div>
    </section>
  );
}

function KeyValue({ label, value, danger = false }: { label: string; value: string; danger?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-3 text-caption">
      <span className="text-muted-foreground">{label}</span>
      <span className={cn("break-all text-right font-mono", danger && "text-destructive")}>{value}</span>
    </div>
  );
}

function EmptyRow() {
  const { t } = useT("orchestration");
  return <p className="rounded-lg border border-dashed p-2.5 text-caption text-muted-foreground">{t(($) => $.detail.none)}</p>;
}

function EmptyDetail() {
  const { t } = useT("orchestration");
  return (
    <div className="flex min-h-72 flex-col items-center justify-center gap-2 text-center">
      <GitBranch className="size-7 text-muted-foreground" />
      <p className="text-body font-medium">{t(($) => $.detail.empty_title)}</p>
      <p className="max-w-xs text-caption text-muted-foreground">{t(($) => $.detail.empty_hint)}</p>
    </div>
  );
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
