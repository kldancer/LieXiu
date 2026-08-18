"use client";

import { useMemo, useState } from "react";
import type { ProjectAttention, ProjectCommandCenterProjection, ProjectMissionSummary, ProjectOwnerAction } from "@liexiu/core/orchestration";

export interface ProjectCommandCenterOpenTarget {
  missionId: string;
  taskNodeId?: string;
  runId?: string;
  artifactId?: string;
  gateId?: string;
  actionKind?: ProjectOwnerAction["kind"];
}

export interface ProjectCommandCenterLabels {
  project: string; missionPortfolio: string; attentionQueue: string; ownerActions: string; capacity: string;
  agents: string; runtimes: string; capacityEmpty: string; missionCount: string; activeMissions: string;
  blockedMissions: string; completedMissions: string; attentionCount: string; tokens: string; cost: string;
  status: string; filter: string; kindFilter: string; severityFilter: string; all: string; sort: string;
  updated: string; risk: string; progress: string; noMissions: string; noAttention: string; evidence: string;
  revisions: string; requiredPermission: string; riskLabel: string; reason: string; openMission: string;
  disabled: string; duties: string; activeMissionsLabel: string; activeQueued: string; refresh: string;
  refreshing: string; truncated: string; actionLabels: Record<ProjectOwnerAction["kind"], string>;
  statusLabels: Record<ProjectMissionSummary["status"] | "all", string>;
  severityLabels: Record<ProjectAttention["severity"] | "all", string>;
  kindLabels: Record<ProjectAttention["kind"], string>;
}

export interface ProjectCommandCenterProps {
  projection: ProjectCommandCenterProjection;
  labels: ProjectCommandCenterLabels;
  onOpenMission: (target: ProjectCommandCenterOpenTarget) => void;
  isRefreshing?: boolean;
  onRefresh?: () => void;
}

type MissionSort = "updated" | "risk" | "progress";
type StatusFilter = ProjectMissionSummary["status"] | "all";
type SeverityFilter = ProjectAttention["severity"] | "all";
type KindFilter = ProjectAttention["kind"] | "all";
const MISSION_STATUSES: StatusFilter[] = ["all", "draft", "ready", "running", "blocked", "completed", "failed", "cancelled"];
const SEVERITIES: SeverityFilter[] = ["all", "critical", "high", "attention"];

export function ProjectCommandCenter({ projection, labels, onOpenMission, isRefreshing = false, onRefresh }: ProjectCommandCenterProps) {
  const [status, setStatus] = useState<StatusFilter>("all");
  const [missionSort, setMissionSort] = useState<MissionSort>("updated");
  const [severity, setSeverity] = useState<SeverityFilter>("all");
  const [kind, setKind] = useState<KindFilter>("all");
  const missions = useMemo(() => [...projection.missions].filter((mission) => status === "all" || mission.status === status).sort((left, right) => {
    if (missionSort === "updated") return right.updatedAt.localeCompare(left.updatedAt);
    if (missionSort === "progress") return right.progress.percent - left.progress.percent || left.id.localeCompare(right.id);
    return riskOf(right) - riskOf(left) || left.id.localeCompare(right.id);
  }), [missionSort, projection.missions, status]);
  const attention = useMemo(() => projection.attention.filter((item) => (severity === "all" || item.severity === severity) && (kind === "all" || item.kind === kind)), [kind, projection.attention, severity]);
  const openAttention = (item: ProjectAttention, action?: ProjectOwnerAction) => onOpenMission({ missionId: item.missionId, taskNodeId: item.taskNodeId, runId: item.runId, artifactId: item.artifactId, gateId: item.gateId, actionKind: action?.kind });

  return <section aria-label={labels.project} className="space-y-4">
    <header className="flex items-center justify-between gap-2"><div><h2>{projection.project.title}</h2><p>{labels.project}: {projection.project.status}</p></div>{onRefresh && <button type="button" onClick={onRefresh} disabled={isRefreshing}>{isRefreshing ? labels.refreshing : labels.refresh}</button>}</header>
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7"><Stat label={labels.missionCount} value={projection.totals.missionCount} /><Stat label={labels.activeMissions} value={projection.totals.activeMissions} /><Stat label={labels.blockedMissions} value={projection.totals.blockedMissions} /><Stat label={labels.completedMissions} value={projection.totals.completedMissions} /><Stat label={labels.attentionCount} value={projection.totals.attentionCount} /><Stat label={labels.tokens} value={`${projection.totals.consumedTokens}/${projection.totals.reservedTokens}`} /><Stat label={labels.cost} value={`${projection.totals.consumedCostUsdTicks}/${projection.totals.reservedCostUsdTicks}`} /></div>
    <MissionPortfolio missions={missions} status={status} missionSort={missionSort} labels={labels} onStatusChange={setStatus} onSortChange={setMissionSort} onOpenMission={onOpenMission} />
    <AttentionQueue attention={attention} severity={severity} kind={kind} labels={labels} onSeverityChange={setSeverity} onKindChange={setKind} onOpen={openAttention} />
    <CapacitySection capacity={projection.capacity} labels={labels} />
    {projection.truncated && <p>{labels.truncated}</p>}
  </section>;
}

function MissionPortfolio({ missions, status, missionSort, labels, onStatusChange, onSortChange, onOpenMission }: { missions: ProjectMissionSummary[]; status: StatusFilter; missionSort: MissionSort; labels: ProjectCommandCenterLabels; onStatusChange: (value: StatusFilter) => void; onSortChange: (value: MissionSort) => void; onOpenMission: (target: ProjectCommandCenterOpenTarget) => void }) {
  return <section aria-labelledby="project-command-center-missions"><h3 id="project-command-center-missions">{labels.missionPortfolio}</h3><div className="flex flex-wrap gap-2"><label>{labels.filter}<select aria-label={labels.status} value={status} onChange={(event) => onStatusChange(event.target.value as StatusFilter)}>{MISSION_STATUSES.map((value) => <option key={value} value={value}>{labels.statusLabels[value]}</option>)}</select></label><label>{labels.sort}<select value={missionSort} onChange={(event) => onSortChange(event.target.value as MissionSort)}><option value="updated">{labels.updated}</option><option value="risk">{labels.risk}</option><option value="progress">{labels.progress}</option></select></label></div><div className="divide-y rounded border">{missions.length === 0 ? <p className="p-2">{labels.noMissions}</p> : missions.map((mission) => <button className="block w-full p-2 text-left" type="button" key={mission.id} onClick={() => onOpenMission({ missionId: mission.id })}><span className="font-medium">{mission.title}</span><span className="ml-2">{labels.status}: {labels.statusLabels[mission.status]}</span><span className="ml-2">{mission.progress.percent}%</span><span className="ml-2">{labels.revisions}: {mission.revision}</span></button>)}</div></section>;
}

function AttentionQueue({ attention, severity, kind, labels, onSeverityChange, onKindChange, onOpen }: { attention: ProjectAttention[]; severity: SeverityFilter; kind: KindFilter; labels: ProjectCommandCenterLabels; onSeverityChange: (value: SeverityFilter) => void; onKindChange: (value: KindFilter) => void; onOpen: (item: ProjectAttention, action?: ProjectOwnerAction) => void }) {
  const kinds = Array.from(new Set(attention.map((item) => item.kind)));
  return <section aria-labelledby="project-command-center-attention"><h3 id="project-command-center-attention">{labels.attentionQueue}</h3><div className="flex flex-wrap gap-2"><label>{labels.severityFilter}<select aria-label={labels.severityFilter} value={severity} onChange={(event) => onSeverityChange(event.target.value as SeverityFilter)}>{SEVERITIES.map((value) => <option key={value} value={value}>{labels.severityLabels[value]}</option>)}</select></label><label>{labels.kindFilter}<select aria-label={labels.kindFilter} value={kind} onChange={(event) => onKindChange(event.target.value as KindFilter)}><option value="all">{labels.all}</option>{kinds.map((value) => <option key={value} value={value}>{labels.kindLabels[value]}</option>)}</select></label></div><div className="space-y-2">{attention.length === 0 ? <p>{labels.noAttention}</p> : attention.map((item) => <article className="rounded border p-2" key={item.id}><button type="button" onClick={() => onOpen(item)}>{labels.openMission}: {item.missionId}</button><div>{labels.kindLabels[item.kind]} · {labels.severityLabels[item.severity]}</div><div>{labels.evidence}: {item.subjectId} · {labels.revisions}: {revisionText(item)}</div><div className="space-y-1"><div>{labels.ownerActions}</div>{item.actions.map((action) => <ActionButton key={action.kind} action={action} item={item} labels={labels} onOpen={onOpen} />)}</div></article>)}</div></section>;
}

function ActionButton({ action, item, labels, onOpen }: { action: ProjectOwnerAction; item: ProjectAttention; labels: ProjectCommandCenterLabels; onOpen: (item: ProjectAttention, action?: ProjectOwnerAction) => void }) {
  const disabled = !action.enabled || action.kind === "reassign_task";
  return <div className="rounded bg-muted/30 p-1"><button type="button" disabled={disabled} title={disabled ? action.reasonCode : undefined} onClick={() => { if (!disabled) onOpen(item, action); }}>{labels.actionLabels[action.kind]}{disabled ? ` (${labels.disabled}: ${action.reasonCode})` : ""}</button><span className="ml-2">{labels.requiredPermission}: {action.requiredPermission} · {labels.riskLabel}: {action.risk} · {labels.reason}: {action.reasonCode}</span></div>;
}

function CapacitySection({ capacity, labels }: { capacity: ProjectCommandCenterProjection["capacity"]; labels: ProjectCommandCenterLabels }) {
  return <section aria-labelledby="project-command-center-capacity"><h3 id="project-command-center-capacity">{labels.capacity}</h3><Capacity title={labels.agents} entries={capacity.agents} labels={labels} /><Capacity title={labels.runtimes} entries={capacity.runtimes} labels={labels} /></section>;
}
function Capacity({ title, entries, labels }: { title: string; entries: ProjectCommandCenterProjection["capacity"]["agents"]; labels: ProjectCommandCenterLabels }) {
  return <div><h4>{title}</h4>{entries.length === 0 ? <p>{labels.capacityEmpty}</p> : entries.map((entry) => <div className="border-b p-2" key={entry.id}><span>{entry.name}</span> · <span>{entry.status}</span><div>{labels.duties}: {entry.duties.join(", ")}</div><div>{labels.activeMissionsLabel}: {entry.activeMissionIds.join(", ") || labels.all}</div><div>{labels.activeQueued}: {entry.activeRuns}/{entry.queuedRuns}</div></div>)}</div>;
}
function Stat({ label, value }: { label: string; value: number | string }) { return <div className="rounded border p-2"><div>{label}</div><strong>{value}</strong></div>; }
function revisionText(item: ProjectAttention) { return [item.missionRevision, item.taskRevision, item.gateRevision].filter((value) => value !== undefined).join("/"); }
function riskOf(mission: ProjectMissionSummary): number { return mission.status === "blocked" || mission.budget.status === "budget_exceeded" ? 4 : mission.budget.status === "approval_required" || mission.pendingHumanGates > 0 ? 3 : mission.pendingReviews > 0 || mission.status === "failed" ? 2 : mission.offlineAgents > 0 ? 1 : 0; }
