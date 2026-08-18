import type { WorldActorModel, WorldActorStatus, WorldDuty } from "./world-model";
import type { VisualEvent, VisualEventKind } from "./visual-events";

export const AVATAR_ANIMATIONS = ["idle", "walk", "work", "review", "blocked", "celebrate"] as const;
export type AvatarAnimation = (typeof AVATAR_ANIMATIONS)[number];

/** The atlas columns are part of the asset contract, not an agent hash. */
export const DUTY_ATLAS_COLUMNS: Readonly<Record<WorldDuty, number>> = {
  planner: 0,
  executor: 1,
  reviewer: 2,
  integrator: 3,
};

export const AVATAR_ANIMATION_ROWS: Readonly<Record<AvatarAnimation, number>> = {
  idle: 0,
  walk: 1,
  work: 2,
  review: 3,
  blocked: 4,
  celebrate: 5,
};

export interface AvatarFrameAvailability {
  /** Missing entries are treated as available. This keeps the default atlas contract small. */
  animations?: Partial<Record<AvatarAnimation, boolean>>;
}

export interface AvatarControllerOptions extends AvatarFrameAvailability {
  reducedMotion?: boolean;
}

export interface AvatarVisualState {
  actorId: string;
  duty: WorldDuty;
  frameColumn: number;
  animation: AvatarAnimation;
  animationRow: number;
  reducedMotion: boolean;
  /** True when the requested semantic had to use another available row. */
  degraded: boolean;
  transitionGeneration: number;
}

export interface AvatarTransitionHandle {
  generation: number;
  isCurrent(): boolean;
  /** Renderer completion hooks must check this before doing any work. */
  complete(): boolean;
}

const STATUS_ANIMATION: Readonly<Record<WorldActorStatus, AvatarAnimation>> = {
  idle: "idle",
  running: "work",
  blocked: "blocked",
  offline: "blocked",
  delivered: "celebrate",
};

const CUE_ANIMATION: Partial<Record<VisualEventKind, AvatarAnimation>> = {
  "agent.assigned": "walk",
  "agent.started": "work",
  "agent.reviewing": "review",
  "agent.blocked": "blocked",
  "agent.completed": "celebrate",
  "run.started": "work",
  "run.succeeded": "celebrate",
  "run.failed": "blocked",
  "review.approved": "celebrate",
  "review.changes_requested": "review",
  "review.rejected": "blocked",
};

const FALLBACKS: Readonly<Record<AvatarAnimation, readonly AvatarAnimation[]>> = {
  idle: ["idle"],
  walk: ["walk", "idle"],
  work: ["work", "idle"],
  review: ["review", "work", "idle"],
  blocked: ["blocked", "idle"],
  celebrate: ["celebrate", "idle"],
};

/**
 * Renderer-neutral avatar projection. It owns no timers, coordinates, or
 * business state; a renderer may use the generation as a stale-callback guard.
 */
export class AvatarController {
  private readonly options: AvatarControllerOptions;
  private generation = 0;
  private snapshot?: WorldActorModel;
  private cue?: VisualEvent;

  constructor(options: AvatarControllerOptions = {}) {
    this.options = options;
  }

  update(actor: WorldActorModel, cue?: VisualEvent): AvatarVisualState {
    this.generation += 1;
    this.snapshot = actor;
    this.cue = cue && cueAppliesToActor(cue, actor) ? cue : undefined;
    return this.state();
  }

  getState(): AvatarVisualState | undefined {
    return this.snapshot ? this.state() : undefined;
  }

  transition(): AvatarTransitionHandle {
    const generation = this.generation;
    return {
      generation,
      isCurrent: () => generation === this.generation,
      complete: () => generation === this.generation,
    };
  }

  isCurrentTransition(generation: number): boolean {
    return generation === this.generation;
  }

  private state(): AvatarVisualState {
    const actor = this.snapshot!;
    const requested = (this.cue && CUE_ANIMATION[this.cue.kind]) ?? STATUS_ANIMATION[actor.status];
    const animation = availableAnimation(requested, this.options.animations);
    return {
      actorId: actor.id,
      duty: actor.duty,
      frameColumn: DUTY_ATLAS_COLUMNS[actor.duty],
      animation,
      animationRow: AVATAR_ANIMATION_ROWS[animation],
      reducedMotion: this.options.reducedMotion === true,
      degraded: animation !== requested,
      transitionGeneration: this.generation,
    };
  }
}

export function createAvatarController(options: AvatarControllerOptions = {}): AvatarController {
  return new AvatarController(options);
}

function availableAnimation(requested: AvatarAnimation, availability: Partial<Record<AvatarAnimation, boolean>> | undefined): AvatarAnimation {
  for (const candidate of FALLBACKS[requested]) {
    if (availability?.[candidate] !== false) return candidate;
  }
  return "idle";
}

function cueAppliesToActor(cue: VisualEvent, actor: WorldActorModel): boolean {
  if (cue.target.type === "task") return cue.target.id === actor.nodeId;
  if (cue.target.type === "run") return cue.target.id === actor.runId;
  return cue.target.type === "mission";
}
