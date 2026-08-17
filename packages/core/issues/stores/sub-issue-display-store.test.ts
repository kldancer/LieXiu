// @vitest-environment jsdom
import { beforeAll, beforeEach, describe, expect, it } from "vitest";
import {
  DEFAULT_SUB_ISSUE_ROW_PROPERTIES,
  useSubIssueDisplayStore,
} from "./sub-issue-display-store";

// Node 25 ships a partial `localStorage` shim under jsdom that's missing
// `clear`/`removeItem`; replace it with a real in-memory Storage so persist
// can round-trip values.
beforeAll(() => {
  if (typeof globalThis.localStorage?.setItem !== "function") {
    const values = new Map<string, string>();
    const storage: Storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (k) => values.get(k) ?? null,
      key: (i) => Array.from(values.keys())[i] ?? null,
      removeItem: (k) => { values.delete(k); },
      setItem: (k, v) => { values.set(k, v); },
    };
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
    Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
  }
});

describe("sub-issue display store", () => {
  beforeEach(() => {
    useSubIssueDisplayStore.setState({
      rowProperties: { ...DEFAULT_SUB_ISSUE_ROW_PROPERTIES },
    });
  });

  it("defaults to showing every built-in field", () => {
    const s = useSubIssueDisplayStore.getState();
    expect(s.rowProperties).toEqual({
      priority: true,
      labels: true,
      childProgress: true,
      dueDate: true,
      assignee: true,
    });
  });

  it("toggleRowProperty flips a single field without touching the rest", () => {
    useSubIssueDisplayStore.getState().toggleRowProperty("dueDate");
    expect(useSubIssueDisplayStore.getState().rowProperties).toEqual({
      ...DEFAULT_SUB_ISSUE_ROW_PROPERTIES,
      dueDate: false,
    });

    useSubIssueDisplayStore.getState().toggleRowProperty("dueDate");
    expect(useSubIssueDisplayStore.getState().rowProperties).toEqual(
      DEFAULT_SUB_ISSUE_ROW_PROPERTIES,
    );
  });

});
