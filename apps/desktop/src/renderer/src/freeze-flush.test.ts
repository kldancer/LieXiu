import { afterEach, describe, expect, it, vi } from "vitest";

import { flushFreezeBreadcrumb } from "./freeze-flush";
import type { FreezeBreadcrumb } from "../../shared/freeze-breadcrumb";

const breadcrumb: FreezeBreadcrumb = {
  kind: "unresponsive",
  ts: 1_700_000_000_000,
  version: "0.4.11",
  context: {
    desktopRoute: { surface: "tab", path: "/:slug/issues" },
  },
};

afterEach(() => {
  vi.clearAllMocks();
});

describe("local freeze breadcrumb consumption", () => {
  it("acknowledges the exact local record immediately", () => {
    const ackFreeze = vi.fn();

    flushFreezeBreadcrumb({
      getLastFreeze: () => breadcrumb,
      ackFreeze,
    });

    expect(ackFreeze).toHaveBeenCalledWith(breadcrumb.ts);
  });

  it("does nothing when there is no pending local record", () => {
    const ackFreeze = vi.fn();

    flushFreezeBreadcrumb({
      getLastFreeze: () => null,
      ackFreeze,
    });

    expect(ackFreeze).not.toHaveBeenCalled();
  });

  it("returns a safe cleanup after consuming a record", () => {
    const cleanup = flushFreezeBreadcrumb({
      getLastFreeze: () => breadcrumb,
      ackFreeze: vi.fn(),
    });

    expect(() => cleanup()).not.toThrow();
  });
});
