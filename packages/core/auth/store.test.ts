import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "../api/client";
import { ApiError } from "../api/client";
import type { BootstrapResponse, StorageAdapter, User } from "../types";
import { createAuthStore } from "./store";

const fakeUser: User = {
  id: "u1",
  name: "Alice",
  email: "alice@example.com",
  avatar_url: null,
} as User;

function makeStorage(initial: Record<string, string> = {}): StorageAdapter & {
  snapshot: () => Record<string, string>;
} {
  const data = { ...initial };
  return {
    getItem: (k) => data[k] ?? null,
    setItem: (k, v) => {
      data[k] = v;
    },
    removeItem: (k) => {
      delete data[k];
    },
    snapshot: () => ({ ...data }),
  };
}

function makeApi(getMe: () => Promise<User>): ApiClient {
  return {
    setToken: vi.fn(),
    getMe,
    // Only the methods touched by store.initialize are needed. Cast to
    // ApiClient for type compatibility — the store treats it opaquely.
  } as unknown as ApiClient;
}

describe("authStore.initialize — token mode", () => {
  it("keeps the stored token when getMe fails with a non-401 ApiError (e.g. 500)", async () => {
    const storage = makeStorage({ liexiu_token: "t" });
    const api = makeApi(() =>
      Promise.reject(new ApiError("server error", 500, "Internal Server Error")),
    );
    const store = createAuthStore({ api, storage });

    await store.getState().initialize();

    expect(store.getState().user).toBeNull();
    expect(store.getState().isLoading).toBe(false);
    expect(storage.snapshot().liexiu_token).toBe("t");
  });

  it("keeps the stored token on a network failure (non-ApiError throw)", async () => {
    const storage = makeStorage({ liexiu_token: "t" });
    const api = makeApi(() => Promise.reject(new TypeError("fetch failed")));
    const store = createAuthStore({ api, storage });

    await store.getState().initialize();

    expect(store.getState().user).toBeNull();
    expect(storage.snapshot().liexiu_token).toBe("t");
  });

  it("on 401, leaves storage cleanup to ApiClient.onUnauthorized and resets state", async () => {
    // Simulate the real path: ApiClient fires onUnauthorized on 401, which
    // removes the token from storage. The store's catch block must not
    // duplicate or short-circuit this — it should only reset in-memory
    // auth state.
    const storage = makeStorage({ liexiu_token: "t" });
    const api = makeApi(() => {
      storage.removeItem("liexiu_token"); // stand-in for onUnauthorized
      return Promise.reject(new ApiError("unauthorized", 401, "Unauthorized"));
    });
    const store = createAuthStore({ api, storage });

    await store.getState().initialize();

    expect(store.getState().user).toBeNull();
    expect(storage.snapshot().liexiu_token).toBeUndefined();
  });

  it("populates user when getMe succeeds", async () => {
    const storage = makeStorage({ liexiu_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage });

    await store.getState().initialize();

    expect(store.getState().user).toEqual(fakeUser);
    expect(storage.snapshot().liexiu_token).toBe("t");
  });
});

describe("authStore.bootstrap", () => {
  const response: BootstrapResponse = {
    token: "bootstrap-token",
    user: fakeUser,
    workspace: {
      id: "workspace-1",
      name: "Local",
      slug: "local",
      description: null,
      context: null,
      settings: {},
      repos: [],
      issue_prefix: "MUL",
      avatar_url: null,
      created_at: "",
      updated_at: "",
    },
    provisioned: true,
  };

  it("persists only the token in legacy token mode", async () => {
    const storage = makeStorage();
    const api = {
      bootstrap: vi.fn().mockResolvedValue(response),
      setToken: vi.fn(),
    } as unknown as ApiClient;
    const store = createAuthStore({ api, storage });

    await store.getState().bootstrap({
      secret: "never-persist-this",
      owner_name: "Owner",
      owner_email: "owner@example.com",
      workspace_name: "Local",
      workspace_slug: "local",
      workspace_id: "",
    });

    expect(storage.snapshot()).toEqual({ liexiu_token: "bootstrap-token" });
    expect(api.setToken).toHaveBeenCalledWith("bootstrap-token");
    expect(JSON.stringify(storage.snapshot())).not.toContain("never-persist-this");
  });

  it("keeps cookie-mode bootstrap credentials out of local storage", async () => {
    const storage = makeStorage();
    const api = {
      bootstrap: vi.fn().mockResolvedValue(response),
      setToken: vi.fn(),
    } as unknown as ApiClient;
    const store = createAuthStore({ api, storage, cookieAuth: true });

    await store.getState().bootstrap({
      secret: "never-persist-this",
      owner_name: "Owner",
      owner_email: "owner@example.com",
      workspace_name: "Local",
      workspace_slug: "local",
      workspace_id: "",
    });

    expect(storage.snapshot()).toEqual({});
    expect(api.setToken).not.toHaveBeenCalled();
    expect(store.getState().user).toEqual(fakeUser);
  });
});
