import { describe, expect, it } from "vitest";
import { buildAgentRuntimeDiagnostics, runtimeCapabilities } from "./derive-diagnostics";
import type { Agent, AgentRuntime } from "../types";

const agent = (overrides: Partial<Agent> = {}) => ({
  id: "agent-1", workspace_id: "ws", runtime_id: "runtime-1", runtime_bound: true,
  name: "Agent", description: "", instructions: "", avatar_url: null,
  runtime_mode: "local", runtime_config: {}, custom_args: [], visibility: "workspace",
  permission_mode: "public_to", invocation_targets: [], status: "idle", max_concurrent_tasks: 2,
  model: "model", owner_id: null, skills: [], created_at: "", updated_at: "", archived_at: null,
  archived_by: null, ...overrides,
} as Agent);

const runtime = (overrides: Partial<AgentRuntime> = {}) => ({
  id: "runtime-1", workspace_id: "ws", daemon_id: "d", name: "Runtime", runtime_mode: "local",
  provider: "test", launch_header: "", status: "online", device_info: "", metadata: { capabilities: ["shell"] },
  owner_id: null, visibility: "public", last_seen_at: "2026-08-18T00:00:00Z", created_at: "", updated_at: "", ...overrides,
} as AgentRuntime);

describe("agent/runtime diagnostics", () => {
  it("reports coarse availability from binding, online status, and observed capacity", () => {
    const items = buildAgentRuntimeDiagnostics({
      agents: [agent(), agent({ id: "offline", name: "Offline", runtime_id: "offline-runtime" }), agent({ id: "full", name: "Full", max_concurrent_tasks: 1 }), agent({ id: "unbound", name: "Unbound", runtime_id: "", runtime_bound: false })],
      runtimes: [runtime(), runtime({ id: "offline-runtime", status: "offline" })],
      snapshot: [{ id: "task", agent_id: "full", runtime_id: "runtime-1", issue_id: "", status: "running", priority: 0, dispatched_at: null, started_at: null, completed_at: null, result: null, error: null, created_at: "" }],
      now: Date.now(),
    });
    expect(items.map((item) => item.available)).toEqual([true, false, false, false]);
    expect(items.find((item) => item.agent.id === "full")?.used).toBe(1);
  });

  it("treats missing or malformed daemon capabilities as unknown", () => {
    expect(runtimeCapabilities(runtime({ metadata: {} }))).toBeNull();
    expect(runtimeCapabilities(runtime({ metadata: { capabilities: ["shell", 1] } }))).toBeNull();
    expect(runtimeCapabilities(runtime())).toEqual(["shell"]);
  });
});
