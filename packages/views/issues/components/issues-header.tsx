"use client";

import type { ReactElement } from "react";
import { Columns3, List } from "lucide-react";
import type {
  Issue,
  IssueTableFacetSpec,
  IssueTableFacetsResponse,
  WorkingAgentSummary,
} from "@liexiu/core/types";
import type { IssueDateFilter, IssueViewState } from "@liexiu/core/issues/stores/view-store";
import { useViewStore } from "@liexiu/core/issues/stores/view-store-context";
import { Button } from "@liexiu/ui/components/ui/button";
import { cn } from "@liexiu/ui/lib/utils";
import { PAGE_GUTTER } from "../../layout/page-header";
import { useT } from "../../i18n";
import { WorkspaceAgentWorkingChip } from "./workspace-agent-working-chip";

export function ViewRefreshIndicator({ active }: { active: boolean }) {
  return (
    <span className="flex w-4 shrink-0 items-center justify-center">
      {active && <span className="size-3 animate-pulse rounded-full bg-muted-foreground/50" />}
    </span>
  );
}

type IssueHeaderProps = {
  scopedIssues: Issue[];
  workingAgents: WorkingAgentSummary[] | undefined;
  allowGantt?: boolean;
  dateFilter?: IssueDateFilter | null;
  onDateFilterChange?: (filter: IssueDateFilter | null) => void;
  isRefreshing?: boolean;
  facetCountsExact?: boolean;
  tableFacetCounts?: IssueTableFacetsResponse;
  onTableFacetChange?: (facet: IssueTableFacetSpec | null) => void;
};

export function IssuesHeader({
  scopedIssues,
  workingAgents,
  isRefreshing = false,
}: IssueHeaderProps) {
  const { t } = useT("issues");
  const agentRunningFilter = useViewStore((s) => s.agentRunningFilter);
  const toggleAgentRunningFilter = useViewStore((s) => s.toggleAgentRunningFilter);

  return (
    <div className={cn("min-h-12 shrink-0 py-2", PAGE_GUTTER)}>
      <div className="flex w-full min-w-0 items-center justify-end gap-1">
        {agentRunningFilter && (
          <span className="mr-1 hidden text-caption text-muted-foreground md:inline">
            {t(($) => $.agent_activity.filter_active_label)}
          </span>
        )}
        <WorkspaceAgentWorkingChip
          value={agentRunningFilter}
          onToggle={toggleAgentRunningFilter}
          agents={workingAgents}
        />
        <IssueDisplayControls scopedIssues={scopedIssues} />
        <ViewRefreshIndicator active={isRefreshing} />
      </div>
    </div>
  );
}

export function IssueDisplayControls({
  hideViewToggle = false,
}: {
  scopedIssues: Issue[];
  hideViewToggle?: boolean;
  allowGantt?: boolean;
  dateFilter?: IssueDateFilter | null;
  onDateFilterChange?: (filter: IssueDateFilter | null) => void;
  facetCountsExact?: boolean;
  tableFacetCounts?: IssueTableFacetsResponse;
  onTableFacetChange?: (facet: IssueTableFacetSpec | null) => void;
}) {
  const { t } = useT("issues");
  const viewMode = useViewStore((s) => s.viewMode);
  const setViewMode = useViewStore((s) => s.setViewMode);

  if (hideViewToggle) return null;

  return (
    <div className="flex items-center gap-1">
      {(["list", "board"] as const).map((mode) => (
        <Button
          key={mode}
          type="button"
          variant={viewMode === mode ? "secondary" : "ghost"}
          size="icon-sm"
          aria-label={t(($) => $.view[mode])}
          onClick={() => setViewMode(mode)}
        >
          {mode === "list" ? <List className="size-3.5" /> : <Columns3 className="size-3.5" />}
        </Button>
      ))}
    </div>
  );
}

export function IssueFilterMenu({
  trigger,
}: {
  trigger: ReactElement;
  tooltip?: string;
  scopedIssues?: Issue[];
  facetCountsExact?: boolean;
  tableFacetCounts?: IssueTableFacetsResponse;
  onTableFacetChange?: (facet: IssueTableFacetSpec | null) => void;
  dateFilter?: IssueDateFilter | null;
  onDateFilterChange?: (filter: IssueDateFilter | null) => void;
  onOpenChange?: (open: boolean) => void;
  freezeAnchor?: boolean;
}) {
  return trigger;
}

export type IssueHeaderViewState = Pick<IssueViewState, "viewMode">;
