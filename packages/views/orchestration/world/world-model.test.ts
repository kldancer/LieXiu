import { describe, expect, it } from "vitest";
import type { MissionProjection } from "@liexiu/core/orchestration";
import { buildWorldModel, WORLD_ZONES } from "./world-model";

describe("buildWorldModel", () => {
  it("maps all six zones, four duties, and runtime states deterministically", () => {
    const projection = fixture();
    const first = buildWorldModel(projection);
    const second = buildWorldModel({ ...projection, team: [...projection.team].reverse(), nodes: [...projection.nodes].reverse() });

    expect(first).toEqual(second);
    expect(first.zones.map((zone) => zone.id)).toEqual([...WORLD_ZONES]);
    expect(first.actors.map(({ id, duty, zone, status }) => [id, duty, zone, status])).toEqual([
      ["agent-planner:planner:runtime-0", "planner", "planning_observatory", "idle"],
      ["agent-executor:executor:runtime-1", "executor", "execution_workshop", "running"],
      ["agent-reviewer:reviewer:runtime-2", "reviewer", "blocked_corner", "offline"],
      ["agent-integrator:integrator:runtime-3", "integrator", "delivery_plaza", "delivered"],
    ]);
  });

  it("prioritizes blocked and budget states, and fails soft for missing or unknown input", () => {
    const projection = fixture();
    projection.mission.status = "blocked";
    projection.mission.budget.status = "budget_exceeded";
    expect(buildWorldModel(projection).actors.every((actor) => actor.status === "blocked" || actor.status === "delivered")).toBe(true);

    expect(buildWorldModel(undefined)).toEqual({
      missionId: "", revision: 0, missionStatus: "draft",
      zones: WORLD_ZONES.map((id) => ({ id, actorIds: [] })), actors: [], artifacts: [], signals: [],
    });
    expect(buildWorldModel({ mission: { status: "unknown" } } as unknown as MissionProjection).missionStatus).toBe("draft");
  });

  it("projects bounded artifact and warning pointers without Activity payloads", () => {
    const projection = fixture();
    projection.nodes[1]!.latestArtifact = {
      id: "artifact-1", taskNodeId: "node-1", runId: "run-running", kind: "patch", version: 2,
      uri: "artifact://private", summary: "private summary", metadata: { secret: "never-render" }, createdAt: "",
    };
    projection.nodes[1]!.latestVerdict = {
      id: "verdict-1", taskNodeId: "node-1", reviewRunId: "review-run-1", artifactId: "artifact-1",
      decision: "changes_requested", evidence: { private: true }, requestedChanges: [], createdAt: "",
    };
    projection.humanGates = [{
      id: "gate-1", taskNodeId: "node-1", artifactId: "artifact-1", sourceRunId: "run-running",
      kind: "rework_limit_exceeded", status: "pending", reason: "private", context: { secret: true }, revision: 1, createdAt: "",
    }];
    projection.activities.items = [{
      id: "activity-mailbox", taskNodeId: "node-1", runId: "run-running", type: "mailbox.message_sent",
      actorType: "agent", actorId: "agent-executor", subjectType: "mailbox_message", subjectId: "message-1",
      causationId: "cause", correlationId: "mission-1", payloadVersion: 1, payload: { secret: "never-crosses" }, sequence: 7, occurredAt: "",
    }];

    const model = buildWorldModel(projection);
    expect(model.artifacts).toEqual([expect.objectContaining({ id: "artifact-1", runId: "run-running", status: "changes_requested", zone: "review_archive" })]);
    expect(model.signals).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: "review:verdict-1", artifactId: "artifact-1", runId: "review-run-1" }),
      expect.objectContaining({ id: "human-gate:gate-1", gateId: "gate-1", zone: "blocked_corner" }),
      expect.objectContaining({ id: "collaboration:message-1:7", activityId: "activity-mailbox", sequence: 7 }),
      expect.objectContaining({ kind: "offline", actorId: "agent-reviewer:reviewer:runtime-2" }),
    ]));
    expect(JSON.stringify(model)).not.toContain("never-");
    expect(JSON.stringify(model)).not.toContain("private summary");
  });

  it("uses a stable composite actor id for the same agent across duties and ignores unknown duties", () => {
    const projection = fixture();
    projection.team = [
      ...projection.team,
      { ...projection.team[0]!, agentId: "agent-shared", duty: "executor", runtimeId: "runtime-executor", currentNodeIds: [] },
      { ...projection.team[0]!, agentId: "agent-shared", duty: "reviewer", runtimeId: "runtime-reviewer", currentNodeIds: [] },
      { ...projection.team[0]!, agentId: "agent-unknown", duty: "unknown" as never, runtimeId: "runtime-unknown", currentNodeIds: [] },
    ];

    const shared = buildWorldModel(projection).actors.filter((actor) => actor.agentId === "agent-shared");
    expect(shared.map(({ id, agentId, runtimeId, duty }) => [id, agentId, runtimeId, duty])).toEqual([
      ["agent-shared:executor:runtime-executor", "agent-shared", "runtime-executor", "executor"],
      ["agent-shared:reviewer:runtime-reviewer", "agent-shared", "runtime-reviewer", "reviewer"],
    ]);
    expect(buildWorldModel(projection).actors.some((actor) => actor.agentId === "agent-unknown")).toBe(false);
  });
});

function fixture(): MissionProjection {
  const statuses = ["pending", "running", "review", "completed"] as const;
  const duties = ["planner", "executor", "reviewer", "integrator"] as const;
  const nodes = statuses.map((status, index) => ({
    id: `node-${index}`, key: `NODE-${index}`, title: `Node ${index}`, description: "", duty: duties[index]!, status,
    dependencyIds: [], acceptanceCriteria: [], artifactKinds: [], reworkCount: 0, revision: 1,
    latestRun: status === "running" ? { id: "run-running", taskNodeId: `node-${index}`, assignmentId: "assignment", purpose: "run", attempt: 1, status: "running" as const, input: {}, dispatchDeadlineAt: "", timeoutSeconds: 1, createdAt: "" } : undefined,
  }));
  return {
    mission: { id: "mission-1", title: "Mission", description: "", status: "running", currentPhase: "execute", progress: { completed: 1, total: 4, percent: 25 }, limits: { maxParallelRuns: 1, maxTaskAttempts: 1, maxReworkCycles: 1 }, budget: { status: "ok", consumedTokens: 0, reservedTokens: 0, consumedCostUsdTicks: 0, reservedCostUsdTicks: 0, grantTokens: 0, grantCostUsdTicks: 0 }, revision: 3, lastSequence: 0, createdAt: "", updatedAt: "" },
    nodes, team: duties.map((duty, index) => ({ agentId: `agent-${duty}`, agentName: duty, avatarUrl: undefined, duty, runtimeId: `runtime-${index}`, runtimeName: "runtime", runtimeStatus: index === 2 ? "offline" : "online", provider: "test", capabilities: {}, currentNodeIds: [`node-${index}`] })),
    activities: { items: [], firstSequence: 0, lastSequence: 0, hasPrevious: false }, planning: { assignments: [], runs: [], proposals: [] }, rolePolicySnapshots: [], humanGates: [],
  };
}
