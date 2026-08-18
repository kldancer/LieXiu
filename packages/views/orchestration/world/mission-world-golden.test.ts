import { describe, expect, it } from "vitest";
import type {
  ActivityProjection,
  MissionProjection,
  TaskNodeProjection,
  TeamMemberProjection,
} from "@liexiu/core/orchestration";

import { activitiesToVisualEvents } from "./visual-events";
import { buildWorldModel } from "./world-model";

describe("first mission world golden scenario", () => {
  it("projects A/B parallel, review rework, C integration and delivery from canonical facts", () => {
    const parallel = scenario();
    expect(buildWorldModel(parallel).actors.map(({ agentId, zone, status }) => [agentId, zone, status])).toEqual([
      ["planner", "planning_observatory", "idle"],
      ["executor-a", "execution_workshop", "running"],
      ["executor-b", "execution_workshop", "running"],
      ["reviewer", "review_archive", "running"],
      ["integrator", "integration_forge", "idle"],
    ]);

    const rework = scenario({ rework: true });
    const reworkModel = buildWorldModel(rework);
    expect(reworkModel.artifacts).toContainEqual(expect.objectContaining({
      id: "artifact-a", status: "changes_requested", zone: "review_archive",
    }));
    expect(reworkModel.signals).toContainEqual(expect.objectContaining({
      id: "review:verdict-a", runId: "review-run-a", artifactId: "artifact-a",
    }));

    const integrating = scenario({ integrating: true });
    expect(buildWorldModel(integrating).actors.find((actor) => actor.agentId === "integrator")).toEqual(
      expect.objectContaining({ zone: "integration_forge", status: "running", runId: "run-c" }),
    );

    const delivered = scenario({ delivered: true });
    const deliveredModel = buildWorldModel(delivered);
    expect(deliveredModel.missionStatus).toBe("completed");
    expect(deliveredModel.actors.every((actor) => actor.zone === "delivery_plaza" && actor.status === "delivered")).toBe(true);
    expect(deliveredModel.artifacts.every((artifact) => artifact.zone === "delivery_plaza" && artifact.status === "delivered")).toBe(true);
    expect(buildWorldModel({ ...delivered, team: [...delivered.team].reverse(), nodes: [...delivered.nodes].reverse() })).toEqual(deliveredModel);
  });

  it("keeps cancel, offline, blocked and collaboration evidence observable by stable domain ID", () => {
    const projection = scenario({ blocked: true, offline: true });
    projection.mission.status = "cancelled";
    projection.activities.items.push(activity(9, "mission.cancelled", "mission", "mission-1"));
    projection.activities.items.push({
      ...activity(8, "mailbox.message_expired", "mailbox_message", "message-1"),
      taskNodeId: "node-a", runId: "run-a", actorType: "agent", actorId: "executor-a",
      payload: { private: "does-not-cross" },
    });

    const model = buildWorldModel(projection);
    expect(model.missionStatus).toBe("cancelled");
    expect(model.signals).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: "blocked:node-a", kind: "blocked", nodeId: "node-a" }),
      expect.objectContaining({ kind: "offline", actorId: "executor-b:executor:runtime-b" }),
      expect.objectContaining({ id: "collaboration:message-1:8", activityId: "activity-8", sequence: 8 }),
    ]));
    expect(JSON.stringify(model)).not.toContain("does-not-cross");

    const events = activitiesToVisualEvents([...projection.activities.items, projection.activities.items.at(-1)!]);
    expect(events.map(({ sequence, kind, target }) => [sequence, kind, target.type, target.id])).toEqual([
      [8, "mailbox.expired", "mailbox", "message-1"],
      [9, "mission.cancelled", "mission", "mission-1"],
    ]);
  });
});

function scenario(options: { rework?: boolean; integrating?: boolean; delivered?: boolean; blocked?: boolean; offline?: boolean } = {}): MissionProjection {
  const nodes: TaskNodeProjection[] = [
    node("node-a", "A", "executor", options.blocked ? "blocked" : options.rework ? "rework" : options.delivered || options.integrating ? "completed" : "running", "run-a", "executor-a"),
    node("node-b", "B", "executor", options.delivered || options.integrating ? "completed" : "running", "run-b", "executor-b"),
    node("node-review", "REVIEW", "reviewer", options.delivered || options.integrating ? "completed" : "review", "review-run-a", "reviewer"),
    node("node-c", "C", "integrator", options.delivered ? "completed" : options.integrating ? "running" : "pending", options.integrating || options.delivered ? "run-c" : undefined, "integrator"),
  ];
  nodes[0]!.latestArtifact = artifact("artifact-a", "node-a", "run-a");
  nodes[1]!.latestArtifact = artifact("artifact-b", "node-b", "run-b");
  if (options.rework) {
    nodes[0]!.latestVerdict = {
      id: "verdict-a", taskNodeId: "node-a", reviewRunId: "review-run-a", artifactId: "artifact-a",
      decision: "changes_requested", evidence: {}, requestedChanges: [], createdAt: "",
    };
  }
  if (options.integrating || options.delivered) nodes[3]!.latestArtifact = artifact("artifact-c", "node-c", "run-c");
  const team: TeamMemberProjection[] = [
    member("planner", "planner", "runtime-plan", "online", []),
    member("executor-a", "executor", "runtime-a", "online", ["node-a"]),
    member("executor-b", "executor", "runtime-b", options.offline ? "offline" : "online", ["node-b"]),
    member("reviewer", "reviewer", "runtime-review", "online", ["node-review"]),
    member("integrator", "integrator", "runtime-integrate", "online", ["node-c"]),
  ];
  return {
    mission: {
      id: "mission-1", title: "Golden", description: "", status: options.delivered ? "completed" : "running",
      currentPhase: options.delivered ? "delivery" : options.integrating ? "integration" : "execution",
      progress: { completed: options.delivered ? 4 : 0, total: 4, percent: options.delivered ? 100 : 0 },
      limits: { maxParallelRuns: 2, maxTaskAttempts: 2, maxReworkCycles: 1 },
      budget: { status: "ok", consumedTokens: 0, reservedTokens: 0, consumedCostUsdTicks: 0, reservedCostUsdTicks: 0, grantTokens: 0, grantCostUsdTicks: 0 },
      revision: options.delivered ? 4 : 2, lastSequence: 0, createdAt: "", updatedAt: "",
    },
    nodes, team,
    activities: { items: [], firstSequence: 0, lastSequence: 0, hasPrevious: false },
    planning: { assignments: [], runs: [], proposals: [] }, rolePolicySnapshots: [], humanGates: [],
  };
}

function node(id: string, key: string, duty: TaskNodeProjection["duty"], status: TaskNodeProjection["status"], runId: string | undefined, agentId: string): TaskNodeProjection {
  return {
    id, key, title: key, description: "", duty, status, dependencyIds: [], acceptanceCriteria: [], artifactKinds: ["patch"], reworkCount: 0, revision: 1,
    latestRun: runId ? { id: runId, taskNodeId: id, assignmentId: `assignment-${agentId}`, purpose: "work", attempt: 1, status: status === "completed" ? "succeeded" : status === "blocked" ? "failed" : status === "pending" ? "queued" : "running", input: {}, dispatchDeadlineAt: "", timeoutSeconds: 60, createdAt: "" } : undefined,
  };
}

function artifact(id: string, taskNodeId: string, runId: string) {
  return { id, taskNodeId, runId, kind: "patch", version: 1, uri: `artifact://${id}`, summary: id, metadata: {}, createdAt: "" };
}

function member(agentId: string, duty: TeamMemberProjection["duty"], runtimeId: string, runtimeStatus: string, currentNodeIds: string[]): TeamMemberProjection {
  return { agentId, agentName: agentId, duty, runtimeId, runtimeName: runtimeId, runtimeStatus, provider: "fixture", capabilities: {}, currentNodeIds };
}

function activity(sequence: number, type: string, subjectType: string, subjectId: string): ActivityProjection {
  return { id: `activity-${sequence}`, type, actorType: "owner", subjectType, subjectId, causationId: `cause-${sequence}`, correlationId: "mission-1", payloadVersion: 1, payload: {}, sequence, occurredAt: "" };
}
