import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { TaskMessagePayload } from "../types/events";

/**
 * Transcript-only compatibility surface.
 *
 * The task transcript view still imports this module directly. Keep the stable
 * task-message key and pure merge helpers here until that view moves to its own
 * package path.
 */
export const chatKeys = {
  taskMessagesAll: () => ["task-messages"] as const,
  taskMessages: (taskId: string) => [...chatKeys.taskMessagesAll(), taskId] as const,
};

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isTaskMessageTaskId(taskId: string | null | undefined): taskId is string {
  return typeof taskId === "string" && UUID_PATTERN.test(taskId);
}

export function taskMessagesOptions(taskId: string) {
  return queryOptions({
    queryKey: chatKeys.taskMessages(taskId),
    queryFn: () => api.listTaskMessages(taskId),
    enabled: isTaskMessageTaskId(taskId),
    staleTime: Infinity,
  });
}

export function mergeTaskMessagesBySeq(
  existing: readonly TaskMessagePayload[],
  incoming: readonly TaskMessagePayload[],
): TaskMessagePayload[] {
  if (incoming.length === 0) return existing as TaskMessagePayload[];
  const knownSeqs = new Set(existing.map((message) => message.seq));
  const fresh = incoming.filter((message) => !knownSeqs.has(message.seq));
  if (fresh.length === 0) return existing as TaskMessagePayload[];
  return [...existing, ...fresh].sort((a, b) => a.seq - b.seq);
}
