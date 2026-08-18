import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const projectKeys = {
  all: (wsId: string) => ["projects", wsId] as const,
  list: (wsId: string) => [...projectKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) =>
    [...projectKeys.all(wsId), "detail", id] as const,
  commandCenter: (wsId: string, id: string) =>
    [...projectKeys.all(wsId), "command-center", id] as const,
};

export function projectListOptions(wsId: string) {
  return queryOptions({
    queryKey: projectKeys.list(wsId),
    queryFn: () => api.listProjects(),
    select: (data) => data.projects,
  });
}

export function projectDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: projectKeys.detail(wsId, id),
    queryFn: () => api.getProject(id),
  });
}

export function projectCommandCenterOptions(
  wsId: string,
  id: string,
  active = true,
) {
  return queryOptions({
    queryKey: projectKeys.commandCenter(wsId, id),
    queryFn: () => api.getProjectCommandCenter(id),
    enabled: wsId.length > 0 && id.length > 0 && active,
    refetchOnReconnect: "always",
    refetchOnWindowFocus: true,
    refetchInterval: active ? 5_000 : false,
  });
}
