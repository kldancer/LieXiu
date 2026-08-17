"use client";

import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Issue, Project } from "@liexiu/core/types";
import { ALL_STATUSES } from "@liexiu/core/issues/config";
import { projectListOptions } from "@liexiu/core/projects/queries";
import { childIssueProgressOptions } from "@liexiu/core/issues/queries";
import type { IssueStatus } from "@liexiu/core/types";
import {
  applyIssueFilters,
  type IssueFilterState,
  type IssueFilters,
} from "../utils/filter";
import type { ChildProgress } from "../components/list-row";
import type {
  IssueStatusBranches,
  IssueStatusPagination,
} from "./use-issue-status-branches";
import type { IssueGroupBranches } from "./use-issue-group-branches";

const EMPTY_ISSUES: Issue[] = [];
const EMPTY_CHILD_PROGRESS = new Map<string, ChildProgress>();
const EMPTY_PROJECTS: Project[] = [];

export interface IssueSurfaceData {
  surfaceIssues: Issue[];
  projectIssues: Issue[];
  issues: Issue[];
  swimlaneIssues: Issue[];
  /** Retained as an empty compatibility field for old controller consumers. */
  ganttWorkingScopeIssues: Issue[] | undefined;
  filteredGanttIssues: Issue[];
  ganttIssues: Issue[];
  visibleStatuses: IssueStatus[];
  hiddenStatuses: IssueStatus[];
  statusPagination: IssueStatusPagination;
  activeFilters: Omit<IssueFilters, "statusFilters">;
  childProgressMap: Map<string, ChildProgress>;
  projectMap: Map<string, Project>;
  resolveTableExportLookups: (needs: {
    projects: boolean;
    childProgress: boolean;
  }) => Promise<{
    projectMap: Map<string, Project>;
    childProgressMap: Map<string, ChildProgress>;
  }>;
  isLoading: boolean;
  /** The window's data is being revalidated while the previous snapshot is
   *  shown as a placeholder (sort/date change, or any grouped-board filter
   *  change). Drives the header's deferred refresh indicator — content stays
   *  put, so this is NOT a loading state. */
  isRefreshing: boolean;
  isEmpty: boolean;
}

export function useIssueSurfaceData({
  wsId,
  serverStatusBranches,
  serverGroupBranches,
  statusFilters,
  priorityFilters,
  assigneeFilters,
  includeNoAssignee,
  agentRunningFilter,
  creatorFilters,
  projectFilters,
  includeNoProject,
  labelFilters,
  workingIssueIDs,
  showSubIssues,
  loadProjects,
}: {
  wsId: string;
  serverStatusBranches: IssueStatusBranches;
  serverGroupBranches: IssueGroupBranches;
  statusFilters: IssueStatus[];
  priorityFilters: IssueFilterState["priorityFilters"];
  assigneeFilters: IssueFilterState["assigneeFilters"];
  includeNoAssignee: boolean;
  agentRunningFilter: boolean;
  creatorFilters: IssueFilterState["creatorFilters"];
  projectFilters: string[];
  includeNoProject: boolean;
  labelFilters: string[];
  /** Distinct running-task issue ids projected by `/api/working-agents`. */
  workingIssueIDs: ReadonlySet<string>;
  showSubIssues: boolean;
  loadProjects: boolean;
}): IssueSurfaceData {
  const workingFilterContext = useMemo(
    () => ({ runningIssueIds: workingIssueIDs }),
    [workingIssueIDs],
  );
  const bucketedIssues = serverStatusBranches.enabled
    ? serverStatusBranches.issues
    : serverGroupBranches.enabled
      ? serverGroupBranches.issues
      : EMPTY_ISSUES;

  // `cancelled` is a first-class default status (MUL-4290): it is fetched into
  // the cache like every other status and flows straight through to list /
  // board / swimlane columns, header facet counts, batch selection, and the
  // isEmpty check. The status filter narrows this set like any other status —
  // it no longer unlocks an otherwise-hidden bucket.
  const surfaceIssues = bucketedIssues;

  const baseFilterState = useMemo<IssueFilterState>(
    () => ({
      statusFilters,
      priorityFilters,
      assigneeFilters,
      includeNoAssignee,
      creatorFilters,
      projectFilters,
      includeNoProject,
      labelFilters,
      workingOnly: agentRunningFilter,
      showSubIssues,
    }),
    [
      assigneeFilters,
      agentRunningFilter,
      creatorFilters,
      includeNoAssignee,
      includeNoProject,
      labelFilters,
      priorityFilters,
      projectFilters,
      showSubIssues,
      statusFilters,
    ],
  );

  const issues = useMemo(
    () =>
      serverStatusBranches.enabled
        ? surfaceIssues
        : applyIssueFilters(
            surfaceIssues,
            baseFilterState,
            workingFilterContext,
          ),
    [
      baseFilterState,
      serverStatusBranches.enabled,
      surfaceIssues,
      workingFilterContext,
    ],
  );

  const statuslessFilterState = useMemo<IssueFilterState>(
    () => ({
      ...baseFilterState,
      statusFilters: [],
    }),
    [baseFilterState],
  );

  const swimlaneIssues = useMemo(
    () =>
      applyIssueFilters(
        surfaceIssues,
        statuslessFilterState,
        workingFilterContext,
      ),
    [statuslessFilterState, surfaceIssues, workingFilterContext],
  );

  const {
    data: childProgressData,
    refetch: refetchChildProgress,
  } = useQuery(childIssueProgressOptions(wsId));
  const childProgressMap = childProgressData ?? EMPTY_CHILD_PROGRESS;
  const {
    data: projectData,
    refetch: refetchProjects,
  } = useQuery({
    ...projectListOptions(wsId),
    enabled: loadProjects,
  });
  const projects = projectData ?? EMPTY_PROJECTS;
  const projectMap = useMemo(
    () => new Map(projects.map((project) => [project.id, project])),
    [projects],
  );
  const resolveTableExportLookups = useCallback(
    async (needs: { projects: boolean; childProgress: boolean }) => {
      const [projectResult, progressResult] = await Promise.all([
        needs.projects ? refetchProjects() : Promise.resolve(null),
        needs.childProgress
          ? refetchChildProgress()
          : Promise.resolve(null),
      ]);
      if (projectResult?.error) throw projectResult.error;
      if (progressResult?.error) throw progressResult.error;
      if (needs.projects && !projectResult?.data) {
        throw new Error("Failed to load project data for export");
      }
      if (needs.childProgress && !progressResult?.data) {
        throw new Error("Failed to load child progress for export");
      }
      const resolvedProjects = projectResult?.data ?? projects;
      return {
        projectMap: new Map(
          resolvedProjects.map((project) => [project.id, project]),
        ),
        childProgressMap: progressResult?.data ?? childProgressMap,
      };
    },
    [
      childProgressMap,
      projects,
      refetchChildProgress,
      refetchProjects,
    ],
  );

  const visibleStatuses = useMemo<IssueStatus[]>(() => {
    // Default view shows every lifecycle status, `cancelled` last (its
    // canonical position in ALL_STATUSES). An active status filter narrows to
    // the selected subset while preserving that order.
    if (statusFilters.length > 0) {
      return ALL_STATUSES.filter((s) => statusFilters.includes(s));
    }
    return ALL_STATUSES;
  }, [statusFilters]);

  // Hidden columns are the lifecycle statuses not currently visible, so
  // `cancelled` participates in the board show/hide controls exactly like the
  // rest of the statuses.
  const hiddenStatuses = useMemo<IssueStatus[]>(
    () => ALL_STATUSES.filter((s) => !visibleStatuses.includes(s)),
    [visibleStatuses],
  );

  const activeFilters = useMemo(
    () => ({
      priorityFilters,
      assigneeFilters,
      includeNoAssignee,
      agentRunningFilter,
      runningIssueIds: workingIssueIDs,
      creatorFilters,
      projectFilters,
      includeNoProject,
      labelFilters,
      showSubIssues,
    }),
    [
      assigneeFilters,
      agentRunningFilter,
      creatorFilters,
      includeNoAssignee,
      includeNoProject,
      labelFilters,
      priorityFilters,
      projectFilters,
      showSubIssues,
      workingIssueIDs,
    ],
  );

  const isLoading = serverGroupBranches.enabled
    ? serverGroupBranches.isLoading
    : serverStatusBranches.enabled
        ? serverStatusBranches.isLoading
        : false;

  // Placeholder-backed revalidation of the active list/group query.
  const isRefreshing = serverGroupBranches.enabled
    ? serverGroupBranches.isRefreshing
    : serverStatusBranches.enabled
      ? serverStatusBranches.isRefreshing
      : false;

  return {
    surfaceIssues,
    projectIssues: surfaceIssues,
    issues,
    swimlaneIssues,
    ganttWorkingScopeIssues: undefined,
    filteredGanttIssues: EMPTY_ISSUES,
    ganttIssues: EMPTY_ISSUES,
    visibleStatuses,
    hiddenStatuses,
    statusPagination: serverStatusBranches.pagination,
    activeFilters,
    childProgressMap,
    projectMap,
    resolveTableExportLookups,
    isLoading,
    isRefreshing,
    // The board/list data is the full window, so an empty result proves it.
    isEmpty:
      !isLoading &&
      (serverStatusBranches.enabled
        ? serverStatusBranches.isTotalKnown &&
          serverStatusBranches.total === 0
        : serverGroupBranches.enabled &&
          !serverGroupBranches.isError &&
          serverGroupBranches.total === 0),
  };
}
