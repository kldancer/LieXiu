"use client";

import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Agent, MemberWithUser } from "../types";
import { useWorkspaceId } from "../hooks";
import { memberListOptions, agentListOptions } from "./queries";
import { resolvePublicFileUrl } from "./avatar-url";

// Stable empties for the still-loading directory queries. A fresh `= []`
// default allocates a new array on every render while `data` is undefined,
// which makes `useMemo(..., [members, agents])` recompute
// `getActorName` on every render during cold load. Consumers that list
// `getActorName` in their own memo deps (BoardView's `groups`, SwimLaneView's
// `laneGroups`) then churn a fresh value each render, and the board/list
// column resync `useEffect(setColumns, [groups])` re-fires without end — an
// infinite re-render that react-virtuoso turns into "Maximum update depth
// exceeded" on the Issues route (MUL-4985). Sharing one reference keeps the
// loading snapshot referentially stable.
const EMPTY_MEMBERS: MemberWithUser[] = [];
const EMPTY_AGENTS: Agent[] = [];

/**
 * Pure actor-name resolution over explicit directory snapshots. Async flows
 * (e.g. CSV export) must resolve names from directories they have awaited
 * themselves — a hook-bound getActorName closes over whatever the queries
 * held at render time, silently naming everyone "Unknown*" while the
 * directories are still loading.
 */
export function buildActorNameResolver(directories: {
  members: readonly { user_id: string; name: string }[];
  agents: readonly { id: string; name: string }[];
}) {
  const memberNames = new Map(directories.members.map((m) => [m.user_id, m.name]));
  const agentNames = new Map(directories.agents.map((a) => [a.id, a.name]));
  return (type: string, id: string) => {
    if (type === "member") return memberNames.get(id) ?? "Unknown";
    if (type === "agent") return agentNames.get(id) ?? "Unknown Agent";
    if (type === "system") return "LieXiu";
    return "System";
  };
}

export function useActorName() {
  const wsId = useWorkspaceId();
  const { data: members = EMPTY_MEMBERS } = useQuery(memberListOptions(wsId));
  const { data: agents = EMPTY_AGENTS } = useQuery(agentListOptions(wsId));

  const getMemberName = useCallback((userId: string) => {
    const m = members.find((m) => m.user_id === userId);
    return m?.name ?? "Unknown";
  }, [members]);

  const getAgentName = useCallback((agentId: string) => {
    const a = agents.find((a) => a.id === agentId);
    return a?.name ?? "Unknown Agent";
  }, [agents]);

  const getActorName = useMemo(
    () => buildActorNameResolver({ members, agents }),
    [agents, members],
  );

  const getActorInitials = useCallback((type: string, id: string) => {
    const name = getActorName(type, id);
    return name
      .split(" ")
      .map((w) => w[0])
      .join("")
      .toUpperCase()
      .slice(0, 2);
  }, [getActorName]);

  const getActorAvatarUrl = useCallback((type: string, id: string): string | null => {
    if (type === "member") return resolvePublicFileUrl(members.find((m) => m.user_id === id)?.avatar_url);
    if (type === "agent") return resolvePublicFileUrl(agents.find((a) => a.id === id)?.avatar_url);
    return null;
  }, [agents, members]);

  return useMemo(
    () => ({
      getMemberName,
      getAgentName,
      getActorName,
      getActorInitials,
      getActorAvatarUrl,
    }),
    [
      getActorAvatarUrl,
      getActorInitials,
      getActorName,
      getAgentName,
      getMemberName,
    ],
  );
}
