import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const { mockPush, mockReplace, mockCanonical, authState, searchParamsState } = vi.hoisted(() => ({
  mockPush: vi.fn(),
  mockReplace: vi.fn(),
  mockCanonical: vi.fn(),
  authState: { user: null as null | { id: string }, isLoading: false },
  searchParamsState: { value: new URLSearchParams() },
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: mockPush, replace: mockReplace }),
  useSearchParams: () => searchParamsState.value,
}));

vi.mock("@liexiu/core/auth", async () => {
  const actual = await vi.importActual<typeof import("@liexiu/core/auth")>("@liexiu/core/auth");
  return {
    ...actual,
    useAuthStore: (selector: (state: typeof authState) => unknown) => selector(authState),
  };
});

vi.mock("@liexiu/core/workspace/queries", () => ({
  workspaceKeys: { list: () => ["workspaces", "list"] },
  workspaceListOptions: () => ({
    queryKey: ["workspaces", "list"],
    queryFn: mockCanonical,
  }),
}));

vi.mock("@liexiu/views/auth", () => ({
  validateCliCallback: (value: string) => value.startsWith("http://127.0.0.1:"),
  LoginPage: ({ onSuccess, cliCallback }: { onSuccess: (workspace: unknown) => void; cliCallback?: unknown }) => (
    <div>
      <span>{cliCallback ? "cli-callback" : "local-bootstrap"}</span>
      <button onClick={() => onSuccess({ id: "ws-1", slug: "local" })}>complete-bootstrap</button>
    </div>
  ),
}));

vi.mock("@liexiu/views/i18n", () => ({
  useT: () => ({ t: () => "text" }),
}));

vi.mock("@/features/auth/auth-cookie", () => ({ setLoggedInCookie: vi.fn() }));

import Page from "./page";

function Wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe("single-owner web login", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authState.user = null;
    authState.isLoading = false;
    searchParamsState.value = new URLSearchParams();
    mockCanonical.mockResolvedValue([{ id: "ws-1", slug: "local" }]);
  });

  it("routes a bootstrap result directly to the canonical workspace", async () => {
    render(<Page />, { wrapper: Wrapper });
    await userEvent.click(screen.getByRole("button", { name: "complete-bootstrap" }));
    expect(mockPush).toHaveBeenCalledWith("/local/issues");
  });

  it("restores an existing session through the canonical workspace query", async () => {
    authState.user = { id: "owner-1" };
    render(<Page />, { wrapper: Wrapper });
    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith("/local/issues"));
    expect(mockCanonical).toHaveBeenCalledTimes(1);
  });

  it("keeps the safe CLI callback on the bootstrap screen", () => {
    searchParamsState.value = new URLSearchParams({
      cli_callback: "http://127.0.0.1:44123/callback",
      cli_state: "state-1",
    });
    render(<Page />, { wrapper: Wrapper });
    expect(screen.getByText("cli-callback")).toBeInTheDocument();
  });
});
