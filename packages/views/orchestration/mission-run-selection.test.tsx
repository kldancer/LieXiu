import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { NavigationAdapter } from "../navigation";
import { NavigationProvider } from "../navigation";
import { useMissionRunSelection } from "./mission-run-selection";

function SelectionHarness({ navigation }: { navigation: NavigationAdapter }) {
  return (
    <NavigationProvider value={navigation}>
      <Selection />
    </NavigationProvider>
  );
}

function Selection() {
  const { selectedRunId, selectRun } = useMissionRunSelection(
    new Set(["run-1", "run-2"]),
  );
  return (
    <>
      <span>{selectedRunId}</span>
      <button type="button" onClick={() => selectRun("run-1")}>select</button>
    </>
  );
}

function navigation(search: string, replace = vi.fn()): NavigationAdapter {
  return {
    pathname: "/acme/missions/mission-1",
    searchParams: new URLSearchParams(search),
    push: vi.fn(),
    replace,
    back: vi.fn(),
    getShareableUrl: (path) => path,
  };
}

describe("useMissionRunSelection", () => {
  it("restores a valid deep link and preserves unrelated query parameters", async () => {
    const replace = vi.fn();
    render(<SelectionHarness navigation={navigation("view=world&run=run-2", replace)} />);
    expect(screen.getByText("run-2")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "select" }));
    expect(replace).toHaveBeenCalledWith(
      "/acme/missions/mission-1?view=world&run=run-1",
    );
  });

  it("falls back safely for stale IDs and follows external URL changes", async () => {
    const { rerender } = render(
      <SelectionHarness navigation={navigation("run=missing")} />,
    );
    expect(screen.getByText("run-1")).toBeInTheDocument();

    rerender(<SelectionHarness navigation={navigation("run=run-2")} />);
    await waitFor(() => expect(screen.getByText("run-2")).toBeInTheDocument());
  });
});
