import { describe, expect, it } from "vitest";
import type { VisualEvent } from "./visual-events";
import { applyWorldPerformanceBudget, type WorldPerformanceInput } from "./world-performance";

const event = (sequence: number, priority: VisualEvent["priority"], key = `activity:${sequence}`): VisualEvent => ({ key, activityId: key, sequence, kind: "run.started", target: { type: "run", id: key }, priority });
const item = (id: string): { id: string } => ({ id });

describe("world performance budget", () => {
  it("uses bounded default and low-performance budgets", () => {
    const input = { actors: Array.from({ length: 50 }, (_, i) => item(`a${i}`)), artifacts: Array.from({ length: 70 }, (_, i) => item(`f${i}`)), signals: [], visualEvents: [] } as unknown as WorldPerformanceInput;
    expect(applyWorldPerformanceBudget(input).actors).toHaveLength(48);
    expect(applyWorldPerformanceBudget(input, "low").actors).toHaveLength(24);
  });

  it("retains critical items first and is independent of input order", () => {
    const input = { actors: [], artifacts: [], signals: [
      { ...item("normal") , severity: "info" }, { ...item("critical"), severity: "critical" },
    ] as never[], visualEvents: [event(2, "low"), event(1, "critical")] };
    const budget = { actors: 0, artifacts: 0, signals: 1, visualEvents: 1 };
    const first = applyWorldPerformanceBudget(input, "default", budget);
    const second = applyWorldPerformanceBudget({ ...input, signals: [...input.signals].reverse(), visualEvents: [...input.visualEvents].reverse() }, "default", budget);
    expect(first.signals.map((x) => x.id)).toEqual(["critical"]);
    expect(second).toEqual(first);
  });

  it("deduplicates sequence deterministically and reports degradation", () => {
    const result = applyWorldPerformanceBudget({ actors: [], artifacts: [], signals: [], visualEvents: [event(4, "normal", "z"), event(4, "critical", "a"), event(5, "low")] }, "default", { actors: 0, artifacts: 0, signals: 0, visualEvents: 1 });
    expect(result.visualEvents).toEqual([expect.objectContaining({ sequence: 4, key: "a" })]);
    expect(result.dropped.visualEvents).toBe(2);
    expect(result.degraded).toBe(true);
  });

  it("culls entities outside the camera-visible Zone set before applying limits", () => {
    const actor = (id: string, zone: "execution_workshop" | "review_archive") => ({
      id, agentId: id, runtimeId: id, name: id, duty: "executor" as const, zone, status: "running" as const, slot: 0,
    });
    const result = applyWorldPerformanceBudget({
      actors: [actor("visible", "execution_workshop"), actor("offscreen", "review_archive")],
      artifacts: [], signals: [], visualEvents: [], visibleZones: ["execution_workshop"],
    });
    expect(result.actors.map(({ id }) => id)).toEqual(["visible"]);
    expect(result.dropped.actors).toBe(1);
    expect(result.degraded).toBe(true);
  });
});
