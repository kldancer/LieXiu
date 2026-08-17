"use client";

import { useEffect } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { StoreApi, UseBoundStore } from "zustand";
import type { AuthState } from "../auth/store";
import type { WSClient } from "../api/ws-client";
import type { WSMessage, TaskMessagePayload } from "../types/events";
import { getCurrentWsId } from "../platform/workspace-storage";
import { chatKeys, mergeTaskMessagesBySeq } from "../chat/queries";
import { issueKeys } from "../issues/queries";
import { projectKeys } from "../projects/queries";
import { workspaceKeys } from "../workspace/queries";
import { runtimeKeys } from "../runtimes/queries";
import { githubKeys } from "../github/queries";
import { agentTaskSnapshotKeys, agentTasksKeys, workspaceWorkingAgentsKeys } from "../agents/queries";

export interface RealtimeSyncStores {
  authStore: UseBoundStore<StoreApi<AuthState>>;
}

function isTaskMessagePayload(value: unknown): value is TaskMessagePayload {
  if (!value || typeof value !== "object") return false;
  const payload = value as Partial<TaskMessagePayload>;
  return typeof payload.task_id === "string" && typeof payload.seq === "number";
}

/** Keep the transcript cache authoritative for streamed task messages. */
export function applyTaskMessageToCache(qc: QueryClient, payload: TaskMessagePayload) {
  qc.setQueryData<TaskMessagePayload[]>(
    chatKeys.taskMessages(payload.task_id),
    (old) => mergeTaskMessagesBySeq(old ?? [], [payload]),
  );
}

function invalidateWorkspaceQueries(qc: QueryClient, wsId: string) {
  const keys = [
    issueKeys.all(wsId),
    projectKeys.all(wsId),
    workspaceKeys.all(wsId),
    runtimeKeys.all(wsId),
    githubKeys.all(wsId),
    agentTaskSnapshotKeys.list(wsId),
    agentTasksKeys.all(wsId),
    workspaceWorkingAgentsKeys.all(wsId),
  ];
  for (const queryKey of keys) qc.invalidateQueries({ queryKey });
}

function invalidateForEvent(qc: QueryClient, type: string, wsId: string | null) {
  if (type === "task:message") return;
  if (!wsId) {
    if (type.startsWith("workspace:")) qc.invalidateQueries({ queryKey: workspaceKeys.list() });
    return;
  }

  const prefix = type.split(":", 1)[0];
  switch (prefix) {
    case "issue":
    case "comment":
    case "activity":
    case "property":
    case "label":
      qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
      break;
    case "project":
      qc.invalidateQueries({ queryKey: projectKeys.all(wsId) });
      break;
    case "agent":
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      break;
    case "member":
    case "workspace":
      qc.invalidateQueries({ queryKey: workspaceKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.list() });
      break;
    case "daemon":
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      break;
    case "github_installation":
      qc.invalidateQueries({ queryKey: githubKeys.all(wsId) });
      break;
    case "task":
      qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: agentTasksKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceWorkingAgentsKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
      break;
    default:
      invalidateWorkspaceQueries(qc, wsId);
  }
}

function invalidateAfterReconnect(qc: QueryClient) {
  const wsId = getCurrentWsId();
  if (wsId) invalidateWorkspaceQueries(qc, wsId);
  qc.invalidateQueries({ queryKey: chatKeys.taskMessagesAll() });
}

export function useRealtimeSync(
  ws: WSClient | null,
  _stores: RealtimeSyncStores,
  _onToast?: (message: string, type?: "info" | "error") => void,
) {
  const qc = useQueryClient();

  useEffect(() => {
    if (!ws) return;

    const timers = new Map<string, ReturnType<typeof setTimeout>>();
    const unsubAny = ws.onAny((message: WSMessage) => {
      if (message.type === "task:message" && isTaskMessagePayload(message.payload)) {
        applyTaskMessageToCache(qc, message.payload);
        return;
      }

      const prefix = message.type.split(":", 1)[0] ?? message.type;
      const oldTimer = timers.get(prefix);
      if (oldTimer) clearTimeout(oldTimer);
      timers.set(
        prefix,
        setTimeout(() => {
          timers.delete(prefix);
          invalidateForEvent(qc, message.type, getCurrentWsId());
        }, 100),
      );
    });

    const unsubReconnect = ws.onReconnect(() => invalidateAfterReconnect(qc));
    return () => {
      unsubAny();
      unsubReconnect();
      for (const timer of timers.values()) clearTimeout(timer);
    };
  }, [qc, ws]);
}
