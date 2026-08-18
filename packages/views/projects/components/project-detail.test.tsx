import React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Project } from "@liexiu/core/types";
import type { ProjectCommandCenterProjection } from "@liexiu/core/orchestration";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { ProjectDetail } from "./project-detail";

const mocks = vi.hoisted(() => ({
  role: "admin",
  deleteProject: vi.fn(),
  push: vi.fn(),
  replace: vi.fn(),
  search: "",
  toastSuccess: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: readonly unknown[] }) => {
    switch (options.queryKey?.[0]) {
      case "project-detail":
        return { data: PROJECT, isLoading: false };
      case "members":
        return {
          data: [{ user_id: "user-1", name: "User One", role: mocks.role }],
          isLoading: false,
        };
      case "agents":
        return { data: [], isLoading: false };
      case "project-command-center":
        return {
          data: COMMAND_PROJECTION,
          isLoading: false,
          isError: false,
          isFetching: false,
          refetch: vi.fn(),
        };
      default:
        return { data: undefined, isLoading: false };
    }
  },
}));

vi.mock("@liexiu/core/projects/queries", () => ({
  projectDetailOptions: () => ({ queryKey: ["project-detail"] }),
  projectCommandCenterOptions: () => ({ queryKey: ["project-command-center"] }),
}));

vi.mock("@liexiu/core/projects/mutations", () => ({
  useUpdateProject: () => ({ mutate: vi.fn() }),
  useDeleteProject: () => ({ mutate: mocks.deleteProject }),
}));


vi.mock("@liexiu/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
  agentListOptions: () => ({ queryKey: ["agents"] }),
}));

vi.mock("@liexiu/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@liexiu/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

vi.mock("@liexiu/core/paths", () => ({
  useWorkspacePaths: () => ({
    projects: () => "/test-workspace/projects",
    projectDetail: (id: string) => `/test-workspace/projects/${id}`,
    missionDetail: (id: string) => `/test-workspace/missions/${id}`,
  }),
}));

vi.mock("@liexiu/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "User One" }),
}));

vi.mock("sonner", () => ({
  toast: { success: mocks.toastSuccess },
}));

vi.mock("react-resizable-panels", () => ({
  useDefaultLayout: () => ({
    defaultLayout: undefined,
    onLayoutChanged: vi.fn(),
  }),
  usePanelRef: () => ({
    current: {
      isCollapsed: () => false,
      expand: vi.fn(),
      collapse: vi.fn(),
    },
  }),
}));

vi.mock("@liexiu/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@liexiu/ui/components/common/emoji-picker", () => ({
  EmojiPicker: () => null,
}));

vi.mock("@liexiu/ui/components/ui/resizable", () => ({
  ResizablePanelGroup: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  ResizablePanel: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  ResizableHandle: () => null,
}));

vi.mock("@liexiu/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
  }) => (
    <button type="button" onClick={onClick}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}));

vi.mock("@liexiu/ui/components/ui/popover", () => ({
  Popover: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  PopoverTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
  PopoverContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

vi.mock("@liexiu/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

vi.mock("@liexiu/ui/components/ui/sheet", () => ({
  Sheet: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SheetContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

vi.mock("@liexiu/ui/components/ui/alert-dialog", () => ({
  AlertDialog: ({
    open,
    children,
  }: {
    open: boolean;
    children: React.ReactNode;
  }) => (open ? <div role="alertdialog">{children}</div> : null),
  AlertDialogContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  AlertDialogHeader: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  AlertDialogTitle: ({ children }: { children: React.ReactNode }) => (
    <h2>{children}</h2>
  ),
  AlertDialogDescription: ({ children }: { children: React.ReactNode }) => (
    <p>{children}</p>
  ),
  AlertDialogFooter: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  AlertDialogCancel: ({ children }: { children: React.ReactNode }) => (
    <button type="button">{children}</button>
  ),
  AlertDialogAction: ({
    children,
    onClick,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
  }) => (
    <button type="button" onClick={onClick}>
      {children}
    </button>
  ),
}));

vi.mock("../../editor", () => ({
  TitleEditor: ({ defaultValue }: { defaultValue: string }) => (
    <div>{defaultValue}</div>
  ),
  ContentEditor: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

vi.mock("../../issues/components/priority-icon", () => ({
  PriorityIcon: () => null,
}));

vi.mock("./project-resources-section", () => ({
  ProjectResourcesSection: () => null,
}));

vi.mock("./project-start-date-picker", () => ({
  ProjectStartDatePicker: () => null,
}));

vi.mock("./project-due-date-picker", () => ({
  ProjectDueDatePicker: () => null,
}));

vi.mock("../../issues/surface/issue-surface", () => ({
  IssueSurface: () => null,
}));

vi.mock("../../layout/breadcrumb-header", () => ({
  BreadcrumbHeader: ({ actions }: { actions: React.ReactNode }) => (
    <header>{actions}</header>
  ),
}));

vi.mock("../../layout/animated-right-sidebar", () => ({
  AnimatedRightSidebar: ({ children }: { children: React.ReactNode }) => (
    <aside>{children}</aside>
  ),
  getAnimatedRightSidebarInitialOpen: () => true,
  rightSidebarPanelMotionProps: {},
  useAnimatedRightSidebarState: () => ({
    open: true,
    visualOpen: true,
    motionEnabled: false,
    beginToggle: vi.fn(),
    handleResize: vi.fn(),
  }),
}));

const PROJECT: Project = {
  id: "project-1",
  workspace_id: "workspace-1",
  title: "Launch Plan",
  description: null,
  icon: null,
  status: "in_progress",
  priority: "high",
  lead_type: null,
  lead_id: null,
  start_date: null,
  due_date: null,
  created_at: "2026-06-01T00:00:00Z",
  updated_at: "2026-06-01T00:00:00Z",
  issue_count: 3,
  done_count: 1,
  resource_count: 0,
};

const COMMAND_PROJECTION: ProjectCommandCenterProjection = {
  project: {
    id: PROJECT.id,
    title: PROJECT.title,
    status: "in_progress",
    updatedAt: PROJECT.updated_at,
  },
  generatedAt: PROJECT.updated_at,
  truncated: false,
  missions: [{
    id: "mission-1",
    title: "Mission one",
    status: "running",
    currentPhase: "executing",
    progress: { completed: 1, total: 2, percent: 50 },
    budget: {
      status: "ok",
      consumedTokens: 10,
      reservedTokens: 5,
      consumedCostUsdTicks: 1,
      reservedCostUsdTicks: 1,
      grantTokens: 0,
      grantCostUsdTicks: 0,
    },
    revision: 2,
    lastSequence: 3,
    updatedAt: PROJECT.updated_at,
    pendingHumanGates: 0,
    pendingReviews: 0,
    pendingPlanProposals: 0,
    offlineAgents: 0,
    activeRuns: 1,
    queuedRuns: 0,
  }],
  attention: [],
  capacity: { agents: [], runtimes: [] },
  totals: {
    missionCount: 1,
    activeMissions: 1,
    blockedMissions: 0,
    completedMissions: 0,
    attentionCount: 0,
    activeRuns: 1,
    queuedRuns: 0,
    offlineAgents: 0,
    pendingHumanGates: 0,
    pendingReviews: 0,
    consumedTokens: 10,
    reservedTokens: 5,
    consumedCostUsdTicks: 1,
    reservedCostUsdTicks: 1,
  },
};

function renderProjectDetail() {
  const adapter: NavigationAdapter = {
    push: mocks.push,
    replace: mocks.replace,
    back: vi.fn(),
    pathname: "/test-workspace/projects/project-1",
    searchParams: new URLSearchParams(mocks.search),
    getShareableUrl: (path) => path,
  };

  renderWithI18n(
    <NavigationProvider value={adapter}>
      <ProjectDetail projectId={PROJECT.id} />
    </NavigationProvider>,
  );
}

beforeEach(() => {
  mocks.role = "admin";
  mocks.deleteProject.mockReset();
  mocks.push.mockReset();
  mocks.replace.mockReset();
  mocks.search = "";
  mocks.toastSuccess.mockReset();
});

describe("ProjectDetail Command Center navigation", () => {
  it("switches to the URL-addressable project command view", async () => {
    renderProjectDetail();

    await userEvent.click(screen.getByRole("button", { name: "Command center" }));

    expect(mocks.replace).toHaveBeenCalledWith(
      "/test-workspace/projects/project-1?view=command-center",
    );
  });

  it("drills from a project Mission back to the canonical Mission workspace", async () => {
    mocks.search = "view=command-center";
    renderProjectDetail();

    await userEvent.click(screen.getByRole("button", { name: /Mission one/ }));

    expect(mocks.push).toHaveBeenCalledWith(
      "/test-workspace/missions/mission-1?project=project-1&projectView=command-center&view=world",
    );
  });
});

describe("ProjectDetail project deletion", () => {
  it("requires confirmation and navigates only after deletion succeeds", async () => {
    const user = userEvent.setup();
    renderProjectDetail();

    await user.click(screen.getByRole("button", { name: "Delete project" }));

    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(mocks.deleteProject).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(mocks.deleteProject).toHaveBeenCalledWith(
      PROJECT.id,
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    expect(mocks.push).not.toHaveBeenCalled();

    const options = mocks.deleteProject.mock.calls[0]?.[1] as {
      onSuccess: () => void;
    };
    options.onSuccess();

    expect(mocks.toastSuccess).toHaveBeenCalledWith("Project deleted");
    expect(mocks.push).toHaveBeenCalledWith("/test-workspace/projects");
  });

  it("does not offer project deletion to regular members", () => {
    mocks.role = "member";

    renderProjectDetail();

    expect(
      screen.queryByRole("button", { name: "Delete project" }),
    ).not.toBeInTheDocument();
  });
});
