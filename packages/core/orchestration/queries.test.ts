import { describe, expect, it } from "vitest";
import type { ActivityPage } from "./types";
import {
  missionActivitiesOptions,
  missionKeys,
  shouldRefreshMissionSnapshot,
} from "./queries";

describe("mission activity recovery query", () => {
  it("keys the incremental page by the canonical snapshot cursor", () => {
    expect(missionKeys.activities("workspace-1", "mission-1", 42)).toEqual([
      "missions",
      "workspace-1",
      "detail",
      "mission-1",
      "activities",
      42,
    ]);
  });

  it("polls only while the mission is active and always recovers on reconnect", () => {
    const active = missionActivitiesOptions("workspace-1", "mission-1", 42, true);
    const terminal = missionActivitiesOptions("workspace-1", "mission-1", 42, false);

    expect(active.enabled).toBe(true);
    expect(active.refetchInterval).toBe(1_500);
    expect(active.refetchOnReconnect).toBe("always");
    expect(terminal.enabled).toBe(false);
    expect(terminal.refetchInterval).toBe(false);
  });

  it("rebuilds the canonical snapshot after new activity, a gap, or a cursor mismatch", () => {
    const page = (overrides: Partial<Pick<ActivityPage, "items" | "lastSequence" | "resetRequired">>) => ({
      items: [] as ActivityPage["items"],
      lastSequence: 42,
      resetRequired: false,
      ...overrides,
    });

    expect(shouldRefreshMissionSnapshot(42, page({}))).toBe(false);
    expect(shouldRefreshMissionSnapshot(42, page({ items: [{} as ActivityPage["items"][number]] }))).toBe(true);
    expect(shouldRefreshMissionSnapshot(42, page({ lastSequence: 43 }))).toBe(true);
    expect(shouldRefreshMissionSnapshot(42, page({ resetRequired: true }))).toBe(true);
  });
});
