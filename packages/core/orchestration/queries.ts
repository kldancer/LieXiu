import { mutationOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type {
  ActivityPage,
  ApproveMissionBudgetRequest,
  MissionLifecycleRequest,
  RetryMissionTaskRequest,
  RequestPlanRequest,
  StartMissionRequest,
  EditPlanProposalRequest,
  RejectPlanProposalRequest,
  ResolveHumanGateRequest,
} from "./types";

/**
 * Activity pages are invalidation signals, never a second business-state
 * store. Any observed activity or cursor disagreement requires rebuilding all
 * three visual surfaces from one canonical Mission projection.
 */
export function shouldRefreshMissionSnapshot(
  snapshotSequence: number,
  page: Pick<ActivityPage, "items" | "lastSequence" | "resetRequired">,
) {
  return page.resetRequired || page.items.length > 0 || page.lastSequence !== snapshotSequence;
}

export const missionKeys = {
  all: (wsId: string) => ["missions", wsId] as const,
  detail: (wsId: string, missionId: string) => [...missionKeys.all(wsId), "detail", missionId] as const,
  activities: (wsId: string, missionId: string, afterSequence: number) =>
    [...missionKeys.detail(wsId, missionId), "activities", afterSequence] as const,
  run: (wsId: string, missionId: string, runId: string) => [...missionKeys.detail(wsId, missionId), "run", runId] as const,
};

export function missionProjectionOptions(wsId: string, missionId: string) {
  return queryOptions({
    queryKey: missionKeys.detail(wsId, missionId),
    queryFn: () => api.getMissionProjection(missionId),
    refetchOnReconnect: "always",
    refetchOnWindowFocus: true,
    refetchInterval: (query) => {
      const status = query.state.data?.mission.status;
      return status === "completed" || status === "failed" || status === "cancelled" ? false : 3_000;
    },
  });
}

export function missionActivitiesOptions(
  wsId: string,
  missionId: string,
  afterSequence: number,
  active: boolean,
) {
  return queryOptions({
    queryKey: missionKeys.activities(wsId, missionId, afterSequence),
    queryFn: () => api.listMissionActivities(missionId, afterSequence, 100),
    enabled: wsId.length > 0 && missionId.length > 0 && active,
    refetchInterval: active ? 1_500 : false,
    refetchOnReconnect: "always",
    refetchOnWindowFocus: true,
  });
}

export function missionRunDetailOptions(wsId: string, missionId: string, runId: string) {
  return queryOptions({
    queryKey: missionKeys.run(wsId, missionId, runId),
    queryFn: () => api.getMissionRunDetail(missionId, runId),
    enabled: runId.length > 0,
  });
}

export function approveMissionBudgetMutationOptions(missionId: string) {
  return mutationOptions({
    mutationFn: (request: ApproveMissionBudgetRequest) => api.approveMissionBudget(missionId, request),
  });
}

export function startMissionMutationOptions(missionId: string) {
  return mutationOptions({
    mutationFn: (request: StartMissionRequest) => api.startMission(missionId, request),
  });
}

export const roleProfileKeys = {
  all: (wsId: string) => ["role-profiles", wsId] as const,
};

export function roleProfilesOptions(wsId: string) {
  return queryOptions({
    queryKey: roleProfileKeys.all(wsId),
    queryFn: () => api.listRoleProfiles(wsId),
    enabled: wsId.length > 0,
  });
}

export function cancelMissionMutationOptions(missionId: string) {
  return mutationOptions({
    mutationFn: (request: MissionLifecycleRequest) => api.cancelMission(missionId, request),
  });
}

export function retryMissionTaskMutationOptions(missionId: string) {
  return mutationOptions({
    mutationFn: (request: RetryMissionTaskRequest) => api.retryMissionTask(missionId, request),
  });
}

export function resolveHumanGateMutationOptions(missionId: string, gateId: string) {
  return mutationOptions({
    mutationFn: (request: ResolveHumanGateRequest) => api.resolveHumanGate(missionId, gateId, request),
  });
}

export function requestPlanMutationOptions(missionId: string) {
  return mutationOptions({ mutationFn: (request: RequestPlanRequest) => api.requestPlan(missionId, request) });
}

export function editPlanProposalMutationOptions(missionId: string, artifactId: string) {
  return mutationOptions({ mutationFn: (request: EditPlanProposalRequest) => api.editPlanProposal(missionId, artifactId, request) });
}

export function rejectPlanProposalMutationOptions(missionId: string, artifactId: string) {
  return mutationOptions({ mutationFn: (request: RejectPlanProposalRequest) => api.rejectPlanProposal(missionId, artifactId, request) });
}

export function approvePlanProposalMutationOptions(missionId: string, artifactId: string) {
  return mutationOptions({
    mutationFn: (request: { commandId: string; expectedRevision: number }) =>
      api.approvePlanProposal(missionId, artifactId, request),
  });
}
