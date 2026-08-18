"use client";

import { useMemo } from "react";
import {
  Activity,
  AlertTriangle,
  Box,
  CheckCircle2,
  GitBranch,
  LoaderCircle,
  MessageSquareText,
  RotateCcw,
} from "lucide-react";
import type {
  MissionProjection,
  RunDetailProjection,
  TaskNodeProjection,
} from "@liexiu/core/orchestration";
import { Badge } from "@liexiu/ui/components/ui/badge";
import { Button } from "@liexiu/ui/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@liexiu/ui/components/ui/card";
import { Skeleton } from "@liexiu/ui/components/ui/skeleton";
import { cn } from "@liexiu/ui/lib/utils";
import { useT } from "../i18n";
import { buildMailboxActivityViews } from "./view-model";
import { MailboxActivityRows } from "./mission-mailbox-activity";

export function RunDetailPanel({
  selectedRunId,
  projection,
  detail,
  loading,
  error,
  onSelectRun,
  onRetryTask,
  lifecyclePending,
}: {
  selectedRunId: string;
  projection: MissionProjection;
  detail?: RunDetailProjection;
  loading: boolean;
  error: boolean;
  onSelectRun: (runId: string) => void;
  onRetryTask?: (node: TaskNodeProjection) => Promise<void>;
  lifecyclePending: boolean;
}) {
  const { t } = useT("orchestration");
  const selectedNode = projection.nodes.find((node) => node.latestRun?.id === selectedRunId);
  const mailboxActivities = useMemo(
    () => buildMailboxActivityViews(projection.activities.items).filter((activity) =>
      activity.runId === selectedRunId || (!!selectedNode && activity.taskNodeId === selectedNode.id),
    ),
    [projection.activities.items, selectedNode, selectedRunId],
  );

  return (
    <Card className="min-h-[34rem] xl:min-h-0">
      <CardHeader className="border-b">
        <CardTitle className="flex items-center gap-2">
          <GitBranch className="size-4" />
          {t(($) => $.detail.title)}
        </CardTitle>
        <CardDescription>{t(($) => $.detail.hint)}</CardDescription>
        {detail?.run ?? selectedNode?.latestRun ? (
          <CardAction>
            <Badge variant="outline">{(detail?.run ?? selectedNode?.latestRun)?.status}</Badge>
          </CardAction>
        ) : null}
      </CardHeader>
      <CardContent className="min-h-0 flex-1 overflow-auto">
        {!selectedRunId ? (
          <EmptyDetail />
        ) : loading ? (
          <div className="space-y-3" aria-label={t(($) => $.detail.loading)}>
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-32 w-full" />
          </div>
        ) : error || !detail ? (
          <div className="flex min-h-48 flex-col items-center justify-center gap-2 text-center">
            <AlertTriangle className="size-6 text-destructive" />
            <p className="text-body font-medium">{t(($) => $.detail.error)}</p>
          </div>
        ) : (
          <div className="space-y-5">
            <div>
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-title-sm font-semibold">{detail.node.key} · {detail.node.title}</h2>
                <Badge variant="secondary">{t(($) => $.detail.attempt, { attempt: detail.run.attempt })}</Badge>
              </div>
              <p className="mt-1 break-all font-mono text-caption text-muted-foreground">{detail.run.id}</p>
              {(detail.node.status === "failed" || detail.node.status === "blocked") && onRetryTask && !projection.humanGates.some((gate) => gate.status === "pending" && gate.taskNodeId === detail.node.id) ? (
                <Button className="mt-3" size="sm" variant="outline" disabled={lifecyclePending} onClick={() => void onRetryTask(detail.node)}>
                  <RotateCcw className="size-4" />
                  {t(($) => $.detail.retry)}
                </Button>
              ) : null}
            </div>

            <DetailSection icon={LoaderCircle} title={t(($) => $.detail.execution)}>
              <KeyValue label={t(($) => $.detail.runtime)} value={detail.agent?.runtimeName ?? detail.assignment.runtimeId} />
              <KeyValue label="agent" value={detail.agent?.agentName ?? detail.assignment.agentId} />
              <KeyValue label="duty" value={detail.agent?.duty ?? detail.node.duty} />
              <KeyValue label="provider / model" value={[detail.agent?.provider, detail.agent?.model].filter(Boolean).join(" / ") || "—"} />
              <KeyValue label="status" value={detail.execution?.status ?? detail.run.status} />
              {detail.run.failureMessage ? <KeyValue label={t(($) => $.detail.failure)} value={detail.run.failureMessage} danger /> : null}
            </DetailSection>

            <DetailSection icon={Box} title={t(($) => $.detail.artifacts)} count={detail.artifacts.length}>
              {detail.artifacts.length === 0 ? <EmptyRow /> : detail.artifacts.map((artifact) => (
                <div key={artifact.id} className="rounded-lg border p-2.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-caption font-medium">{artifact.kind}</span>
                    <Badge variant="outline">{t(($) => $.detail.version, { version: artifact.version })}</Badge>
                  </div>
                  {artifact.summary ? <p className="mt-1 text-caption text-muted-foreground">{artifact.summary}</p> : null}
                  {isWebUrl(artifact.uri) ? (
                    <a className="mt-1 block truncate text-caption text-primary underline-offset-4 hover:underline" href={artifact.uri} target="_blank" rel="noreferrer">
                      {artifact.uri}
                    </a>
                  ) : artifact.uri ? (
                    <p className="mt-1 break-all font-mono text-caption text-muted-foreground">{artifact.uri}</p>
                  ) : null}
                </div>
              ))}
            </DetailSection>

            <DetailSection icon={CheckCircle2} title={t(($) => $.detail.reviews)} count={detail.reviews.length}>
              {detail.reviews.length === 0 ? <EmptyRow /> : detail.reviews.map((review) => (
                <div key={review.id} className="rounded-lg border p-2.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-caption">{review.decision}</span>
                    <span className="text-caption text-muted-foreground">{review.requestedChanges.length}</span>
                  </div>
                  {review.requestedChanges.length > 0 ? (
                    <p className="mt-1 line-clamp-3 text-caption text-muted-foreground">{formatEvidence(review.requestedChanges)}</p>
                  ) : null}
                </div>
              ))}
            </DetailSection>

            <DetailSection icon={GitBranch} title={t(($) => $.detail.lineage)}>
              <div className="flex flex-wrap gap-2">
                <Badge variant="outline">{t(($) => $.detail.assignments, { count: detail.lineage.assignments.length })}</Badge>
                <Badge variant="outline">{t(($) => $.detail.runs, { count: detail.lineage.runs.length })}</Badge>
              </div>
              <div className="flex flex-wrap gap-2">
                {detail.lineage.runs.map((run) => (
                  <Button
                    key={run.id}
                    type="button"
                    size="sm"
                    variant={run.id === selectedRunId ? "secondary" : "outline"}
                    onClick={() => onSelectRun(run.id)}
                    aria-pressed={run.id === selectedRunId}
                  >
                    #{run.attempt} · {run.status}
                  </Button>
                ))}
              </div>
            </DetailSection>

            <DetailSection icon={MessageSquareText} title={t(($) => $.collaboration.title)} count={mailboxActivities.length}>
              <MailboxActivityRows items={mailboxActivities.slice(-6).reverse()} onSelectRun={onSelectRun} />
            </DetailSection>

            <DetailSection icon={MessageSquareText} title={t(($) => $.detail.messages)} count={detail.messages.length}>
              {detail.messages.length === 0 ? <EmptyRow /> : detail.messages.slice(-6).map((message) => (
                <div key={`${message.sequence}-${message.createdAt}`} className="rounded-lg bg-muted/60 p-2.5 text-caption">
                  <div className="flex items-center justify-between gap-2 font-mono text-muted-foreground">
                    <span>#{message.sequence} · {message.type}</span>
                    <span>{message.tool}</span>
                  </div>
                  {message.content ? <p className="mt-1 line-clamp-3 whitespace-pre-wrap">{message.content}</p> : null}
                </div>
              ))}
            </DetailSection>

            <DetailSection icon={Activity} title={t(($) => $.detail.usage)} count={detail.usage.length}>
              {detail.usage.length === 0 ? <EmptyRow /> : detail.usage.map((usage) => (
                <div key={`${usage.provider}-${usage.model}-${usage.createdAt}`} className="flex items-center justify-between gap-2 rounded-lg border p-2.5 text-caption">
                  <span className="truncate">{usage.provider} / {usage.model}</span>
                  <span className="shrink-0 tabular-nums text-muted-foreground">
                    {t(($) => $.detail.tokens, { count: usage.inputTokens + usage.outputTokens })}
                  </span>
                </div>
              ))}
            </DetailSection>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
function isWebUrl(value: string) {
  return /^https?:\/\//i.test(value);
}

function formatEvidence(value: unknown) {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function DetailSection({ icon: Icon, title, count, children }: { icon: typeof Activity; title: string; count?: number; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="mb-2 flex items-center gap-2 text-caption font-semibold uppercase tracking-wide text-muted-foreground">
        <Icon className="size-3.5" />
        {title}
        {count !== undefined ? <span className="ml-auto tabular-nums">{count}</span> : null}
      </h3>
      <div className="space-y-2">{children}</div>
    </section>
  );
}

function KeyValue({ label, value, danger = false }: { label: string; value: string; danger?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-3 text-caption">
      <span className="text-muted-foreground">{label}</span>
      <span className={cn("break-all text-right font-mono", danger && "text-destructive")}>{value}</span>
    </div>
  );
}

function EmptyRow() {
  const { t } = useT("orchestration");
  return <p className="rounded-lg border border-dashed p-2.5 text-caption text-muted-foreground">{t(($) => $.detail.none)}</p>;
}

function EmptyDetail() {
  const { t } = useT("orchestration");
  return (
    <div className="flex min-h-72 flex-col items-center justify-center gap-2 text-center">
      <GitBranch className="size-7 text-muted-foreground" />
      <p className="text-body font-medium">{t(($) => $.detail.empty_title)}</p>
      <p className="max-w-xs text-caption text-muted-foreground">{t(($) => $.detail.empty_hint)}</p>
    </div>
  );
}
