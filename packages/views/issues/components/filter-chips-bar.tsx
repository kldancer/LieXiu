"use client";

import { useMemo, type ReactNode } from "react";
import {
  CalendarDays,
  CircleDot,
  FolderKanban,
  SignalHigh,
  Tag,
  User,
  UserPen,
  X,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@liexiu/ui/components/ui/button";
import { useWorkspaceId } from "@liexiu/core/hooks";
import { memberListOptions, agentListOptions } from "@liexiu/core/workspace/queries";
import { projectListOptions } from "@liexiu/core/projects/queries";
import { labelListOptions } from "@liexiu/core/labels/queries";
import {
  type ActorFilterValue,
  type FilterDimension,
  type IssueDateFilter,
} from "@liexiu/core/issues/stores/view-store";
import { useViewStore, useViewStoreApi } from "@liexiu/core/issues/stores/view-store-context";
import { StatusIcon } from "./status-icon";
import { PriorityIcon } from "./priority-icon";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";

/** One rendered chip: a dimension with its selected values summarised. */
interface FilterChip {
  key: string;
  icon: ReactNode;
  /** Dimension name, e.g. "状态". */
  label: string;
  /** Values summary, e.g. "进行中" or "4 个状态". */
  value: string;
  /** Up to three overlapping value icons rendered before the summary text. */
  valueIcons?: ReactNode;
  onRemove: () => void;
}

function summarize(names: (string | undefined)[]): string {
  const first = names.find((n): n is string => !!n);
  if (first === undefined) return String(names.length);
  const rest = names.length - 1;
  return rest > 0 ? `${first} +${rest}` : first;
}

const CHIP_ICON_CLASS = "size-3 shrink-0 text-muted-foreground";

/** Overlapping value icons, following the runtime-list stack pattern: each
 *  icon sits on an opaque wrapper so the one underneath cannot show through. */
function IconStack({ children }: { children: ReactNode[] }) {
  return (
    <span className="flex items-center -space-x-1">
      {children.map((child, i) => (
        <span
          key={i}
          className="inline-flex size-4 items-center justify-center rounded-full bg-background ring-1 ring-border"
        >
          {child}
        </span>
      ))}
    </span>
  );
}

/** Avatar variant of the stack — avatars are already opaque, so they only
 *  need the background-colored separation ring. */
function AvatarStack({ actors }: { actors: ActorFilterValue[] }) {
  return (
    <span className="flex items-center -space-x-1">
      {actors.slice(0, 3).map((actor) => (
        <span
          key={`${actor.type}:${actor.id}`}
          className="inline-flex rounded-full ring-2 ring-background"
        >
          <ActorAvatar
            actorType={actor.type}
            actorId={actor.id}
            size="xs"
            profileLink={false}
          />
        </span>
      ))}
    </span>
  );
}

/** Colored-dot stack for labels and select options. */
function DotStack({ colors }: { colors: string[] }) {
  if (colors.length === 0) return null;
  return (
    <IconStack>
      {colors.slice(0, 3).map((color, i) => (
        <span
          key={i}
          className="size-2.5 rounded-full"
          style={{ backgroundColor: color }}
        />
      ))}
    </IconStack>
  );
}

/**
 * Active-filter chips for the surrounding ViewStoreProvider's store — works
 * against the live surface store on pages and against the draft store inside
 * the save-view dialog. Name lookups reuse the caches the filter dropdown
 * already populates; a cache miss degrades a chip to a count, never blocks.
 */
function useFilterChips(
  dateFilter: IssueDateFilter | null,
  onDateFilterChange?: (filter: IssueDateFilter | null) => void,
) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();

  const statusFilters = useViewStore((s) => s.statusFilters);
  const priorityFilters = useViewStore((s) => s.priorityFilters);
  const assigneeFilters = useViewStore((s) => s.assigneeFilters);
  const includeNoAssignee = useViewStore((s) => s.includeNoAssignee);
  const creatorFilters = useViewStore((s) => s.creatorFilters);
  const projectFilters = useViewStore((s) => s.projectFilters);
  const includeNoProject = useViewStore((s) => s.includeNoProject);
  const labelFilters = useViewStore((s) => s.labelFilters);
  const store = useViewStoreApi();

  const hasStoreFilters =
    statusFilters.length > 0 ||
    priorityFilters.length > 0 ||
    assigneeFilters.length > 0 ||
    includeNoAssignee ||
    creatorFilters.length > 0 ||
    projectFilters.length > 0 ||
    includeNoProject ||
    labelFilters.length > 0;
  const showDateChip = !!onDateFilterChange && !!dateFilter;

  const enabled = hasStoreFilters;
  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: enabled && (assigneeFilters.length > 0 || creatorFilters.length > 0),
  });
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: enabled && (assigneeFilters.length > 0 || creatorFilters.length > 0),
  });
  const { data: projects = [] } = useQuery({
    ...projectListOptions(wsId),
    enabled: enabled && projectFilters.length > 0,
  });
  const { data: labels = [] } = useQuery({
    ...labelListOptions(wsId),
    enabled: enabled && labelFilters.length > 0,
  });

  const actorName = useMemo(() => {
    const byKey = new Map<string, string>();
    for (const m of members) byKey.set(`member:${m.id}`, m.name);
    for (const a of agents) byKey.set(`agent:${a.id}`, a.name);
    return (actor: ActorFilterValue) => byKey.get(`${actor.type}:${actor.id}`);
  }, [members, agents]);

  const clearDimension = (dimension: FilterDimension) => {
    store.getState().clearFilterDimension(dimension);
  };

  const deltaStatus = statusFilters;
  const deltaPriority = priorityFilters;
  const deltaAssignees = assigneeFilters;
  const deltaNoAssignee = includeNoAssignee;
  const deltaCreators = creatorFilters;
  const deltaProjects = projectFilters;
  const deltaNoProject = includeNoProject;
  const deltaLabels = labelFilters;

  const chips: FilterChip[] = [];

  const [onlyStatus] = deltaStatus;
  if (deltaStatus.length > 0) {
    chips.push({
      key: "status",
      icon: <CircleDot className={CHIP_ICON_CLASS} />,
      label: t(($) => $.filters.section_status),
      valueIcons: (
        <IconStack>
          {deltaStatus.slice(0, 3).map((s) => (
            <StatusIcon key={s} status={s} className="size-3" />
          ))}
        </IconStack>
      ),
      value:
        onlyStatus !== undefined && deltaStatus.length === 1
          ? t(($) => $.status[onlyStatus])
          : t(($) => $.filters.chip_status_count, { count: deltaStatus.length }),
      onRemove: () => clearDimension("status"),
    });
  }
  const [onlyPriority] = deltaPriority;
  if (deltaPriority.length > 0) {
    chips.push({
      key: "priority",
      icon: <SignalHigh className={CHIP_ICON_CLASS} />,
      label: t(($) => $.filters.section_priority),
      valueIcons: (
        <IconStack>
          {deltaPriority.slice(0, 3).map((p) => (
            <PriorityIcon key={p} priority={p} className="size-3" />
          ))}
        </IconStack>
      ),
      value:
        onlyPriority !== undefined && deltaPriority.length === 1
          ? t(($) => $.priority[onlyPriority])
          : t(($) => $.filters.chip_priority_count, { count: deltaPriority.length }),
      onRemove: () => clearDimension("priority"),
    });
  }
  if (deltaAssignees.length > 0 || deltaNoAssignee) {
    const names = deltaAssignees.map(actorName);
    if (deltaNoAssignee) names.push(t(($) => $.filters.no_assignee));
    chips.push({
      key: "assignee",
      icon: <User className={CHIP_ICON_CLASS} />,
      label: t(($) => $.filters.section_assignee),
      valueIcons: deltaAssignees.length > 0 ? <AvatarStack actors={deltaAssignees} /> : undefined,
      value: summarize(names),
      onRemove: () => clearDimension("assignee"),
    });
  }
  if (deltaCreators.length > 0) {
    chips.push({
      key: "creator",
      icon: <UserPen className={CHIP_ICON_CLASS} />,
      label: t(($) => $.filters.section_creator),
      valueIcons: <AvatarStack actors={deltaCreators} />,
      value: summarize(deltaCreators.map(actorName)),
      onRemove: () => clearDimension("creator"),
    });
  }
  if (deltaProjects.length > 0 || deltaNoProject) {
    const projectById = new Map(projects.map((p) => [p.id, p]));
    const names = deltaProjects.map((id) => projectById.get(id)?.title);
    if (deltaNoProject) names.push(t(($) => $.filters.no_project));
    const emojis = deltaProjects
      .map((id) => projectById.get(id)?.icon)
      .filter((icon): icon is string => !!icon);
    chips.push({
      key: "project",
      icon: <FolderKanban className={CHIP_ICON_CLASS} />,
      label: t(($) => $.filters.section_project),
      valueIcons:
        emojis.length > 0 ? (
          <IconStack>
            {emojis.slice(0, 3).map((emoji, i) => (
              <span key={i} className="text-micro leading-none">{emoji}</span>
            ))}
          </IconStack>
        ) : undefined,
      value: summarize(names),
      onRemove: () => clearDimension("project"),
    });
  }
  if (deltaLabels.length > 0) {
    const labelById = new Map(labels.map((l) => [l.id, l]));
    chips.push({
      key: "label",
      icon: <Tag className={CHIP_ICON_CLASS} />,
      label: t(($) => $.filters.section_label),
      valueIcons: (
        <DotStack
          colors={deltaLabels
            .map((id) => labelById.get(id)?.color)
            .filter((c): c is string => !!c)}
        />
      ),
      value: summarize(deltaLabels.map((id) => labelById.get(id)?.name)),
      onRemove: () => clearDimension("label"),
    });
  }
  if (showDateChip && dateFilter) {
    const fieldLabel =
      dateFilter.field === "created_at"
        ? t(($) => $.filters.date_field_created)
        : t(($) => $.filters.date_field_updated);
    const short = (dateOnly: string) => {
      const [, m, d] = dateOnly.split("-");
      return `${Number(m)}/${Number(d)}`;
    };
    chips.push({
      key: "date",
      icon: <CalendarDays className={CHIP_ICON_CLASS} />,
      label: fieldLabel,
      value:
        dateFilter.from === dateFilter.to
          ? short(dateFilter.from)
          : `${short(dateFilter.from)} - ${short(dateFilter.to)}`,
      onRemove: () => onDateFilterChange?.(null),
    });
  }

  const clearAll = () => {
    store.getState().clearFilters();
    onDateFilterChange?.(null);
  };

  return { chips, hasAny: chips.length > 0, clearAll };
}

function ChipSpan({ chip }: { chip: FilterChip }) {
  const { t } = useT("issues");
  return (
    // Keyed per dimension: the chip animates in once when its dimension
    // first gains a value; value changes inside it re-render without
    // re-animating. No exit animation — unmount presence isn't worth the
    // machinery for a 150ms nicety.
    <span className="flex h-6 max-w-72 items-center gap-1.5 rounded-md bg-background pl-2 pr-1 text-caption shadow-xs animate-in fade-in-0 zoom-in-95 duration-150">
      <span className="flex shrink-0 items-center gap-1 text-muted-foreground">
        {chip.icon}
        <span>{chip.label}</span>
      </span>
      <span className="flex min-w-0 items-center gap-1">
        {chip.valueIcons}
        <span className="truncate font-medium">{chip.value}</span>
      </span>
      <button
        type="button"
        aria-label={t(($) => $.filters.chip_remove, { name: chip.label })}
        onClick={chip.onRemove}
        className="flex size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        <X className="size-3" />
      </button>
    </span>
  );
}

/**
 * Bare chip list for form contexts (the save-view dialog): no bar chrome,
 * no page margins. `trailing` renders after the chips — the add-filter
 * trigger slot. Chips get a border here since they sit on the dialog
 * background rather than the muted bar. Menu-position stability while
 * adding filters is handled by the menu itself (IssueFilterMenu's
 * `freezeAnchor`), not by this list.
 */
export function FilterChipList({ trailing }: { trailing?: ReactNode }) {
  const { chips } = useFilterChips(null);
  return (
    <div className="flex flex-wrap items-center gap-1.5 [&>span]:border [&>span]:border-border [&>span]:shadow-none">
      {chips.map((chip) => (
        <ChipSpan key={chip.key} chip={chip} />
      ))}
      {trailing}
    </div>
  );
}

/**
 * Horizontal strip of active-filter chips shown under the issues header row.
 * Renders nothing when no filter is active. Each chip clears exactly its own
 * dimension; the right-aligned Clear resets everything (including the date
 * filter, which lives outside the view store).
 */
export function FilterChipsBar({
  dateFilter = null,
  onDateFilterChange,
}: {
  dateFilter?: IssueDateFilter | null;
  onDateFilterChange?: (filter: IssueDateFilter | null) => void;
}) {
  const { t } = useT("issues");
  const { chips, hasAny, clearAll } = useFilterChips(dateFilter, onDateFilterChange);

  if (!hasAny) return null;

  return (
    <div className="mx-4 mb-2 flex shrink-0 flex-wrap items-center gap-1.5 rounded-lg bg-muted/50 px-2 py-1.5">
      {chips.map((chip) => (
        <ChipSpan key={chip.key} chip={chip} />
      ))}
      <span className="ml-auto flex items-center gap-1.5">
        <Button
          variant="ghost"
          size="sm"
          onClick={clearAll}
          className="h-6 px-2 text-caption text-muted-foreground"
        >
          {t(($) => $.filters.chip_clear)}
        </Button>
      </span>
    </div>
  );
}
