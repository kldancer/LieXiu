import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_MISSION_PROJECTION,
  MissionProjectionSchema,
} from "./schemas";

describe("MissionProjectionSchema", () => {
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
          role: "executor",
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
          role: "reviewer",
          runtime_id: "runtime-1",
          runtime_name: "Local runtime",
          runtime_status: "online",
          provider: "fake",
          current_node_ids: ["node-a"],
        }],
        activities: {
          items: [], first_sequence: 1, last_sequence: 18, has_previous: false,
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
