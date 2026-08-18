"use client";

import { MessageSquareText } from "lucide-react";
import { Badge } from "@liexiu/ui/components/ui/badge";
import { useT } from "../i18n";
import type { MailboxActivityView } from "./view-model";

export function MailboxActivityList({ items, onSelectRun }: { items: MailboxActivityView[]; onSelectRun: (runId: string) => void }) {
  const { t } = useT("orchestration");
  return (
    <section className="shrink-0 rounded-lg border bg-muted/20 p-3" aria-label={t(($) => $.collaboration.title)}>
      <h2 className="flex items-center gap-2 text-caption font-semibold uppercase tracking-wide text-muted-foreground">
        <MessageSquareText className="size-3.5" />
        {t(($) => $.collaboration.title)}
        <span className="ml-auto tabular-nums">{items.length}</span>
      </h2>
      <p className="mt-1 text-caption text-muted-foreground">{t(($) => $.collaboration.hint)}</p>
      <div className="mt-2 space-y-2">
        <MailboxActivityRows items={items} onSelectRun={onSelectRun} />
      </div>
    </section>
  );
}
export function MailboxActivityRows({ items, onSelectRun }: { items: MailboxActivityView[]; onSelectRun: (runId: string) => void }) {
  const { t } = useT("orchestration");
  if (items.length === 0) return <p className="text-caption text-muted-foreground">{t(($) => $.collaboration.empty)}</p>;
  return items.map((item) => (
    <button
      key={`${item.sequence}-${item.id}`}
      type="button"
      disabled={!item.runId}
      onClick={() => item.runId && onSelectRun(item.runId)}
      aria-label={t(($) => $.collaboration.locate, { sequence: item.sequence })}
      data-mailbox-message-id={item.messageId}
      data-mailbox-status={item.status}
      className="block w-full rounded-lg border bg-background p-2.5 text-left text-caption transition-colors hover:border-primary/50 disabled:cursor-default disabled:hover:border-border"
    >
      <span className="flex items-center gap-2">
        <span className="font-mono text-muted-foreground">#{item.sequence}</span>
        <span className="min-w-0 flex-1 truncate font-medium">{item.messageType}</span>
        <Badge variant={mailboxStatusVariant(item.status)}>{item.status}</Badge>
      </span>
      <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground">
        <span>{t(($) => $.collaboration.recipient)}: {item.recipientType}/{item.recipientId}</span>
        <span>{t(($) => $.collaboration.hops)}: {item.hops}/8</span>
        {item.hops >= 8 ? <span className="font-medium text-destructive">{t(($) => $.collaboration.hop_limit)}</span> : null}
      </span>
      <span className="mt-1 block truncate text-muted-foreground">
        {t(($) => $.collaboration.expires)}: <time dateTime={item.expiresAt}>{item.expiresAt}</time>
      </span>
    </button>
  ));
}

function mailboxStatusVariant(status: MailboxActivityView["status"]): "outline" | "secondary" | "destructive" {
  if (status === "expired" || status === "cancelled") return "destructive";
  if (status === "consumed") return "secondary";
  return "outline";
}
