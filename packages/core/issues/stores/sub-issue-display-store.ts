import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../../platform/storage";

/**
 * Which properties the issue-detail sub-issues rows display.
 *
 * This is a personal reading preference (like the comment composer's sticky
 * toggle), not a per-view setting: every issue's sub-issues panel shares the
 * one configuration, persisted globally via `defaultStorage`. Defaults match
 * the panel's built-in row layout so the preference is invisible until the
 * user changes it.
 *
 */
export const SUB_ISSUE_ROW_PROPERTY_KEYS = [
  "priority",
  "labels",
  "childProgress",
  "dueDate",
  "assignee",
] as const;
export type SubIssueRowPropertyKey =
  (typeof SUB_ISSUE_ROW_PROPERTY_KEYS)[number];
export type SubIssueRowProperties = Record<SubIssueRowPropertyKey, boolean>;

export const DEFAULT_SUB_ISSUE_ROW_PROPERTIES: SubIssueRowProperties = {
  priority: true,
  labels: true,
  childProgress: true,
  dueDate: true,
  assignee: true,
};

interface SubIssueDisplayStore {
  rowProperties: SubIssueRowProperties;
  toggleRowProperty: (key: SubIssueRowPropertyKey) => void;
}

export const useSubIssueDisplayStore = create<SubIssueDisplayStore>()(
  persist(
    (set) => ({
      rowProperties: { ...DEFAULT_SUB_ISSUE_ROW_PROPERTIES },
      toggleRowProperty: (key) =>
        set((s) => ({
          rowProperties: { ...s.rowProperties, [key]: !s.rowProperties[key] },
        })),
    }),
    {
      name: "liexiu_sub_issue_display",
      storage: createJSONStorage(() => defaultStorage),
      // Deep-merge rowProperties so a key added in a future release defaults
      // to visible instead of undefined (persist's default shallow merge
      // would replace the whole object with the stale persisted one).
      merge: (persisted, current) => {
        const p = (persisted ?? {}) as Partial<SubIssueDisplayStore>;
        return {
          ...current,
          ...p,
          rowProperties: {
            ...current.rowProperties,
            ...(p.rowProperties ?? {}),
          },
        };
      },
    },
  ),
);
