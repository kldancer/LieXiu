import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_MISSION_PROJECTION,
  MissionProjectionSchema,
} from "./schemas";

describe("MissionProjectionSchema", () => {
  it("defaults missing human_gates and parses pending gates when present", () => {
    const base = EMPTY_MISSION_PROJECTION;
    const wire = { mission: { id: "m", progress: {}, limits: {}, budget: {} }, activities: {} };
    expect(MissionProjectionSchema.parse(wire).humanGates).toEqual([]);
    const result = MissionProjectionSchema.parse({
      ...wire,
      human_gates: [{ id: "g", task_node_id: "n", artifact_id: "a", source_run_id: "r", kind: "reviewer_unavailable", status: "pending", reason: "No reviewer", context: { producer: "x" }, revision: 2, created_at: "2026-08-18T00:00:00Z" }],
    });
    expect(result.humanGates).toEqual([{ id: "g", taskNodeId: "n", artifactId: "a", sourceRunId: "r", kind: "reviewer_unavailable", status: "pending", reason: "No reviewer", context: { producer: "x" }, revision: 2, createdAt: "2026-08-18T00:00:00Z" }]);
    expect(base.humanGates).toEqual([]);
  });
  it("maps the API wire shape to one camelCase projection", () => {
    const result = parseWithFallback(
      {
        mission: {
          id: "mission-1",
          title: "Ship the tool",
          status: "running",
          current_phase: "reviewing",
          progress: { completed: 1, total: 3, percent: 33 },
          limits: { max_parallel_runs: 2, max_task_attempts: 2, max_rework_cycles: 1 },
          budget: {
            status: "approval_required",
            gate: "fail_closed",
            max_tokens: 1000,
            max_cost_usd_ticks: 2500,
            consumed_tokens: 120,
            reserved_tokens: 80,
            consumed_cost_usd_ticks: 300,
            reserved_cost_usd_ticks: 100,
            grant_tokens: 0,
            grant_cost_usd_ticks: 0,
          },
          revision: 7,
          last_sequence: 18,
          created_at: "2026-08-17T10:00:00Z",
          updated_at: "2026-08-17T11:00:00Z",
        },
        nodes: [{
          id: "node-a",
          key: "A",
          title: "Implement",
          duty: "executor",
          status: "review",
          dependency_ids: [],
          acceptance_criteria: ["tests pass"],
          artifact_kinds: ["commit"],
          budget_estimate: { tokens: 300, cost_usd_ticks: 700 },
          rework_count: 1,
          revision: 4,
          latest_run: {
            id: "run-2",
            task_node_id: "node-a",
            assignment_id: "assignment-2",
            purpose: "review",
            attempt: 1,
            status: "running",
            dispatch_deadline_at: "2026-08-17T11:01:00Z",
            timeout_seconds: 1800,
            created_at: "2026-08-17T11:00:00Z",
          },
        }],
        team: [{
          agent_id: "agent-1",
          agent_name: "Reviewer",
          duty: "reviewer",
          runtime_id: "runtime-1",
          runtime_name: "Local runtime",
          runtime_status: "online",
          provider: "fake",
          current_node_ids: ["node-a"],
        }],
        activities: {
          items: [], first_sequence: 1, last_sequence: 18, has_previous: false,
        },
        planning: {
          source: "proposal",
          assignments: [{
            id: "assignment-plan",
            duty: "planner",
            agent_id: "agent-plan",
            runtime_id: "runtime-plan",
            status: "fulfilled",
            sequence: 1,
            created_at: "2026-08-17T09:00:00Z",
          }],
          runs: [{
            id: "run-plan",
            assignment_id: "assignment-plan",
            purpose: "plan",
            attempt: 1,
            status: "succeeded",
            input: { objective: "Ship the tool" },
            dispatch_deadline_at: "2026-08-17T09:05:00Z",
            timeout_seconds: 300,
            created_at: "2026-08-17T09:00:00Z",
          }],
          proposals: [{
            id: "proposal-1",
            run_id: "run-plan",
            version: 1,
            uri: "planner://proposal-1",
            content_hash: "sha256:proposal",
            summary: "Plan proposal",
            proposal: { objective: "Ship the tool" },
            decision: "pending",
            created_at: "2026-08-17T09:01:00Z",
          }],
        },
      },
      MissionProjectionSchema,
      EMPTY_MISSION_PROJECTION,
      { endpoint: "test" },
    );

    expect(result.mission.currentPhase).toBe("reviewing");
    expect(result.mission.limits.maxParallelRuns).toBe(2);
    expect(result.mission.budget.status).toBe("approval_required");
    expect(result.mission.budget.maxCostUsdTicks).toBe(2500);
    expect(result.nodes[0]?.latestRun?.assignmentId).toBe("assignment-2");
    expect(result.nodes[0]?.budgetEstimate).toEqual({ tokens: 300, costUsdTicks: 700 });
    expect(result.team[0]?.currentNodeIds).toEqual(["node-a"]);
    expect(result.planning.assignments[0]?.duty).toBe("planner");
    expect(result.planning.source).toBe("proposal");
    expect(result.planning.proposals[0]).toMatchObject({
      id: "proposal-1",
      runId: "run-plan",
      decision: "pending",
    });
  });

  it("returns the stable empty projection when the required identity is malformed", () => {
    const result = parseWithFallback(
      { mission: { title: "missing id" } },
      MissionProjectionSchema,
      EMPTY_MISSION_PROJECTION,
      { endpoint: "test" },
    );

    expect(result).toBe(EMPTY_MISSION_PROJECTION);
    expect(result.nodes).toEqual([]);
    expect(result.team).toEqual([]);
  });
});
