import { useState } from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type {
  AssignmentProjection,
  MissionProjection,
  RunDetailProjection,
  RunProjection,
  TaskNodeProjection,
} from "@liexiu/core/orchestration";
import { renderWithI18n } from "../test/i18n";
import { MissionWorkspace } from "./mission-page";

const previousRun = makeRun("run-0", "node-1", "assignment-1", "succeeded");
const firstRun = { ...makeRun("run-1", "node-1", "assignment-1", "running"), attempt: 2 };
const secondRun = makeRun("run-2", "node-2", "assignment-2", "succeeded");
const firstAssignment = makeAssignment("assignment-1", "node-1", "executor", "agent-1");
const secondAssignment = makeAssignment("assignment-2", "node-2", "reviewer", "agent-2");
const firstNode = makeNode("node-1", "PLAN", "Plan delivery", "running", firstRun, firstAssignment);
const secondNode = makeNode("node-2", "REVIEW", "Review delivery", "review", secondRun, secondAssignment);

const projection: MissionProjection = {
  mission: {
    id: "mission-1",
    title: "Ship the visual workspace",
    description: "One projection, three coordinated views.",
    status: "running",
    currentPhase: "execution",
    progress: { completed: 0, total: 2, percent: 0 },
    limits: { maxParallelRuns: 2, maxTaskAttempts: 3, maxReworkCycles: 2 },
    budget: {
      status: "ok",
      consumedTokens: 120,
      reservedTokens: 40,
      consumedCostUsdTicks: 300,
      reservedCostUsdTicks: 100,
      grantTokens: 0,
      grantCostUsdTicks: 0,
    },
    revision: 3,
    lastSequence: 7,
    createdAt: "2026-08-17T00:00:00Z",
    updatedAt: "2026-08-17T00:01:00Z",
  },
  nodes: [firstNode, secondNode],
  team: [
    {
      agentId: "agent-1",
      agentName: "Planner Fox",
      role: "executor",
      runtimeId: "runtime-1",
      runtimeName: "Local Claude",
      runtimeStatus: "online",
      provider: "anthropic",
      model: "claude-test",
      capabilities: {},
      currentNodeIds: ["node-1"],
    },
  ],
  activities: {
    items: [
      {
        id: "activity-1",
        taskNodeId: "node-1",
        runId: "run-1",
        type: "run.started",
        actorType: "agent",
        actorId: "agent-1",
        subjectType: "run",
        subjectId: "run-1",
        causationId: "cause-1",
        correlationId: "mission-1",
        payloadVersion: 1,
        payload: {},
        sequence: 7,
        occurredAt: "2026-08-17T00:01:00Z",
      },
    ],
    firstSequence: 7,
    lastSequence: 7,
    hasPrevious: false,
  },
};

const details = new Map<string, RunDetailProjection>([
  ["run-1", { ...makeDetail(firstNode, firstRun, firstAssignment), lineage: { assignments: [firstAssignment], runs: [previousRun, firstRun] } }],
  ["run-0", { ...makeDetail(firstNode, previousRun, firstAssignment), lineage: { assignments: [firstAssignment], runs: [previousRun, firstRun] } }],
  ["run-2", makeDetail(secondNode, secondRun, secondAssignment)],
]);

function Harness() {
  const [selectedRunId, setSelectedRunId] = useState("run-1");
  return (
    <MissionWorkspace
      projection={projection}
      selectedRunId={selectedRunId}
      onSelectRun={setSelectedRunId}
      detail={details.get(selectedRunId)}
    />
  );
}

describe("MissionWorkspace", () => {
  it("renders all three views from one projection and changes Run details from the board", async () => {
    const user = userEvent.setup();
    renderWithI18n(<Harness />);

    expect(screen.getByText("Project board")).toBeInTheDocument();
    expect(screen.getByText("Agent world")).toBeInTheDocument();
    expect(screen.getByText("Run details")).toBeInTheDocument();
    expect(screen.getByText("Dependency graph")).toBeInTheDocument();
    expect(screen.getByText("Planner Fox")).toBeInTheDocument();
    expect(screen.getAllByText(/Plan delivery/).length).toBeGreaterThan(0);
    expect(screen.getByText("PLAN · Plan delivery")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /REVIEW Review delivery/ }));

    expect(screen.getByText("REVIEW · Review delivery")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /REVIEW Review delivery/ })).toHaveAttribute("aria-pressed", "true");
  });

  it("navigates historical attempts from Run lineage without changing board truth", async () => {
    const user = userEvent.setup();
    renderWithI18n(<Harness />);

    await user.click(screen.getByRole("button", { name: "#1 · succeeded" }));

    expect(screen.getByText("Attempt 1")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "#1 · succeeded" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: /PLAN Plan delivery/ })).toHaveAttribute("aria-pressed", "false");
  });

  it("keeps the Chinese board, world, and detail vocabulary aligned", () => {
    renderWithI18n(<Harness />, { locale: "zh-Hans" });

    expect(screen.getByText("项目总看板")).toBeInTheDocument();
    expect(screen.getByText("智能体像素世界")).toBeInTheDocument();
    expect(screen.getByText("执行详情")).toBeInTheDocument();
    expect(screen.getByText("执行工坊")).toBeInTheDocument();
  });

  it("lets the owner submit a budget approval command when the gate requires it", async () => {
    const user = userEvent.setup();
    const onApproveBudget = vi.fn().mockResolvedValue(undefined);
    renderWithI18n(
      <MissionWorkspace
        projection={{
          ...projection,
          mission: {
            ...projection.mission,
            budget: {
              ...projection.mission.budget,
              status: "approval_required",
              maxTokens: 1000,
              maxCostUsdTicks: 2500,
            },
          },
        }}
        selectedRunId=""
        onSelectRun={() => undefined}
        onApproveBudget={onApproveBudget}
      />,
    );

    await user.clear(screen.getByLabelText("Grant tokens"));
    await user.type(screen.getByLabelText("Grant tokens"), "500");
    await user.clear(screen.getByLabelText("Grant cost (USD ticks)"));
    await user.type(screen.getByLabelText("Grant cost (USD ticks)"), "1250");
    await user.type(screen.getByLabelText("Reason"), "Owner approved the allowance");
    await user.click(screen.getByRole("button", { name: "Approve budget" }));

    await waitFor(() => expect(onApproveBudget).toHaveBeenCalledWith(expect.objectContaining({
      expectedRevision: 3,
      grantTokens: 500,
      grantCostUsdTicks: 1250,
      reason: "Owner approved the allowance",
      commandId: expect.any(String),
    })));
  });

  it("exposes explicit owner start and cancel controls for a ready mission", async () => {
    const user = userEvent.setup();
    const onStart = vi.fn().mockResolvedValue(undefined);
    const onCancel = vi.fn().mockResolvedValue(undefined);
    renderWithI18n(
      <MissionWorkspace
        projection={{ ...projection, mission: { ...projection.mission, status: "ready" } }}
        selectedRunId=""
        onSelectRun={() => undefined}
        onStart={onStart}
        onCancel={onCancel}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Start mission" }));
    await user.click(screen.getByRole("button", { name: "Cancel mission" }));

    expect(onStart).toHaveBeenCalledOnce();
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("retries a failed TaskNode from its Run evidence panel", async () => {
    const user = userEvent.setup();
    const failedNode = { ...firstNode, status: "failed" as const };
    const failedDetail = { ...makeDetail(failedNode, firstRun, firstAssignment), node: failedNode };
    const onRetryTask = vi.fn().mockResolvedValue(undefined);
    renderWithI18n(
      <MissionWorkspace
        projection={{ ...projection, nodes: [failedNode, secondNode] }}
        selectedRunId={firstRun.id}
        onSelectRun={() => undefined}
        detail={failedDetail}
        onRetryTask={onRetryTask}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Retry task" }));
    expect(onRetryTask).toHaveBeenCalledWith(failedNode);
  });
});

function makeRun(
  id: string,
  taskNodeId: string,
  assignmentId: string,
  status: RunProjection["status"],
): RunProjection {
  return {
    id,
    taskNodeId,
    assignmentId,
    purpose: "execute",
    attempt: 1,
    status,
    input: {},
    dispatchDeadlineAt: "2026-08-17T00:05:00Z",
    timeoutSeconds: 300,
    createdAt: "2026-08-17T00:00:00Z",
  };
}

function makeAssignment(
  id: string,
  taskNodeId: string,
  role: AssignmentProjection["role"],
  agentId: string,
): AssignmentProjection {
  return {
    id,
    taskNodeId,
    role,
    agentId,
    runtimeId: `runtime-${agentId}`,
    status: "active",
    sequence: 1,
    createdAt: "2026-08-17T00:00:00Z",
  };
}

function makeNode(
  id: string,
  key: string,
  title: string,
  status: TaskNodeProjection["status"],
  latestRun: RunProjection,
  activeAssignment: AssignmentProjection,
): TaskNodeProjection {
  return {
    id,
    key,
    title,
    description: `${title} description`,
    role: activeAssignment.role,
    status,
    dependencyIds: [],
    acceptanceCriteria: ["evidence exists"],
    artifactKinds: ["report"],
    reworkCount: 0,
    revision: 1,
    latestRun,
    activeAssignment,
  };
}

function makeDetail(
  node: TaskNodeProjection,
  run: RunProjection,
  assignment: AssignmentProjection,
): RunDetailProjection {
  return {
    missionId: "mission-1",
    node,
    run,
    assignment,
    execution: {
      agentTaskId: `task-${run.id}`,
      status: run.status,
      createdAt: run.createdAt,
    },
    messages: [],
    usage: [],
    artifacts: [],
    reviews: [],
    lineage: { assignments: [assignment], runs: [run] },
  };
}
