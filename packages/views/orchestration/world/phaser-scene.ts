import type Phaser from "phaser";

import { createAvatarController, type AvatarController, type AvatarFrameAvailability, type AvatarVisualState } from "./avatar-controller";
import { loadWorldMap, type WorldMapAnchor, type WorldMapDefinition } from "./map-loader";
import {
  type WorldActorModel,
  type WorldArtifactModel,
  type WorldModel,
  type WorldSignalModel,
  type WorldZone,
} from "./world-model";
import type { VisualEvent } from "./visual-events";

export interface WorldRendererCallbacks {
  onActorClick?: (actorId: string) => void;
  onZoneClick?: (zoneId: WorldZone) => void;
  onArtifactClick?: (artifactId: string) => void;
  onSignalClick?: (signalId: string) => void;
}
export interface WorldRendererOptions extends WorldRendererCallbacks {
  width?: number; height?: number; mapDefinition?: WorldMapDefinition; fetchMap?: (url: URL) => Promise<unknown>;
  reducedMotion?: boolean; avatarFrames?: AvatarFrameAvailability; phaserRuntime?: typeof Phaser;
  gameFactory?: (config: Phaser.Types.Core.GameConfig, onSceneReady: (scene: WorldSceneController) => void) => Phaser.Game;
}
export interface WorldRendererController { mount(container: HTMLElement): Promise<void>; update(model: WorldModel, events?: readonly VisualEvent[]): void; destroy(): void; }
export interface WorldSceneController { renderModel(model: WorldModel): void; renderEvents(events: readonly VisualEvent[]): void; dispose(): void; }

const DEFAULT_WIDTH = 960;
const DEFAULT_HEIGHT = 600;
const SCENE_KEY = "liexiu-world-debug";
const ATLAS_KEY = "liexiu-agent-role-atlas-v1";
const ATLAS_URL = new URL("../agent-role-atlas-v1.png", import.meta.url);
const MAP_URL = new URL("./maps/mission-world-v1.tmj", import.meta.url);
const ATLAS_FRAME_WIDTH = 256;
const ATLAS_FRAME_HEIGHT = 256;

export function createWorldRenderer(options: WorldRendererOptions = {}): WorldRendererController {
  let game: Phaser.Game | undefined;
  let scene: WorldSceneController | undefined;
  let pendingModel: WorldModel | undefined;
  let pendingEvents: readonly VisualEvent[] = [];
  let mounted = false;
  let destroyed = false;
  const renderLatest = (): void => { if (scene) { if (pendingModel) scene.renderModel(pendingModel); scene.renderEvents(pendingEvents); } };
  const onSceneReady = (readyScene: WorldSceneController): void => { if (!destroyed) { scene = readyScene; renderLatest(); } };

  return {
    async mount(container: HTMLElement): Promise<void> {
      if (destroyed || mounted) return;
      const map = options.mapDefinition ?? await fetchMapDefinition(options.fetchMap);
      if (destroyed) return;
      const phaserModule = options.phaserRuntime ? undefined : await import("phaser");
      if (destroyed) return;
      const PhaserRuntime = options.phaserRuntime ?? ((phaserModule && "default" in phaserModule ? phaserModule.default : phaserModule) as typeof Phaser);
      const SceneClass = createWorldScene(PhaserRuntime, options, map, onSceneReady);
      const config: Phaser.Types.Core.GameConfig = {
        type: PhaserRuntime.CANVAS, width: options.width ?? map.pixelWidth ?? DEFAULT_WIDTH, height: options.height ?? map.pixelHeight ?? DEFAULT_HEIGHT,
        parent: container, backgroundColor: "#101827", audio: { noAudio: true }, scene: SceneClass,
      };
      game = options.gameFactory ? options.gameFactory(config, onSceneReady) : new PhaserRuntime.Game(config);
      mounted = true;
    },
    update(model: WorldModel, events: readonly VisualEvent[] = []): void {
      if (destroyed) return;
      pendingModel = model; pendingEvents = events;
      if (mounted && scene) { scene.renderModel(model); scene.renderEvents(events); }
    },
    destroy(): void {
      if (destroyed) return;
      destroyed = true; pendingModel = undefined; pendingEvents = [];
      scene?.dispose(); game?.destroy(true, true); scene = undefined; game = undefined; mounted = false;
    },
  };
}

export const createPhaserWorldController = createWorldRenderer;

async function fetchMapDefinition(fetchMap?: (url: URL) => Promise<unknown>): Promise<WorldMapDefinition> {
  if (fetchMap) return loadWorldMap(await fetchMap(MAP_URL));
  const response = await fetch(MAP_URL);
  if (!response.ok) throw new Error(`Unable to load world map: ${response.status}`);
  return loadWorldMap(await response.json() as unknown);
}

function createWorldScene(PhaserRuntime: typeof Phaser, options: WorldRendererOptions, map: WorldMapDefinition, onReady: (scene: WorldSceneController) => void) {
  return class WorldScene extends PhaserRuntime.Scene {
    private readonly actorObjects = new Map<string, ActorVisual>();
    private readonly artifactObjects = new Map<string, DomainVisual>();
    private readonly signalObjects = new Map<string, DomainVisual>();
    private readonly zoneObjects = new Map<WorldZone, Phaser.GameObjects.Rectangle>();
    private readonly seenEventKeys = new Set<string>();
    private eventText?: Phaser.GameObjects.Text;
    private disposed = false;
    private atlasAvailable = false;
    constructor() { super({ key: SCENE_KEY }); }

    preload(): void {
      this.load.spritesheet(ATLAS_KEY, ATLAS_URL.toString(), { frameWidth: ATLAS_FRAME_WIDTH, frameHeight: ATLAS_FRAME_HEIGHT });
      this.load.once("loaderror", () => { this.atlasAvailable = false; });
    }

    create(): void {
      this.atlasAvailable = this.textures.exists(ATLAS_KEY);
      for (const zone of map.zones) {
        const rectangle = this.add.rectangle(zone.bounds.x + zone.bounds.width / 2, zone.bounds.y + zone.bounds.height / 2, zone.bounds.width, zone.bounds.height, zoneColor(zone.id), 1)
          .setStrokeStyle(2, 0x52627a).setInteractive({ useHandCursor: true });
        rectangle.setData("domainId", zone.id); rectangle.on("pointerdown", () => options.onZoneClick?.(zone.id)); this.zoneObjects.set(zone.id, rectangle);
        this.add.text(zone.labelAnchor.x, zone.labelAnchor.y, zone.id, { color: "#d7e3f4", fontFamily: "monospace", fontSize: "14px" }).setOrigin(0.5, 0);
        drawMapDebugOverlay(this, zone);
      }
      this.eventText = this.add.text(20, map.pixelHeight - 28, "No visual events", { color: "#aab9ce", fontFamily: "monospace", fontSize: "13px" });
      this.events.once("shutdown", this.dispose, this); onReady(this);
    }

    renderModel(model: WorldModel): void {
      if (this.disposed || !this.add) return;
      const actorsById = new Map(model.actors.map((actor) => [actor.id, actor]));
      for (const [actorId, visual] of this.actorObjects) if (!actorsById.has(actorId)) { visual.tween?.stop(); visual.root.destroy(); this.actorObjects.delete(actorId); }
      for (const actor of model.actors) { const existing = this.actorObjects.get(actor.id); if (existing) this.updateActor(existing, actor); else this.actorObjects.set(actor.id, this.createActor(actor)); }
      this.syncArtifacts(model.artifacts);
      this.syncSignals(model.signals);
    }

    renderEvents(events: readonly VisualEvent[]): void {
      if (this.disposed || !this.eventText) return;
      const fresh = events.filter((event) => !this.seenEventKeys.has(event.key)); fresh.forEach((event) => this.seenEventKeys.add(event.key));
      for (const event of fresh) {
        for (const visual of this.actorObjects.values()) {
          if (!eventAppliesToActor(event, visual.actor)) continue;
          applyAvatarState(visual, visual.avatar.update(visual.actor, event));
        }
      }
      const latest = fresh[fresh.length - 1]; if (latest) this.eventText.setText(`${latest.kind} → ${latest.target.type}:${latest.target.id}`);
    }

    dispose(): void {
      if (this.disposed) return;
      this.disposed = true; this.events.off("shutdown", this.dispose, this);
      this.actorObjects.forEach((visual) => { visual.tween?.stop(); visual.root.destroy(); }); this.actorObjects.clear(); this.zoneObjects.clear(); this.seenEventKeys.clear(); this.eventText = undefined;
      this.artifactObjects.forEach((visual) => visual.root.destroy()); this.artifactObjects.clear();
      this.signalObjects.forEach((visual) => visual.root.destroy()); this.signalObjects.clear();
    }

    private createActor(actor: WorldActorModel): ActorVisual {
      const avatar = createAvatarController({ reducedMotion: options.reducedMotion, animations: options.avatarFrames?.animations });
      const state = avatar.update(actor); const spawn = anchorFor(map, actor, "spawn");
      const root = this.add.container(spawn.point.x, spawn.point.y).setSize(44, 52).setInteractive({ useHandCursor: true });
      const body = this.add.circle(0, 0, 16, actorColor(actor.status));
      const label = this.add.text(0, 24, actor.name, { color: "#f4f7fb", fontFamily: "monospace", fontSize: "12px", align: "center" }).setOrigin(0.5, 0);
      const sprite = this.atlasAvailable ? this.add.sprite(0, 0, ATLAS_KEY) : undefined;
      if (sprite) { sprite.setDisplaySize(32, 32); sprite.setFrame(frameIndex(state)); body.setVisible(false); }
      root.add([sprite ?? body, label]); root.setData("domainId", actor.id); root.on("pointerdown", () => options.onActorClick?.(actor.id));
      const visual: ActorVisual = { root, body, label, sprite, avatar, actor, targetKey: "" }; this.moveActor(visual, actor, true); return visual;
    }

    private updateActor(visual: ActorVisual, actor: WorldActorModel): void {
      visual.actor = actor;
      const state = visual.avatar.update(actor); visual.label.setText(actor.name); visual.body.setFillStyle(actorColor(actor.status)); visual.root.setData("domainId", actor.id);
      applyAvatarState(visual, state); this.moveActor(visual, actor, false);
    }

    private moveActor(visual: ActorVisual, actor: WorldActorModel, entering: boolean): void {
      const target = anchorFor(map, actor, targetAnchorKind(actor)); const targetKey = `${target.point.x}:${target.point.y}`;
      if (!entering && visual.targetKey === targetKey) return;
      visual.targetKey = targetKey; visual.tween?.stop(); const transition = visual.avatar.transition();
      if (options.reducedMotion) { visual.root.setPosition(target.point.x, target.point.y); return; }
      visual.tween = this.tweens.add({ targets: visual.root, x: target.point.x, y: target.point.y, duration: entering ? 180 : 220, ease: "Sine.easeOut", onComplete: () => { if (transition.complete()) visual.tween = undefined; } });
    }

    private syncArtifacts(artifacts: readonly WorldArtifactModel[]): void {
      const currentIds = new Set(artifacts.map((artifact) => artifact.id));
      for (const [id, visual] of this.artifactObjects) if (!currentIds.has(id)) { visual.root.destroy(); this.artifactObjects.delete(id); }
      for (const artifact of artifacts) {
        const anchor = resolveWorldEntityAnchor(map, artifact.zone, artifact.slot, "work");
        const existing = this.artifactObjects.get(artifact.id);
        if (existing) {
          existing.root.setPosition(anchor.point.x + 22, anchor.point.y - 24);
          existing.root.setData("domainId", artifact.id);
          continue;
        }
        const root = this.add.container(anchor.point.x + 22, anchor.point.y - 24).setSize(30, 30).setInteractive({ useHandCursor: true });
        const marker = this.add.rectangle(0, 0, 17, 17, artifactColor(artifact.status)).setRotation(Math.PI / 4).setStrokeStyle(2, 0xffedb3);
        const label = this.add.text(0, 18, `A${artifact.version}`, { color: "#fff1bd", fontFamily: "monospace", fontSize: "10px" }).setOrigin(0.5, 0);
        root.add([marker, label]); root.setData("domainId", artifact.id); root.on("pointerdown", () => options.onArtifactClick?.(artifact.id));
        this.artifactObjects.set(artifact.id, { root });
      }
    }

    private syncSignals(signals: readonly WorldSignalModel[]): void {
      const currentIds = new Set(signals.map((signal) => signal.id));
      for (const [id, visual] of this.signalObjects) if (!currentIds.has(id)) { visual.root.destroy(); this.signalObjects.delete(id); }
      for (const signal of signals) {
        const actorRoot = signal.actorId ? this.actorObjects.get(signal.actorId)?.root : undefined;
        const anchor = resolveWorldEntityAnchor(map, signal.zone, signal.slot, "spawn");
        const x = actorRoot?.x ?? anchor.point.x;
        const y = (actorRoot?.y ?? anchor.point.y) - 38;
        const existing = this.signalObjects.get(signal.id);
        if (existing) {
          existing.root.setPosition(x, y);
          existing.root.setData("domainId", signal.id);
          continue;
        }
        const root = this.add.container(x, y).setSize(54, 24).setInteractive({ useHandCursor: true });
        const bubble = this.add.rectangle(0, 0, 50, 20, signalColor(signal.severity), 0.95).setStrokeStyle(1, 0xf4f7fb);
        const label = this.add.text(0, 0, signalLabel(signal.kind), { color: "#ffffff", fontFamily: "monospace", fontSize: "9px" }).setOrigin(0.5);
        root.add([bubble, label]); root.setData("domainId", signal.id); root.on("pointerdown", () => options.onSignalClick?.(signal.id));
        this.signalObjects.set(signal.id, { root });
      }
    }
  };
}

interface DomainVisual { root: Phaser.GameObjects.Container; }
interface ActorVisual extends DomainVisual { body: Phaser.GameObjects.Arc; label: Phaser.GameObjects.Text; sprite?: Phaser.GameObjects.Sprite; avatar: AvatarController; actor: WorldActorModel; targetKey: string; tween?: Phaser.Tweens.Tween; }

export function resolveWorldActorAnchor(map: WorldMapDefinition, actor: WorldActorModel, kind: "spawn" | "work"): WorldMapAnchor {
  return resolveWorldEntityAnchor(map, actor.zone, actor.slot, kind);
}
export function resolveWorldEntityAnchor(map: WorldMapDefinition, zoneId: WorldZone, slot: number, kind: "spawn" | "work"): WorldMapAnchor {
  const zone = map.zones.find((candidate) => candidate.id === zoneId); if (!zone) throw new Error(`Missing map zone: ${zoneId}`);
  const anchors = zone.anchors.filter((anchor) => anchor.kind === kind); if (anchors.length === 0) throw new Error(`Missing ${kind} anchor: ${zoneId}`);
  return anchors[Math.max(0, slot) % anchors.length]!;
}
function anchorFor(map: WorldMapDefinition, actor: WorldActorModel, kind: "spawn" | "work"): WorldMapAnchor { return resolveWorldActorAnchor(map, actor, kind); }
function targetAnchorKind(actor: WorldActorModel): "spawn" | "work" { return actor.status === "blocked" || actor.status === "offline" ? "spawn" : "work"; }
function frameIndex(state: AvatarVisualState): number { return state.animationRow * 4 + state.frameColumn; }
function applyAvatarState(visual: ActorVisual, state: AvatarVisualState): void { if (visual.sprite) { visual.sprite.setFrame(frameIndex(state)); visual.body.setVisible(false); } }
function drawMapDebugOverlay(scene: Phaser.Scene, zone: WorldMapDefinition["zones"][number]): void {
  const graphics = scene.add.graphics(); graphics.lineStyle(1, 0x8da2bf, 0.5);
  for (const anchor of zone.anchors) { graphics.strokeCircle(anchor.point.x, anchor.point.y, 5); graphics.fillStyle(anchor.kind === "spawn" ? 0x69a8ff : 0xffd166, 0.65); graphics.fillCircle(anchor.point.x, anchor.point.y, 2); }
}
function zoneColor(zone: WorldZone): number { return { planning_observatory: 0x243a5e, execution_workshop: 0x3a3424, review_archive: 0x3b2e4b, integration_forge: 0x493325, blocked_corner: 0x4b2930, delivery_plaza: 0x264a42 }[zone]; }
function actorColor(status: WorldActorModel["status"]): number { return { idle: 0x8ba4c7, running: 0x63d6a4, blocked: 0xf08a8a, offline: 0x707887, delivered: 0xf0c76e }[status]; }
function artifactColor(status: WorldArtifactModel["status"]): number { return { produced: 0xf0c76e, approved: 0x63d6a4, changes_requested: 0xf5a65b, rejected: 0xf08a8a, delivered: 0xffe17a }[status]; }
function signalColor(severity: WorldSignalModel["severity"]): number { return { info: 0x3f78a8, attention: 0xa8682d, critical: 0xa83f4d }[severity]; }
function signalLabel(kind: WorldSignalModel["kind"]): string { return { collaboration: "MSG", review: "REVIEW", blocked: "BLOCKED", offline: "OFFLINE", budget: "BUDGET", human_gate: "GATE" }[kind]; }
function eventAppliesToActor(event: VisualEvent, actor: WorldActorModel): boolean {
  if (event.target.type === "task") return event.target.id === actor.nodeId;
  if (event.target.type === "run") return event.target.id === actor.runId;
  return event.target.type === "mission";
}
