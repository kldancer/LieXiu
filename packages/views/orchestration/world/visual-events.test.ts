import type { ActivityProjection } from "@liexiu/core/orchestration";
import { describe, expect, it } from "vitest";
import { activitiesToVisualEvents, activityToVisualEvent, visualEventKey } from "./visual-events";

function activity(overrides: Partial<ActivityProjection> = {}): ActivityProjection {
  return {
    id: "activity-1",
    taskNodeId: "task-1",
    runId: "run-1",
    type: "task.started",
    actorType: "agent",
    actorId: "agent-1",
    subjectType: "task_node",
    subjectId: "task-1",
    causationId: "cause-1",
    correlationId: "correlation-1",
    payloadVersion: 1,
    payload: { arbitrary: "ignored" },
    sequence: 1,
    occurredAt: "2026-08-19T00:00:00.000Z",
    ...overrides,
  };
}

describe("Activity to VisualEvent", () => {
  it.each([
    ["mission.created", "mission.created", "mission", "mission-1"],
    ["mission.plan_requested", "mission.plan_requested", "run", "run-1"],
    ["mission.plan_accepted", "mission.plan_accepted", "mission", "mission-1"],
    ["task.ready", "task.ready", "task_node", "task-1"],
    ["task.retry_requested", "task.retry_requested", "task_node", "task-1"],
    ["budget.approved", "budget.approved", "mission", "mission-1"],
    ["human_gate.required", "human_gate.required", "task_node", "task-1"],
    ["human_gate.resolved", "human_gate.resolved", "task_node", "task-1"],
    ["plan_proposal.edited", "plan_proposal.edited", "artifact", "artifact-1"],
    ["plan_proposal.rejected", "plan_proposal.rejected", "artifact", "artifact-1"],
  ] as const)("maps existing %s activity", (type, kind, subjectType, subjectId) => {
    expect(activityToVisualEvent(activity({ type, subjectType, subjectId }))).toEqual(
      expect.objectContaining({ kind, target: expect.objectContaining({ id: subjectId }) }),
    );
  });

  it("maps to a bounded event without passing payload or renderer fields", () => {
    const event = activityToVisualEvent(activity({ type: "task.blocked" }));
    expect(event).toEqual({
      key: "activity:activity-1",
      activityId: "activity-1",
      sequence: 1,
      kind: "agent.blocked",
      target: { type: "task", id: "task-1" },
      priority: "critical",
    });
    expect(event && Object.keys(event)).not.toContain("payload");
  });

  it("uses a stable idempotency key and converges duplicate sequences", () => {
    const first = activity({ id: "z", sequence: 2, type: "run.started", subjectType: "run", subjectId: "run-1" });
    const winner = activity({ id: "a", sequence: 2, type: "run.succeeded", subjectType: "run", subjectId: "run-1" });
    expect(visualEventKey(first)).toBe("activity:z");
    expect(activitiesToVisualEvents([first, winner])).toEqual([
      expect.objectContaining({ activityId: "a", sequence: 2, kind: "run.succeeded" }),
    ]);
    expect(activitiesToVisualEvents([winner, first])).toEqual(activitiesToVisualEvents([first, winner]));
  });

  it("sorts by sequence independent of input order", () => {
    const events = activitiesToVisualEvents([
      activity({ id: "third", sequence: 3, type: "mission.completed", subjectType: "mission", subjectId: "mission-1" }),
      activity({ id: "first", sequence: 1 }),
      activity({ id: "second", sequence: 2, type: "artifact.created", subjectType: "artifact", subjectId: "artifact-1" }),
    ]);
    expect(events.map(({ sequence }) => sequence)).toEqual([1, 2, 3]);
  });

  it("fails soft for unknown, unsupported, and malformed activities", () => {
    expect(activityToVisualEvent(activity({ type: "future.event" }))).toBeNull();
    expect(activityToVisualEvent(activity({ subjectType: "unsupported" }))).toBeNull();
    expect(activityToVisualEvent(activity({ id: "", sequence: 0 }))).toBeNull();
    expect(activitiesToVisualEvents([null as unknown as ActivityProjection, {} as ActivityProjection])).toEqual([]);
  });
});
