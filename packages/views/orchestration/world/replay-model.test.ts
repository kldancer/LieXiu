import { describe, expect, it } from "vitest";
import type { WorldModel } from "./world-model";
import type { VisualEvent } from "./visual-events";
import { advanceReplay, createReplayModel, pauseReplay, playReplay, replayEvents, reduceReplay, seekReplay, setReplayFilter, setReplayRate } from "./replay-model";

const snapshot: WorldModel = {
  missionId: "mission-1", revision: 3, missionStatus: "running", zones: [],
  actors: [{ id: "actor-1", agentId: "agent-1", runtimeId: "runtime-1", name: "A", duty: "executor", zone: "execution_workshop", status: "running", nodeId: "task-1", runId: "run-1", slot: 0 }],
  artifacts: [], signals: [],
};

function event(sequence: number, target: VisualEvent["target"], kind: VisualEvent["kind"] = "task.ready"): VisualEvent {
  return { key: `activity:${sequence}`, activityId: `activity-${sequence}`, sequence, kind, target, priority: "normal" };
}

describe("Replay model", () => {
  it("sorts, deduplicates by sequence, and starts at the snapshot cursor", () => {
    const model = createReplayModel(snapshot, [event(3, { type: "run", id: "run-1" }), event(1, { type: "task", id: "task-1" }), event(3, { type: "run", id: "run-1" })], { snapshotSequence: 1 });
    expect(model.events.map(({ sequence }) => sequence)).toEqual([3]);
    expect(model.cursor).toBe(1);
    expect(replayEvents(seekReplay(model, 3))).toHaveLength(1);
  });

  it("supports play, pause, deterministic rate-scaled advance, and bounded seek", () => {
    let model = createReplayModel(snapshot, [event(2, { type: "task", id: "task-1" }), event(10, { type: "mission", id: "mission-1" })], { snapshotSequence: 1 });
    model = setReplayRate(playReplay(model), 2);
    model = advanceReplay(model, 1_500);
    expect(model).toMatchObject({ status: "playing", rate: 2, cursor: 4 });
    model = pauseReplay(model);
    expect(advanceReplay(model, 100)).toBe(model);
    expect(seekReplay(model, -5).cursor).toBe(1);
    expect(seekReplay(model, 100).cursor).toBe(10);
    expect(setReplayRate(model, Number.NaN).rate).toBe(1);
  });

  it("filters by actor, task, and run without changing the cursor", () => {
    const model = createReplayModel(snapshot, [
      event(1, { type: "task", id: "task-1" }), event(2, { type: "run", id: "run-1" }), event(3, { type: "task", id: "other-task" }),
    ], { cursor: 3 });
    expect(replayEvents(setReplayFilter(model, { actorId: "agent-1" })).map(({ sequence }) => sequence)).toEqual([1, 2]);
    expect(replayEvents(setReplayFilter(model, { taskId: "task-1" })).map(({ sequence }) => sequence)).toEqual([1]);
    expect(replayEvents(setReplayFilter(model, { runId: "run-1" })).map(({ sequence }) => sequence)).toEqual([2]);
    expect(setReplayFilter(model, { taskId: "task-1" }).cursor).toBe(3);
  });

  it("ignores malformed or unknown events and never mutates the snapshot", () => {
    const unknown = event(2, { type: "mission", id: "mission-1" }, "future.event" as VisualEvent["kind"]);
    const model = createReplayModel(snapshot, [unknown, null as unknown as VisualEvent, event(1, { type: "task", id: "task-1" })], { cursor: 2 });
    expect(replayEvents(model)).toEqual([expect.objectContaining({ sequence: 1 })]);
    expect(model.snapshot).toBe(snapshot);
    expect(reduceReplay(model, { type: "tick", elapsedMs: 1 }).snapshot).toBe(snapshot);
  });
});
