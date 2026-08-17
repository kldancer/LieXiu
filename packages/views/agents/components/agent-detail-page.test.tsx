// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Agent } from "@liexiu/core/types";
import { I18nProvider } from "@liexiu/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import {
  NavigationProvider,
  type NavigationAdapter,
} from "../../navigation";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

// These tests exercise the retained assignment and archive actions; the tabbed
// body and avatar/presence widgets are irrelevant weight, so they're stubbed.
vi.mock("./agent-overview-pane", () => ({
  AgentOverviewPane: () => <div>agent-overview-pane</div>,
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div>actor-avatar</div>,
}));
vi.mock("./agent-presence-indicator", () => ({
  AgentPresenceIndicator: () => null,
}));

const agentsRef = vi.hoisted(() => ({ current: [] as unknown[] }));
const membersRef = vi.hoisted(() => ({ current: [] as unknown[] }));
const currentUserRef = vi.hoisted(() => ({
  current: { id: "user-1" } as { id: string } | null,
}));
const mockToastError = vi.hoisted(() => vi.fn());
const mockModalOpen = vi.hoisted(() => vi.fn());

vi.mock("@liexiu/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));
vi.mock("@liexiu/core/agents", () => ({
  isAgentRuntimeBound: (agent: { runtime_id: string; runtime_bound?: boolean }) =>
    agent.runtime_bound !== false && agent.runtime_id.length > 0,
  useWorkspacePresenceMap: () => ({ byAgent: new Map() }),
}));
vi.mock("@liexiu/core/workspace/queries", () => ({
  agentListOptions: (wsId: string) => ({
    queryKey: ["agents", wsId],
    queryFn: () => Promise.resolve(agentsRef.current),
  }),
  memberListOptions: (wsId: string) => ({
    queryKey: ["members", wsId],
    queryFn: () => Promise.resolve(membersRef.current),
  }),
  workspaceKeys: { agents: (wsId: string) => ["agents", wsId] },
}));
vi.mock("@liexiu/core/runtimes", () => ({
  runtimeListOptions: (wsId: string) => ({
    queryKey: ["runtimes", wsId],
    queryFn: () => Promise.resolve([]),
  }),
}));
vi.mock("@liexiu/core/auth", () => {
  type AuthState = { user: { id: string } | null };
  const state = (): AuthState => ({ user: currentUserRef.current });
  const useAuthStore = Object.assign(
    (selector?: (s: AuthState) => unknown) =>
      selector ? selector(state()) : state(),
    { getState: state },
  );
  return { useAuthStore };
});
vi.mock("@liexiu/core/modals", () => ({
  useModalStore: Object.assign(vi.fn(), {
    getState: () => ({ open: mockModalOpen }),
  }),
}));
vi.mock("@liexiu/core/paths", () => ({
  useWorkspacePaths: () => ({
    agents: () => "/acme/agents",
  }),
}));
vi.mock("@liexiu/core/api", () => {
  class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  }
  return {
    api: { getAgent: vi.fn(() => Promise.reject(new ApiError(404, "not found"))) },
    ApiError,
  };
});
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: mockToastError },
}));

import { AgentDetailPage } from "./agent-detail-page";

const baseAgent: Agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Lambda",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: null }],
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-2",
  skills: [],
  created_at: "2026-05-28T00:00:00Z",
  updated_at: "2026-05-28T00:00:00Z",
  archived_at: null,
  archived_by: null,
};

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const push = vi.fn();
  const navigation: NavigationAdapter = {
    push,
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/agents/agent-1",
    searchParams: new URLSearchParams(),
    getShareableUrl: (path) => path,
  };
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <NavigationProvider value={navigation}>
        <QueryClientProvider client={queryClient}>
          <AgentDetailPage agentId="agent-1" />
        </QueryClientProvider>
      </NavigationProvider>
    </I18nProvider>,
  );
  return { push };
}

beforeEach(() => {
  vi.clearAllMocks();
  currentUserRef.current = { id: "user-1" };
  membersRef.current = [{ user_id: "user-1", role: "member" }];
  agentsRef.current = [baseAgent];
});

describe("AgentDetailPage actions", () => {
  it("keeps the more-actions trigger for an editable non-system agent", async () => {
    // Positive counterpart: an owner of a normal agent has a real archive
    // action, so the menu trigger must still render. Guards the gate against
    // over-hiding.
    agentsRef.current = [{ ...baseAgent, owner_id: "user-1" }];
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    renderPage();

    await screen.findByRole("button", { name: "Assign work" });
    expect(
      screen.getByLabelText("Agent actions"),
    ).toBeInTheDocument();
  });

  it("explains an unbound agent and blocks run actions without losing the profile", async () => {
    agentsRef.current = [
      {
        ...baseAgent,
        owner_id: "user-1",
        runtime_id: "",
        runtime_bound: false,
      },
    ];
    renderPage();

    expect(
      await screen.findByText(/needs a runtime before it can run/i),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Bind runtime" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Assign work" }));
    expect(mockToastError).toHaveBeenCalledWith(
      "Bind a runtime before running this agent.",
    );
    expect(mockModalOpen).not.toHaveBeenCalled();
  });
});
