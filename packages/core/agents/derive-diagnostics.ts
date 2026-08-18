import { buildPresenceMap } from "./derive-presence";
import type { Agent, AgentRuntime, AgentTask } from "../types";

export interface AgentRuntimeDiagnostic {
  agent: Agent;
  runtime: AgentRuntime | null;
  runtimeBound: boolean;
  runtimeOnline: boolean;
  used: number;
  limit: number;
  capabilities: string[] | null;
  permissionMode: Agent["permission_mode"];
  runtimeVisibility: AgentRuntime["visibility"] | "unknown";
  available: boolean;
}

/** Parse only the daemon-owned runtime metadata capability array. */
export function runtimeCapabilities(runtime: AgentRuntime | null): string[] | null {
  const value = runtime?.metadata?.capabilities;
  if (!Array.isArray(value) || !value.every((item): item is string => typeof item === "string")) {
    return null;
  }
  return value;
}

/**
 * Read-only, coarse diagnostics. Availability intentionally does not inspect
 * RolePolicy or profile fields: it only reports current observed reachability
 * and load, and must not be used as a routing/eligibility decision.
 */
export function buildAgentRuntimeDiagnostics(args: {
  agents: readonly Agent[];
  runtimes: readonly AgentRuntime[];
  snapshot: readonly AgentTask[];
  now: number;
}): AgentRuntimeDiagnostic[] {
  const presence = buildPresenceMap(args);
  const runtimesById = new Map(args.runtimes.map((runtime) => [runtime.id, runtime]));
  return [...args.agents]
    .sort((left, right) => `${left.name}\u0000${left.id}`.localeCompare(`${right.name}\u0000${right.id}`))
    .map((agent) => {
      const runtime = runtimesById.get(agent.runtime_id) ?? null;
      const detail = presence.get(agent.id);
      const used = (detail?.runningCount ?? 0) + (detail?.queuedCount ?? 0);
      const limit = agent.max_concurrent_tasks;
      const runtimeBound = Boolean(agent.runtime_id) && agent.runtime_bound !== false && runtime !== null;
      const runtimeOnline = runtime?.status === "online";
      return {
        agent,
        runtime,
        runtimeBound,
        runtimeOnline,
        used,
        limit,
        capabilities: runtimeCapabilities(runtime),
        permissionMode: agent.permission_mode,
        runtimeVisibility: runtime?.visibility ?? "unknown",
        available: !agent.archived_at && runtimeBound && runtimeOnline && used < limit,
      };
    });
}
