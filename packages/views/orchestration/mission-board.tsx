"use client";

import { useMemo } from "react";
import { ClipboardList, GitBranch } from "lucide-react";
import type { MissionProjection, TaskNodeProjection } from "@liexiu/core/orchestration";
import { Badge } from "@liexiu/ui/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@liexiu/ui/components/ui/card";
import { cn } from "@liexiu/ui/lib/utils";
import { useT } from "../i18n";
import {
  BOARD_LANES,
  boardLaneForStatus,
  buildDagLayout,
  buildMailboxActivityViews,
  type BoardLane,
} from "./view-model";
import { MailboxActivityList } from "./mission-mailbox-activity";

export function MissionBoard({
  projection,
  selectedRunId,
  onSelectRun,
}: {
  projection: MissionProjection;
  selectedRunId: string;
  onSelectRun: (runId: string) => void;
}) {
  const { t } = useT("orchestration");
  const laneLabels: Record<BoardLane, string> = {
    queued: t(($) => $.board.queued),
    active: t(($) => $.board.active),
    review: t(($) => $.board.review),
    done: t(($) => $.board.done),
    attention: t(($) => $.board.attention),
  };
  const grouped = useMemo(() => {
    const lanes = new Map<BoardLane, TaskNodeProjection[]>(
      BOARD_LANES.map((lane) => [lane, []]),
    );
    for (const node of projection.nodes) {
      lanes.get(boardLaneForStatus(node.status))?.push(node);
    }
    return lanes;
  }, [projection.nodes]);
  const dag = useMemo(() => buildDagLayout(projection.nodes), [projection.nodes]);
  const mailboxActivities = useMemo(
    () => buildMailboxActivityViews(projection.activities.items),
    [projection.activities.items],
  );

  return (
    <Card className="min-h-[34rem] xl:min-h-0">
      <CardHeader className="border-b">
        <CardTitle className="flex items-center gap-2">
          <ClipboardList className="size-4" />
          {t(($) => $.board.title)}
        </CardTitle>
        <CardDescription>{t(($) => $.board.hint)}</CardDescription>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 overflow-auto">
        <div className="space-y-4">
          <MissionDag columns={dag} />
          <MailboxActivityList
            items={mailboxActivities.slice(-5).reverse()}
            onSelectRun={onSelectRun}
          />
          {BOARD_LANES.map((lane) => {
            const nodes = grouped.get(lane) ?? [];
            return (
              <section key={lane} aria-labelledby={`lane-${lane}`}>
                <div className="mb-2 flex items-center justify-between">
                  <h2 id={`lane-${lane}`} className="text-caption font-semibold uppercase tracking-wide text-muted-foreground">
                    {laneLabels[lane]}
                  </h2>
                  <Badge variant="outline">{nodes.length}</Badge>
                </div>
                {nodes.length === 0 ? (
                  <div className="rounded-lg border border-dashed px-3 py-2 text-caption text-muted-foreground">
                    {t(($) => $.board.empty)}
                  </div>
                ) : (
                  <div className="space-y-2">
                    {nodes.map((node) => {
                      const runId = node.latestRun?.id ?? "";
                      const selected = runId.length > 0 && runId === selectedRunId;
                      return (
                        <button
                          key={node.id}
                          type="button"
                          disabled={!runId}
                          aria-pressed={selected}
                          aria-label={`${node.key} ${node.title}`}
                          onClick={() => onSelectRun(runId)}
                          className={cn(
                            "w-full rounded-lg border p-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default",
                            selected
                              ? "border-primary bg-primary/10 hover:border-primary hover:bg-primary/10"
                              : "border-border bg-card hover:border-primary/50 hover:bg-accent/40",
                          )}
                        >
                          <div className="flex items-start justify-between gap-2">
                            <div className="min-w-0">
                              <p className="truncate text-body font-medium">
                                <span className="mr-1.5 font-mono text-caption text-muted-foreground">{node.key}</span>
                                {node.title}
                              </p>
                              <p className="mt-1 line-clamp-2 text-caption text-muted-foreground">
                                {node.description || node.duty}
                              </p>
                            </div>
                            <Badge variant={node.status === "failed" ? "destructive" : "outline"}>
                              {node.status}
                            </Badge>
                          </div>
                          <div className="mt-2 flex flex-wrap items-center gap-2 text-caption text-muted-foreground">
                            <span>{node.duty}</span>
                            <span>·</span>
                            <span>{t(($) => $.board.dependencies, { count: node.dependencyIds.length })}</span>
                            <span>·</span>
                            <span>{node.latestRun ? `Run #${node.latestRun.attempt}` : t(($) => $.board.no_run)}</span>
                          </div>
                        </button>
                      );
                    })}
                  </div>
                )}
              </section>
            );
          })}
        </div>
      </CardContent>
    </Card>
  );
}
function MissionDag({ columns }: { columns: ReturnType<typeof buildDagLayout> }) {
  const { t } = useT("orchestration");
  return (
    <section className="rounded-lg border bg-muted/20 p-3" aria-labelledby="mission-dag-title">
      <div className="mb-3">
        <h2 id="mission-dag-title" className="flex items-center gap-2 text-caption font-semibold uppercase tracking-wide">
          <GitBranch className="size-3.5" />
          {t(($) => $.board.dag_title)}
        </h2>
        <p className="mt-1 text-caption text-muted-foreground">{t(($) => $.board.dag_hint)}</p>
      </div>
      {columns.length === 0 ? (
        <p className="text-caption text-muted-foreground">{t(($) => $.board.empty)}</p>
      ) : (
        <div className="flex gap-3 overflow-x-auto pb-1" data-testid="mission-dag">
          {columns.map((column, depth) => (
            <div key={depth} className="flex min-w-32 flex-1 flex-col gap-2" data-dag-depth={depth}>
              <span className="font-mono text-caption text-muted-foreground">{t(($) => $.board.dag_stage, { stage: depth + 1 })}</span>
              {column.map((node) => (
                <div key={node.id} className="rounded-md border bg-background p-2 text-caption" title={node.title}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate font-mono font-medium">{node.key}</span>
                    <span className="size-2 shrink-0 rounded-full bg-current opacity-60" data-status={node.status} />
                  </div>
                  <p className="mt-1 truncate text-muted-foreground">
                    {node.dependencyKeys.length > 0
                      ? `← ${node.dependencyKeys.join(", ")}`
                      : t(($) => $.board.dag_root)}
                  </p>
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
