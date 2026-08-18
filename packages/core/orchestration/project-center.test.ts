import { describe, expect, it } from "vitest";
import { parseProjectCommandCenterProjection } from "./project-center";

const wire = {
  project: { id: "p1", title: "Project", status: "in_progress", updated_at: "2026-08-19T00:00:00Z" }, generated_at: "2026-08-19T00:00:00Z", truncated: false,
  missions: [{ id: "m1", title: "Mission", status: "running", current_phase: "executing", progress: { completed: 1, total: 2, percent: 50 }, budget: { status: "ok", consumed_tokens: 1, reserved_tokens: 2, consumed_cost_usd_ticks: 3, reserved_cost_usd_ticks: 4, grant_tokens: 5, grant_cost_usd_ticks: 6 }, revision: 7, last_sequence: 8, updated_at: "2026-08-19T00:00:00Z", pending_human_gates: 0, pending_reviews: 1, pending_plan_proposals: 0, offline_agents: 0, active_runs: 1, queued_runs: 0 }],
  attention: [{ id: "a1", mission_id: "m1", kind: "run_failed", severity: "critical", subject_type: "run", subject_id: "r1", mission_revision: 7, actions: [{ kind: "inspect", enabled: true, risk: "low", reason_code: "projection_only", required_permission: "project:read" }] }],
  capacity: { agents: [{ id: "ag1", name: "Agent", status: "online", duties: ["executor"], active_mission_ids: ["m1"], active_runs: 1, queued_runs: 0 }], runtimes: [] },
  totals: { mission_count: 1, active_missions: 1, blocked_missions: 0, completed_missions: 0, attention_count: 1, active_runs: 1, queued_runs: 0, offline_agents: 0, pending_human_gates: 0, pending_reviews: 1, consumed_tokens: 1, reserved_tokens: 2, consumed_cost_usd_ticks: 3, reserved_cost_usd_ticks: 4 },
};

describe("Project Command Center projection", () => {
  it("parses the complete wire shape into camelCase without payload details", () => {
    const result = parseProjectCommandCenterProjection(wire);
    expect(result?.generatedAt).toBe(wire.generated_at);
    expect(result?.missions[0]?.currentPhase).toBe("executing");
    expect(result?.attention[0]?.missionId).toBe("m1");
    expect(result?.attention[0]?.actions[0]?.requiredPermission).toBe("project:read");
    expect(result?.capacity.agents[0]?.activeMissionIds).toEqual(["m1"]);
    expect(result && "payload" in result).toBe(false);
  });
  it.each([
    ["unknown enum", { ...wire, missions: [{ ...wire.missions[0], status: "future" }] }],
    ["unknown field", { ...wire, totals: { ...wire.totals, secret: "nope" } }],
    ["missing required structure", { ...wire, capacity: undefined }],
    ["invalid revision", { ...wire, missions: [{ ...wire.missions[0], revision: -1 }] }],
    ["forbidden payload", { ...wire, attention: [{ ...wire.attention[0], payload: { failure_message: "secret" } }] }],
  ] as const)("fails closed for %s", (_name, value) => expect(parseProjectCommandCenterProjection(value)).toBeNull());

  it("fails closed when the explicit permission is missing or unknown", () => {
    const action = wire.attention[0]!.actions[0]!;
    expect(parseProjectCommandCenterProjection({ ...wire, attention: [{ ...wire.attention[0], actions: [{ ...action, required_permission: undefined }] }] })).toBeNull();
    expect(parseProjectCommandCenterProjection({ ...wire, attention: [{ ...wire.attention[0], actions: [{ ...action, required_permission: "project:write" }] }] })).toBeNull();
  });
});
