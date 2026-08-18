"use client";

import { useEffect, useMemo, useState, type Dispatch, type SetStateAction } from "react";
import type { MissionProjection } from "@liexiu/core/orchestration";
import { Button } from "@liexiu/ui/components/ui/button";
import { AgentWorld } from "./agent-world";
import {
  createReplayModel,
  advanceReplay,
  pauseReplay,
  playReplay,
  replayEvents,
  seekReplay,
  setReplayFilter,
  setReplayRate,
  type ReplayFilter,
  type ReplayModel,
} from "./world/replay-model";
import { activitiesToVisualEvents, type VisualEvent } from "./world/visual-events";
import { buildWorldModel } from "./world/world-model";

const REPLAY_RATES = [0.5, 1, 2, 4] as const;
const TICK_MS = 100;

export interface MissionReplayLabels {
  play: string;
  pause: string;
  sequence: string;
  actor: string;
  task: string;
  run: string;
  all: string;
  rateLabels: Record<(typeof REPLAY_RATES)[number], string>;
  events: string;
}

export interface MissionReplayProps {
  projection: MissionProjection;
  onSelectRun: (runId: string) => void;
  labels: MissionReplayLabels;
  renderWorld?: boolean;
  onVisibleEventsChange?: (events: readonly VisualEvent[]) => void;
}

export function MissionReplay({ projection, onSelectRun, labels, renderWorld = true, onVisibleEventsChange }: MissionReplayProps) {
  const baseModel = useMemo(() => {
    const snapshot = buildWorldModel(projection);
    const events = activitiesToVisualEvents(projection.activities.items);
    const snapshotSequence = projection.activities.firstSequence > 0
      ? projection.activities.firstSequence - 1
      : 0;
    return createReplayModel(snapshot, events, { snapshotSequence });
  }, [projection]);
  const [model, setModel] = useState<ReplayModel>(baseModel);

  useEffect(() => setModel(baseModel), [baseModel]);

  useEffect(() => {
    if (model.status !== "playing") return;
    const timer = window.setInterval(() => {
      setModel((current) => advanceReplay(current, TICK_MS));
    }, TICK_MS);
    return () => window.clearInterval(timer);
  }, [model.status]);

  const visibleEvents = useMemo(() => replayEvents(model), [model]);
  const lastSequence = model.events.at(-1)?.sequence ?? model.snapshotSequence;
  const filter = model.filter;
  const runs = [...new Map([
    ...projection.planning.runs.map((run) => [run.id, run] as const),
    ...projection.nodes.flatMap((node) => node.latestRun ? [[node.latestRun.id, node.latestRun] as const] : []),
  ]).values()].sort((left, right) => left.id.localeCompare(right.id));

  useEffect(() => {
    onVisibleEventsChange?.(visibleEvents);
  }, [onVisibleEventsChange, visibleEvents]);

  return (
    <section aria-label={labels.sequence}>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          aria-label={model.status === "playing" ? labels.pause : labels.play}
          onClick={() => setModel((current) => current.status === "playing" ? pauseReplay(current) : playReplay(current))}
        >
          {model.status === "playing" ? labels.pause : labels.play}
        </Button>
        {REPLAY_RATES.map((rate) => (
          <Button
            key={rate}
            type="button"
            variant={model.rate === rate ? "default" : "outline"}
            size="sm"
            aria-pressed={model.rate === rate}
            onClick={() => setModel((current) => setReplayRate(current, rate))}
          >
            {labels.rateLabels[rate]}
          </Button>
        ))}
        <label className="flex min-w-52 flex-1 items-center gap-2 text-caption">
          <span>{labels.sequence}</span>
          <input
            aria-label={labels.sequence}
            type="range"
            min={model.snapshotSequence}
            max={lastSequence}
            step="0.01"
            value={model.cursor}
            onChange={(event) => setModel((current) => seekReplay(current, Number(event.target.value)))}
            className="min-w-0 flex-1"
          />
          <output>{formatSequence(model.cursor)}</output>
        </label>
      </div>

      <div className="mt-3 grid gap-2 sm:grid-cols-3">
        <ReplayFilterSelect
          label={labels.actor}
          value={filter.actorId ?? ""}
          options={model.snapshot.actors.map((actor) => ({ id: actor.agentId, label: actor.name }))}
          allLabel={labels.all}
          onChange={(actorId) => updateFilter(setModel, { ...filter, actorId })}
        />
        <ReplayFilterSelect
          label={labels.task}
          value={filter.taskId ?? ""}
          options={projection.nodes.map((node) => ({ id: node.id, label: node.title }))}
          allLabel={labels.all}
          onChange={(taskId) => updateFilter(setModel, { ...filter, taskId })}
        />
        <ReplayFilterSelect
          label={labels.run}
          value={filter.runId ?? ""}
          options={runs.map((run) => ({ id: run.id, label: run.id }))}
          allLabel={labels.all}
          onChange={(runId) => updateFilter(setModel, { ...filter, runId })}
        />
      </div>

      {renderWorld ? <div className="mt-4">
        <AgentWorld
          projection={projection}
          onSelectRun={onSelectRun}
          visualEventsOverride={visibleEvents}
          compactActivity
        />
      </div> : null}

      <div aria-label={labels.events} className="mt-4 flex flex-wrap gap-2">
        {visibleEvents.map((event) => (
          <button
            key={event.key}
            type="button"
            className="rounded border px-2 py-1 text-caption hover:bg-muted"
            onClick={() => selectEventRun(event, projection, onSelectRun)}
          >
            {event.sequence}
          </button>
        ))}
      </div>
    </section>
  );
}

function ReplayFilterSelect({ label, value, options, allLabel, onChange }: {
  label: string;
  value: string;
  options: { id: string; label: string }[];
  allLabel: string;
  onChange: (value: string | undefined) => void;
}) {
  return (
    <label className="flex items-center gap-2 text-caption">
      <span>{label}</span>
      <select aria-label={label} value={value} onChange={(event) => onChange(event.target.value || undefined)} className="min-w-0 flex-1 rounded border bg-background px-2 py-1">
        <option value="">{allLabel}</option>
        {options.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}
      </select>
    </label>
  );
}

function updateFilter(setModel: Dispatch<SetStateAction<ReplayModel>>, filter: ReplayFilter) {
  setModel((current) => setReplayFilter(current, filter));
}

function selectEventRun(event: VisualEvent, projection: MissionProjection, onSelectRun: (runId: string) => void) {
  const runId = event.target.type === "run"
    ? event.target.id
    : event.target.type === "task"
      ? projection.nodes.find((node) => node.id === event.target.id)?.latestRun?.id
      : undefined;
  if (runId) onSelectRun(runId);
}

function formatSequence(sequence: number): string {
  return Number.isInteger(sequence) ? String(sequence) : sequence.toFixed(2);
}
