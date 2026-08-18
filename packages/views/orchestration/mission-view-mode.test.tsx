import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { NavigationAdapter } from "../navigation";
import { NavigationProvider } from "../navigation";
import { useMissionViewMode } from "./mission-view-mode";

function Mode() {
  const { mode, setMode } = useMissionViewMode();
  return <><span data-testid="mode">{mode}</span><button type="button" onClick={() => setMode("replay")}>replay</button></>;
}

function navigation(search: string, replace = vi.fn()): NavigationAdapter {
  return { pathname: "/acme/missions/mission-1", searchParams: new URLSearchParams(search), push: vi.fn(), replace, back: vi.fn(), getShareableUrl: (path) => path };
}

describe("useMissionViewMode", () => {
  it("restores a deep link and preserves the shared Run selection", async () => {
    const replace = vi.fn();
    render(<NavigationProvider value={navigation("view=world&run=run-2", replace)}><Mode /></NavigationProvider>);
    await userEvent.click(screen.getByRole("button", { name: "replay" }));
    expect(replace).toHaveBeenCalledWith("/acme/missions/mission-1?view=replay&run=run-2");
  });

  it("fails soft for an unknown mode and follows external URL changes", async () => {
    const { rerender } = render(<NavigationProvider value={navigation("view=unknown")}><Mode /></NavigationProvider>);
    expect(screen.getByTestId("mode")).toHaveTextContent("world");
    rerender(<NavigationProvider value={navigation("view=replay")}><Mode /></NavigationProvider>);
    await waitFor(() => expect(screen.getByTestId("mode")).toHaveTextContent("replay"));
  });
});
