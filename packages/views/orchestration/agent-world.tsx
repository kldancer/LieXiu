"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Activity, Bot, MessageSquareText } from "lucide-react";
import type { MissionProjection } from "@liexiu/core/orchestration";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@liexiu/ui/components/ui/card";
import { Button } from "@liexiu/ui/components/ui/button";
import { cn } from "@liexiu/ui/lib/utils";
import { useT } from "../i18n";
import agentRoleAtlas from "./agent-role-atlas-v1.png";
import {
  buildMailboxActivityViews,
  type MailboxActivityView,
  type PixelActorState,
} from "./view-model";
import { MailboxActivityList } from "./mission-mailbox-activity";
import {
  WORLD_ZONES,
  buildWorldModel,
  type WorldActorModel,
  type WorldZone,
} from "./world/world-model";
import { activitiesToVisualEvents } from "./world/visual-events";
import type { VisualEvent } from "./world/visual-events";
import type { WorldRendererController } from "./world/phaser-scene";
import { applyWorldPerformanceBudget } from "./world/world-performance";

export interface AgentWorldProps {
  projection: MissionProjection;
  onSelectRun: (runId: string) => void;
  visualEventsOverride?: readonly VisualEvent[];
  compactActivity?: boolean;
  motionPaused?: boolean;
  onMotionPausedChange?: (paused: boolean) => void;
  lowPerformance?: boolean;
  onLowPerformanceChange?: (enabled: boolean) => void;
}

export function AgentWorld({
  projection,
  onSelectRun,
  visualEventsOverride,
  compactActivity = false,
  motionPaused: controlledMotionPaused,
  onMotionPausedChange,
  lowPerformance: controlledLowPerformance,
  onLowPerformanceChange,
}: AgentWorldProps) {
  const { t } = useT("orchestration");
  const rendererContainerRef = useRef<HTMLDivElement>(null);
  const rendererRef = useRef<WorldRendererController | null>(null);
  const [rendererState, setRendererState] = useState<"loading" | "ready" | "fallback">("loading");
  const [selectedZone, setSelectedZone] = useState<WorldZone | null>(null);
  const [systemReducedMotion, setSystemReducedMotion] = useState(false);
  const [localMotionPaused, setLocalMotionPaused] = useState(false);
  const [localLowPerformance, setLocalLowPerformance] = useState(false);
  const motionPaused = controlledMotionPaused ?? localMotionPaused;
  const lowPerformance = controlledLowPerformance ?? localLowPerformance;
  const setMotionPaused = onMotionPausedChange ?? setLocalMotionPaused;
  const setLowPerformance = onLowPerformanceChange ?? setLocalLowPerformance;
  const reducedMotion = systemReducedMotion || motionPaused;
  const zoneLabels: Record<WorldZone, string> = {
    planning_observatory: t(($) => $.world.lobby),
    execution_workshop: t(($) => $.world.workshop),
    review_archive: t(($) => $.world.reviewLab),
    integration_forge: t(($) => $.world.integration),
    blocked_corner: t(($) => $.world.blocked),
    delivery_plaza: t(($) => $.world.delivery),
  };
  const stateLabels: Record<PixelActorState, string> = {
    idle: t(($) => $.world.idle),
    walking: t(($) => $.world.walking),
    working: t(($) => $.world.working),
    reviewing: t(($) => $.world.reviewing),
    blocked: t(($) => $.world.blocked),
    done: t(($) => $.world.done),
  };
  const worldModel = useMemo(() => buildWorldModel(projection), [projection]);
  const projectionVisualEvents = useMemo(
    () => activitiesToVisualEvents(projection.activities.items),
    [projection.activities.items],
  );
  const visualEvents = visualEventsOverride ?? projectionVisualEvents;
  const performance = useMemo(() => applyWorldPerformanceBudget({
    actors: worldModel.actors,
    artifacts: worldModel.artifacts,
    signals: worldModel.signals,
    visualEvents,
  }, lowPerformance ? "low" : "default"), [lowPerformance, visualEvents, worldModel.actors, worldModel.artifacts, worldModel.signals]);
  const renderedWorldModel = useMemo(() => {
    const actorIds = new Set(performance.actors.map((actor) => actor.id));
    return {
      ...worldModel,
      actors: [...performance.actors],
      artifacts: [...performance.artifacts],
      signals: [...performance.signals],
      zones: worldModel.zones.map((zone) => ({ ...zone, actorIds: zone.actorIds.filter((id) => actorIds.has(id)) })),
    };
  }, [performance.actors, performance.artifacts, performance.signals, worldModel]);
  const latestInputRef = useRef({ worldModel: renderedWorldModel, visualEvents: performance.visualEvents, onSelectRun });
  latestInputRef.current = { worldModel: renderedWorldModel, visualEvents: performance.visualEvents, onSelectRun };

  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const query = window.matchMedia("(prefers-reduced-motion: reduce)");
    const sync = () => setSystemReducedMotion(query.matches);
    sync();
    query.addEventListener?.("change", sync);
    return () => query.removeEventListener?.("change", sync);
  }, []);

  useEffect(() => {
    const container = rendererContainerRef.current;
    if (!container) return;
    let cancelled = false;
    let controller: WorldRendererController | null = null;
    setRendererState("loading");

    void import("./world/phaser-scene")
      .then(async ({ createWorldRenderer }) => {
        if (cancelled) return;
        controller = createWorldRenderer({
          onActorClick: (actorId) => {
            const current = latestInputRef.current;
            const runId = current.worldModel.actors.find((actor) => actor.id === actorId)?.runId;
            if (runId) current.onSelectRun(runId);
          },
          onZoneClick: (zoneId) => setSelectedZone((current) => current === zoneId ? null : zoneId),
          onArtifactClick: (artifactId) => {
            const current = latestInputRef.current;
            const runId = current.worldModel.artifacts.find((artifact) => artifact.id === artifactId)?.runId;
            if (runId) current.onSelectRun(runId);
          },
          onSignalClick: (signalId) => {
            const current = latestInputRef.current;
            const signal = current.worldModel.signals.find((candidate) => candidate.id === signalId);
            const runId = signal?.runId
              ?? current.worldModel.artifacts.find((artifact) => artifact.id === signal?.artifactId)?.runId
              ?? current.worldModel.actors.find((actor) => actor.id === signal?.actorId)?.runId;
            if (runId) current.onSelectRun(runId);
          },
          reducedMotion,
        });
        rendererRef.current = controller;
        const current = latestInputRef.current;
        controller.update(current.worldModel, current.visualEvents);
        await controller.mount(container);
        if (!cancelled) setRendererState("ready");
      })
      .catch(() => {
        if (!cancelled) setRendererState("fallback");
      });

    return () => {
      cancelled = true;
      controller?.destroy();
      if (rendererRef.current === controller) rendererRef.current = null;
    };
  }, [reducedMotion]);

  useEffect(() => {
    rendererRef.current?.update(renderedWorldModel, performance.visualEvents);
  }, [performance.visualEvents, renderedWorldModel]);
  const actorsByZone = useMemo(() => {
    const zones = new Map<WorldZone, WorldActorModel[]>(
      WORLD_ZONES.map((zone) => [zone, []]),
    );
    for (const actor of renderedWorldModel.actors) {
      zones.get(actor.zone)?.push(actor);
    }
    return zones;
  }, [renderedWorldModel.actors]);
  const mailboxActivities = useMemo(
    () => buildMailboxActivityViews(projection.activities.items),
    [projection.activities.items],
  );
  const latestMailboxByAgent = useMemo(() => {
    const result = new Map<string, MailboxActivityView>();
    for (const activity of mailboxActivities) {
      if (activity.actorType === "agent" && activity.actorId) result.set(activity.actorId, activity);
      if (activity.recipientType === "agent") result.set(activity.recipientId, activity);
    }
    return result;
  }, [mailboxActivities]);

  return (
    <Card className="min-h-[38rem] xl:min-h-0">
      <CardHeader className="border-b">
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <Bot className="size-4" />
              {t(($) => $.world.title)}
            </CardTitle>
            <CardDescription>{t(($) => $.world.hint)}</CardDescription>
          </div>
          <Button type="button" variant="outline" size="sm" aria-pressed={motionPaused} onClick={() => setMotionPaused(!motionPaused)}>
            {t(($) => motionPaused ? $.world.resume_motion : $.world.pause_motion)}
          </Button>
          <Button type="button" variant="outline" size="sm" aria-pressed={lowPerformance} onClick={() => setLowPerformance(!lowPerformance)}>
            {t(($) => lowPerformance ? $.world.full_performance : $.world.low_performance)}
          </Button>
        </div>
        <span className="sr-only" aria-live="polite">
          {reducedMotion ? t(($) => $.world.reduced_motion) : t(($) => $.world.motion_enabled)}
        </span>
        {performance.degraded ? (
          <span className="text-caption text-muted-foreground" role="status">
            {t(($) => $.world.performance_degraded, { count: Object.values(performance.dropped).reduce((sum, count) => sum + count, 0) })}
          </span>
        ) : null}
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto">
        <div
          ref={rendererContainerRef}
          data-testid="phaser-world-host"
          data-renderer-state={rendererState}
          data-world-zone-filter={selectedZone ?? "all"}
          className={cn(
            "min-h-[32rem] overflow-hidden rounded-xl border bg-muted/20 [&>canvas]:block [&>canvas]:h-auto [&>canvas]:max-w-full",
            rendererState === "fallback" && "hidden",
          )}
        />
        <div className={cn(
          "grid flex-1 auto-rows-fr gap-3 rounded-xl border bg-muted/20 p-3 [background-image:linear-gradient(to_right,var(--border)_1px,transparent_1px),linear-gradient(to_bottom,var(--border)_1px,transparent_1px)] [background-size:24px_24px] sm:grid-cols-2",
          rendererState === "ready" && "sr-only",
        )}>
          {WORLD_ZONES.map((zone) => {
            const actors = actorsByZone.get(zone) ?? [];
            const artifacts = renderedWorldModel.artifacts.filter((artifact) => artifact.zone === zone);
            const signals = renderedWorldModel.signals.filter((signal) => signal.zone === zone);
            return (
              <section
                key={zone}
                data-world-zone={zone}
                className={cn(
                  "min-h-32 rounded-lg border border-dashed bg-background/90 p-3 backdrop-blur-sm",
                  zone === "execution_workshop" && "sm:row-span-2",
                  selectedZone && selectedZone !== zone && "opacity-40",
                )}
                aria-labelledby={`zone-${zone}`}
                data-zone-selected={selectedZone === zone ? "true" : "false"}
              >
                <div className="mb-3 flex items-center justify-between gap-2">
                  <button
                    id={`zone-${zone}`}
                    type="button"
                    className="text-caption font-semibold uppercase tracking-wide text-muted-foreground hover:text-foreground"
                    onClick={() => setSelectedZone((current) => current === zone ? null : zone)}
                  >
                    {zoneLabels[zone]}
                  </button>
                  <span className="font-mono text-caption text-muted-foreground">{actors.length}</span>
                </div>
                {actors.length === 0 ? (
                  <p className="text-caption text-muted-foreground">{t(($) => $.world.empty)}</p>
                ) : (
                  <div className="flex flex-wrap gap-3">
                    {actors.map((actor) => {
                      const mailbox = latestMailboxByAgent.get(actor.agentId);
                      const state = pixelStateForActor(actor);
                      return (
                        <div
                          key={actor.id}
                          className="group flex w-24 flex-col items-center text-center"
                          data-agent-id={actor.agentId}
                          data-world-slot={actor.slot}
                        >
                          <button type="button" disabled={!actor.runId} onClick={() => actor.runId && onSelectRun(actor.runId)}>
                            <PixelActor paletteIndex={actor.slot} state={state} action={actor.status} duty={actor.duty} />
                          </button>
                          <p className="mt-1 w-full truncate text-caption font-medium">{actor.name}</p>
                          <p className="w-full truncate font-mono text-caption text-muted-foreground">{actor.duty}</p>
                          <p className="w-full truncate text-caption text-muted-foreground">{stateLabels[state]}</p>
                          {mailbox ? (
                            <button
                              type="button"
                              className="mt-1 flex max-w-full items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-caption text-muted-foreground hover:text-foreground disabled:cursor-default"
                              disabled={!mailbox.runId}
                              onClick={() => mailbox.runId && onSelectRun(mailbox.runId)}
                              data-mailbox-message-id={mailbox.messageId}
                            >
                              <MessageSquareText className="size-3 shrink-0" />
                              <span className="truncate">{mailbox.messageType}</span>
                            </button>
                          ) : null}
                        </div>
                      );
                    })}
                  </div>
                )}
                <div className="mt-2 flex flex-wrap gap-1">
                  {artifacts.map((artifact) => (
                    <button key={artifact.id} type="button" className="rounded border px-1.5 py-0.5 text-caption" onClick={() => onSelectRun(artifact.runId)} data-artifact-id={artifact.id}>
                      {`${artifact.kind} · ${artifact.version}`}
                    </button>
                  ))}
                  {signals.map((signal) => (
                    <button
                      key={signal.id}
                      type="button"
                      className="rounded bg-muted px-1.5 py-0.5 text-caption"
                      disabled={!signal.runId && !signal.artifactId && !signal.actorId}
                      onClick={() => {
                        const runId = signal.runId
                          ?? renderedWorldModel.artifacts.find((artifact) => artifact.id === signal.artifactId)?.runId
                          ?? renderedWorldModel.actors.find((actor) => actor.id === signal.actorId)?.runId;
                        if (runId) onSelectRun(runId);
                      }}
                      data-world-signal-id={signal.id}
                    >
                      {signal.kind.replaceAll("_", " ")}
                    </button>
                  ))}
                </div>
              </section>
            );
          })}
        </div>
        {compactActivity ? null : <MailboxActivityList
          items={mailboxActivities.slice(-4).reverse()}
          onSelectRun={onSelectRun}
        />}
        {compactActivity ? null : <section className="shrink-0" aria-labelledby="recent-activity">
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
        </section>}
      </CardContent>
    </Card>
  );
}

function pixelStateForActor(actor: WorldActorModel): PixelActorState {
  if (actor.status === "delivered") return "done";
  if (actor.status === "blocked" || actor.status === "offline") return "blocked";
  if (actor.status === "running") return actor.duty === "reviewer" ? "reviewing" : "working";
  return "idle";
}

function PixelActor({
  paletteIndex,
  state,
  action,
  duty,
}: {
  paletteIndex: number;
  state: PixelActorState;
  action: string;
  duty: string;
}) {
  const atlasUrl = typeof agentRoleAtlas === "string" ? agentRoleAtlas : agentRoleAtlas.src;
  const column = pixelDutyColumn(duty, paletteIndex);
  const row = pixelStateRow(state);
  return (
    <div
      data-actor-state={state}
      data-actor-action={action}
      data-actor-duty={duty}
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

function pixelDutyColumn(duty: string, paletteIndex: number) {
  const normalized = duty.trim().toLowerCase();
  if (normalized.includes("plan")) return 0;
  if (normalized.includes("execut") || normalized.includes("engineer") || normalized.includes("worker")) return 1;
  if (normalized.includes("review") || normalized.includes("audit") || normalized.includes("inspect")) return 2;
  if (normalized.includes("integrat") || normalized.includes("lead") || normalized.includes("coordinat")) return 3;
  return paletteIndex % 4;
}

function pixelStateRow(state: PixelActorState) {
  return ({ idle: 0, walking: 1, working: 2, reviewing: 3, blocked: 4, done: 5 } satisfies Record<PixelActorState, number>)[state];
}
