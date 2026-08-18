import { describe, expect, it, vi } from "vitest";

import { createWorldRenderer, resolveWorldActorAnchor, resolveWorldEntityAnchor, type WorldRendererOptions, type WorldSceneController } from "./phaser-scene";
import { createAvatarController } from "./avatar-controller";
import type { WorldMapDefinition } from "./map-loader";
import { WORLD_ZONES, type WorldModel } from "./world-model";
import type { VisualEvent } from "./visual-events";

function model(actorIds: string[]): WorldModel {
  const actors = actorIds.map((id, slot) => ({
    id,
    agentId: id,
    runtimeId: `runtime-${id}`,
    name: id,
    duty: "executor" as const,
    zone: "execution_workshop" as const,
    status: "running" as const,
    slot,
  }));
  return {
    missionId: "mission-1",
    revision: actorIds.length,
    missionStatus: "running",
    zones: WORLD_ZONES.map((id) => ({ id, actorIds: actors.filter((actor) => actor.zone === id).map((actor) => actor.id) })),
    actors,
    artifacts: [],
    signals: [],
  };
}

function fakeGame() {
  const game = { scene: { getScene: vi.fn() }, destroy: vi.fn() };
  return game;
}

const container = {} as HTMLElement;
const phaserRuntime = {
  CANVAS: 1,
  Scene: class { constructor(_config: unknown) {} },
} as never;

function mapDefinition(): WorldMapDefinition {
  return {
    width: 48,
    height: 24,
    tileWidth: 16,
    tileHeight: 16,
    pixelWidth: 768,
    pixelHeight: 384,
    zones: WORLD_ZONES.map((id, index) => ({
      id,
      bounds: { x: (index % 3) * 256, y: Math.floor(index / 3) * 192, width: 256, height: 192 },
      labelAnchor: { x: (index % 3) * 256 + 128, y: Math.floor(index / 3) * 192 + 24 },
      anchors: [0, 1, 2, 3].map((order) => ({
        id: `${id}-${order}`,
        zoneId: id,
        kind: order === 0 || order === 3 ? "spawn" as const : "work" as const,
        order,
        point: { x: (index % 3) * 256 + 48 + order * 32, y: Math.floor(index / 3) * 192 + 80 },
      })),
    })),
  };
}

describe("Phaser world controller lifecycle", () => {
  it("queues updates before mount and destroys the game/canvas safely", async () => {
    const game = fakeGame();
    const gameFactory = vi.fn(() => game) as unknown as NonNullable<WorldRendererOptions["gameFactory"]>;
    const controller = createWorldRenderer({ gameFactory, phaserRuntime, mapDefinition: mapDefinition() });
    controller.update(model(["actor-1"]));
    await controller.mount(container);
    expect(gameFactory).toHaveBeenCalledOnce();
    controller.destroy();
    controller.destroy();
    expect(game.destroy).toHaveBeenCalledWith(true, true);
    controller.update(model(["actor-2"]));
    expect(gameFactory).toHaveBeenCalledOnce();
  });

  it("does not mount twice and accepts the newest model immediately", async () => {
    const game = fakeGame();
    const scene = { renderModel: vi.fn(), renderEvents: vi.fn() };
    game.scene.getScene.mockReturnValue(scene);
    let ready: ((readyScene: WorldSceneController) => void) | undefined;
    const gameFactory = vi.fn((_config: unknown, onReady: (readyScene: WorldSceneController) => void) => {
      ready = onReady;
      return game;
    }) as unknown as NonNullable<WorldRendererOptions["gameFactory"]>;
    const controller = createWorldRenderer({ gameFactory, phaserRuntime, mapDefinition: mapDefinition() });
    await controller.mount(container);
    controller.update(model(["actor-1"]));
    const events: VisualEvent[] = [{
      key: "activity:1",
      activityId: "1",
      sequence: 1,
      kind: "agent.started",
      target: { type: "task", id: "task-1" },
      priority: "normal",
    }];
    controller.update(model(["actor-2"]), events);
    expect(scene.renderModel).not.toHaveBeenCalled();
    ready?.(scene as unknown as WorldSceneController);
    await controller.mount(container);
    expect(scene.renderModel).toHaveBeenLastCalledWith(model(["actor-2"]));
    expect(scene.renderEvents).toHaveBeenLastCalledWith(events);
    expect(scene.renderModel).toHaveBeenCalledOnce();
  });

  it("validates an injected map and fails before creating a game when fetching fails", async () => {
    const gameFactory = vi.fn(() => fakeGame()) as unknown as NonNullable<WorldRendererOptions["gameFactory"]>;
    const fetchMap = vi.fn(async () => ({ invalid: true }));
    const controller = createWorldRenderer({ gameFactory, phaserRuntime, fetchMap });
    await expect(controller.mount(container)).rejects.toThrow("Invalid map.width");
    expect(fetchMap).toHaveBeenCalledOnce();
    expect(gameFactory).not.toHaveBeenCalled();
  });

  it("selects anchors deterministically and rejects stale transition completion", () => {
    const map = mapDefinition();
    const actor = model(["actor-1"]).actors[0]!;
    expect(resolveWorldActorAnchor(map, actor, "spawn").id).toBe("execution_workshop-0");
    expect(resolveWorldActorAnchor(map, { ...actor, slot: 4 }, "spawn").id).toBe("execution_workshop-0");
    expect(resolveWorldActorAnchor(map, actor, "work").id).toBe("execution_workshop-1");
    expect(resolveWorldEntityAnchor(map, "review_archive", 3, "work").id).toBe("review_archive-2");

    const avatar = createAvatarController();
    avatar.update(actor);
    const oldTransition = avatar.transition();
    avatar.update({ ...actor, zone: "delivery_plaza" });
    expect(oldTransition.complete()).toBe(false);
    expect(avatar.transition().complete()).toBe(true);
  });
});
