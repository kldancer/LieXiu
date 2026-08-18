// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { createAuthStore, registerAuthStore, useAuthStore } from "../auth";
import type { StorageAdapter, User, Workspace } from "../types";
import { workspaceKeys } from "../workspace/queries";
import { AuthInitializer } from "./auth-initializer";

const storage: StorageAdapter = {
  getItem: vi.fn(() => null),
  setItem: vi.fn(),
  removeItem: vi.fn(),
};

const owner = { id: "owner-1", name: "LieXiu Owner" } as User;
const workspace = { id: "workspace-1", slug: "liexiu" } as Workspace;

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        {children}
      </QueryClientProvider>
    );
  };
}

describe("AuthInitializer personal mode", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("creates a local session when the server advertises personal mode", async () => {
    const api = {
      getConfig: vi.fn().mockResolvedValue({ cdn_domain: "", auto_login: true }),
      getMe: vi.fn().mockRejectedValue(new Error("unauthorized")),
      listWorkspaces: vi.fn().mockRejectedValue(new Error("unauthorized")),
      startLocalSession: vi
        .fn()
        .mockResolvedValue({ user: owner, workspace, provisioned: false }),
    } as unknown as ApiClient;
    setApiInstance(api);
    registerAuthStore(createAuthStore({ api, storage, cookieAuth: true }));
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const onLogin = vi.fn();

    render(
      <AuthInitializer cookieAuth onLogin={onLogin} storage={storage}>
        <div>app</div>
      </AuthInitializer>,
      { wrapper: wrapper(queryClient) },
    );

    await waitFor(() => expect(useAuthStore.getState().isLoading).toBe(false));
    expect(api.startLocalSession).toHaveBeenCalledTimes(1);
    expect(api.getMe).not.toHaveBeenCalled();
    expect(api.listWorkspaces).not.toHaveBeenCalled();
    expect(useAuthStore.getState().user).toBe(owner);
    expect(queryClient.getQueryData(workspaceKeys.list())).toEqual([workspace]);
    expect(onLogin).toHaveBeenCalledTimes(1);
  });

  it("fails closed when the server does not advertise personal mode", async () => {
    const api = {
      getConfig: vi.fn().mockResolvedValue({ cdn_domain: "", auto_login: false }),
      getMe: vi.fn().mockRejectedValue(new Error("unauthorized")),
      listWorkspaces: vi.fn().mockRejectedValue(new Error("unauthorized")),
      startLocalSession: vi.fn(),
    } as unknown as ApiClient;
    setApiInstance(api);
    registerAuthStore(createAuthStore({ api, storage, cookieAuth: true }));
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    render(
      <AuthInitializer cookieAuth storage={storage}>
        <div>app</div>
      </AuthInitializer>,
      { wrapper: wrapper(queryClient) },
    );

    await waitFor(() => expect(useAuthStore.getState().isLoading).toBe(false));
    expect(api.startLocalSession).not.toHaveBeenCalled();
    expect(useAuthStore.getState().user).toBeNull();
  });
});
