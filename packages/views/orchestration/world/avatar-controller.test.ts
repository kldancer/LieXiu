import { describe, expect, it } from "vitest";
import type { WorldActorModel } from "./world-model";
import { AvatarController, AVATAR_ANIMATION_ROWS, DUTY_ATLAS_COLUMNS } from "./avatar-controller";
import type { VisualEvent } from "./visual-events";

describe("AvatarController", () => {
  it("keeps the four duty columns stable regardless of actor identity", () => {
    expect(["planner", "executor", "reviewer", "integrator"].map((duty) => {
      const state = new AvatarController().update(actor(duty as WorldActorModel["duty"], `agent-${duty}`));
      return [state.duty, state.frameColumn];
    })).toEqual([["planner", 0], ["executor", 1], ["reviewer", 2], ["integrator", 3]]);
    expect(DUTY_ATLAS_COLUMNS).toEqual({ planner: 0, executor: 1, reviewer: 2, integrator: 3 });
  });

  it.each([
    ["idle", "idle"], ["running", "work"], ["blocked", "blocked"],
    ["offline", "blocked"], ["delivered", "celebrate"],
  ] as const)("maps %s status to %s", (status, animation) => {
    expect(new AvatarController().update(actor("executor", "agent", status)).animation).toBe(animation);
  });

  it("accepts a matching visual cue for walk/review and ignores another actor's cue", () => {
    const controller = new AvatarController();
    const snapshot = actor("reviewer", "agent-review", "running");
    expect(controller.update(snapshot, event("agent.assigned", "task", "node-1")).animation).toBe("walk");
    expect(controller.update(snapshot, event("agent.reviewing", "task", "other-node")).animation).toBe("work");
    expect(controller.update(snapshot, event("agent.reviewing", "task", "node-1")).animation).toBe("review");
  });

  it("increments generations and invalidates an old completion callback immediately", () => {
    const controller = new AvatarController();
    controller.update(actor("executor", "agent"));
    const old = controller.transition();
    controller.update(actor("executor", "agent", "blocked"));
    const current = controller.transition();
    expect(current.generation).toBe(old.generation + 1);
    expect(old.complete()).toBe(false);
    expect(current.complete()).toBe(true);
  });

  it("uses deterministic fallbacks and keeps reduced motion static without changing semantics", () => {
    const state = new AvatarController({ reducedMotion: true, animations: { celebrate: false, idle: true } })
      .update(actor("integrator", "agent", "delivered"));
    expect(state).toMatchObject({ animation: "idle", animationRow: AVATAR_ANIMATION_ROWS.idle, degraded: true, reducedMotion: true });
  });
});

function actor(duty: WorldActorModel["duty"], id: string, status: WorldActorModel["status"] = "idle"): WorldActorModel {
  return { id, agentId: id, runtimeId: "runtime", name: id, duty, zone: "planning_observatory", status, nodeId: "node-1", runId: "run-1", slot: 0 };
}

function event(kind: VisualEvent["kind"], type: VisualEvent["target"]["type"], id: string): VisualEvent {
  return { key: `${kind}:${id}`, activityId: `${kind}:${id}`, sequence: 1, kind, target: { type, id }, priority: "normal" };
}
