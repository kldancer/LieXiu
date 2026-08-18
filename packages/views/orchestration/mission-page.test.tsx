import { useState } from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type {
  AssignmentProjection,
  MissionProjection,
  RoleProfile,
  RunDetailProjection,
  RunProjection,
  TaskNodeProjection,
} from "@liexiu/core/orchestration";
import type { AgentRuntimeDiagnostic } from "@liexiu/core/agents";
import { renderWithI18n } from "../test/i18n";
import { MissionWorkspace } from "./mission-page";

const previousRun = makeRun("run-0", "node-1", "assignment-1", "succeeded");
const firstRun = { ...makeRun("run-1", "node-1", "assignment-1", "running"), attempt: 2 };
const secondRun = makeRun("run-2", "node-2", "assignment-2", "succeeded");
const firstAssignment = makeAssignment("assignment-1", "node-1", "executor", "agent-1");
const secondAssignment = makeAssignment("assignment-2", "node-2", "reviewer", "agent-2");
const firstNode = makeNode("node-1", "PLAN", "Plan delivery", "running", firstRun, firstAssignment);
const secondNode = makeNode("node-2", "REVIEW", "Review delivery", "review", secondRun, secondAssignment);

const roleProfiles: RoleProfile[] = (["planner", "executor", "reviewer", "integrator"] as const).map((duty, index) => ({
  id: `profile-${duty}`,
  workspaceId: "workspace-1",
  profileKey: `${duty}-policy`,
  version: index + 1,
  duty,
  name: `${duty} policy`,
  description: "Explicit test policy",
  config: {},
  createdAt: "2026-08-17T00:00:00Z",
}));

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
      duty: "executor",
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
  planning: { assignments: [], runs: [], proposals: [] },
  rolePolicySnapshots: [],
  humanGates: [],
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
	it("locates safe mailbox summaries in Command, World, and Inspector", async () => {
		const user = userEvent.setup();
		const onSelectRun = vi.fn();
		const mailboxActivity = {
			id: "mailbox-event-9", taskNodeId: firstNode.id, runId: firstRun.id,
			type: "mailbox.message_expired", actorType: "orchestrator", subjectType: "mailbox_message",
			subjectId: "message-1", causationId: "command-9", correlationId: "command-9",
			payloadVersion: 1, sequence: 9, occurredAt: "2026-08-19T00:00:00Z",
			payload: {
				message_id: "message-1", message_type: "context_request", recipient_type: "agent", recipient_id: "agent-1",
				from_status: "pending", to_status: "expired", expires_at: "2026-08-19T00:00:00Z", hops: 8,
			},
		};
		const { container } = renderWithI18n(
			<MissionWorkspace
				projection={{ ...projection, activities: { ...projection.activities, items: [...projection.activities.items, mailboxActivity], lastSequence: 9 } }}
				selectedRunId={firstRun.id}
				onSelectRun={onSelectRun}
				detail={makeDetail(firstNode, firstRun, firstAssignment)}
			/>,
		);

		expect(screen.getAllByText("Collaboration mailbox")).toHaveLength(3);
		expect(screen.getAllByText("Hop limit reached")).toHaveLength(3);
		expect(container.querySelectorAll('[data-mailbox-message-id="message-1"]').length).toBeGreaterThanOrEqual(4);
		expect(screen.queryByText(/bounded-secret-context/)).not.toBeInTheDocument();
		await user.click(screen.getAllByRole("button", { name: "Locate collaboration activity #9" })[0]!);
		expect(onSelectRun).toHaveBeenCalledWith(firstRun.id);
	});

  it("renders diagnostics loading, error, and empty states", () => {
    const base = { items: [], onRefresh: vi.fn() };
    const { rerender } = renderWithI18n(<MissionWorkspace projection={projection} selectedRunId="" onSelectRun={() => undefined} diagnostics={{ ...base, loading: true, error: false }} />);
    expect(screen.getByText("Loading diagnostics")).toBeInTheDocument();

    rerender(<MissionWorkspace projection={projection} selectedRunId="" onSelectRun={() => undefined} diagnostics={{ ...base, loading: false, error: true }} />);
    expect(screen.getByText("Diagnostics are unavailable")).toBeInTheDocument();

    rerender(<MissionWorkspace projection={projection} selectedRunId="" onSelectRun={() => undefined} diagnostics={{ ...base, loading: false, error: false }} />);
    expect(screen.getByText("No visible agents")).toBeInTheDocument();
  });

  it("renders coarse read-only diagnostics facts without RolePolicy eligibility", () => {
    const item = {
      agent: { id: "agent-diagnostic", name: "Diagnostic Agent" },
      runtime: { id: "runtime-diagnostic", name: "Diagnostic Runtime" },
      runtimeBound: true,
      runtimeOnline: true,
      used: 1,
      limit: 3,
      capabilities: ["shell", "browser"],
      permissionMode: "public_to",
      runtimeVisibility: "public",
      available: true,
    } as AgentRuntimeDiagnostic;
    renderWithI18n(<MissionWorkspace projection={projection} selectedRunId="" onSelectRun={() => undefined} diagnostics={{ items: [item], loading: false, error: false }} />);

    expect(screen.getByText("Diagnostic Agent")).toBeInTheDocument();
    expect(screen.getByText("1 / 3")).toBeInTheDocument();
    expect(screen.getByText("shell, browser")).toBeInTheDocument();
    expect(screen.getByText("public_to")).toBeInTheDocument();
    expect(screen.getByText("public")).toBeInTheDocument();
    expect(screen.getByText(/not RolePolicy eligibility/)).toBeInTheDocument();
  });

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

  it("exposes only the explicit Human Gate retry for a gated task", async () => {
    const user = userEvent.setup();
    const onResolveHumanGate = vi.fn().mockResolvedValue(undefined);
    const onRetryTask = vi.fn().mockResolvedValue(undefined);
    const blockedNode = { ...firstNode, status: "blocked" as const, revision: 8 };
    renderWithI18n(
      <MissionWorkspace
        projection={{
          ...projection,
          nodes: [blockedNode, secondNode],
          humanGates: [{
            id: "gate-1", taskNodeId: blockedNode.id, artifactId: "artifact-1", sourceRunId: firstRun.id,
            kind: "reviewer_unavailable", status: "pending", reason: "Independent reviewer is unavailable",
            context: {}, revision: 2, createdAt: "2026-08-18T00:00:00Z",
          }],
        }}
        selectedRunId={firstRun.id}
        onSelectRun={() => undefined}
        detail={{ ...makeDetail(blockedNode, firstRun, firstAssignment), node: blockedNode }}
        onResolveHumanGate={onResolveHumanGate}
        onRetryTask={onRetryTask}
      />,
    );

    expect(screen.getByText("Owner action required")).toBeInTheDocument();
    expect(screen.getByText("Reviewer unavailable")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Retry task" })).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "Retry task" }));
    await waitFor(() => expect(onResolveHumanGate).toHaveBeenCalledWith(expect.objectContaining({ id: "gate-1", revision: 2 }), "Owner resolved Human Gate with a retry"));
    expect(onRetryTask).not.toHaveBeenCalled();
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
        roleProfiles={roleProfiles}
      />,
    );

    await user.selectOptions(screen.getByLabelText("executor role profile"), "executor-policy:2");
    await user.selectOptions(screen.getByLabelText("reviewer role profile"), "reviewer-policy:3");
    await user.selectOptions(screen.getByLabelText("integrator role profile"), "integrator-policy:4");
    await user.click(screen.getByRole("button", { name: "Start mission" }));
    await user.click(screen.getByRole("button", { name: "Cancel mission" }));

    expect(onStart).toHaveBeenCalledWith([
      { duty: "executor", profileKey: "executor-policy", version: 2 },
      { duty: "reviewer", profileKey: "reviewer-policy", version: 3 },
      { duty: "integrator", profileKey: "integrator-policy", version: 4 },
    ]);
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

  it("requests a new plan from a draft mission without a pending proposal", async () => {
    const user = userEvent.setup();
    const onRequestPlan = vi.fn().mockResolvedValue(undefined);
    renderWithI18n(
      <MissionWorkspace
        projection={{
          ...projection,
          mission: { ...projection.mission, status: "draft" },
          planning: { assignments: [], runs: [], proposals: [] },
        }}
        selectedRunId=""
        onSelectRun={() => undefined}
        onRequestPlan={onRequestPlan}
        roleProfiles={roleProfiles}
      />,
    );

    await user.selectOptions(screen.getByLabelText("planner role profile"), "planner-policy:1");
    await user.type(screen.getByLabelText("Planning objective"), "Deliver the owner planning gate");
    await user.type(screen.getByLabelText("Delivery criteria"), "Immutable proposal{enter}Owner approval");
    await user.click(screen.getByRole("button", { name: "Request plan" }));

    expect(onRequestPlan).toHaveBeenCalledWith(
      "Deliver the owner planning gate",
      ["Immutable proposal", "Owner approval"],
      { duty: "planner", profileKey: "planner-policy", version: 1 },
    );
  });

  it("keeps the deterministic fallback visible after the mission leaves draft", () => {
    renderWithI18n(
      <MissionWorkspace
        projection={{
          ...projection,
          mission: { ...projection.mission, status: "ready" },
          planning: { assignments: [], runs: [], proposals: [], source: "fixed_template" },
        }}
        selectedRunId=""
        onSelectRun={() => undefined}
      />,
    );

    expect(screen.getByText("Fixed fallback template")).toBeInTheDocument();
    expect(screen.getByText(/not AI planning/)).toBeInTheDocument();
  });

  it("compares immutable versions and exposes commands only for the pending proposal", async () => {
    const user = userEvent.setup();
    const onApproveProposal = vi.fn().mockResolvedValue(undefined);
    const firstProposal = {
      id: "proposal-1",
      runId: "plan-run-1",
      version: 1,
      uri: "planner://proposal-1",
      contentHash: "sha256:first",
      summary: "First proposal",
      proposal: { objective: "first" },
      decision: "superseded" as const,
      createdAt: "2026-08-17T00:00:00Z",
    };
    const secondProposal = {
      ...firstProposal,
      id: "proposal-2",
      runId: "plan-run-2",
      version: 2,
      contentHash: "sha256:second",
      proposal: { objective: "second" },
      decision: "pending" as const,
    };
    renderWithI18n(
      <MissionWorkspace
        projection={{
          ...projection,
          mission: { ...projection.mission, status: "draft" },
          planning: { assignments: [], runs: [], proposals: [firstProposal, secondProposal] },
        }}
        selectedRunId=""
        onSelectRun={() => undefined}
        onApproveProposal={onApproveProposal}
      />,
    );

    await user.selectOptions(screen.getByLabelText("Compare with"), "proposal-1");
    expect((screen.getByLabelText("Compared plan proposal") as HTMLTextAreaElement).value).toContain('"first"');
    await user.click(screen.getByRole("button", { name: "Approve plan" }));
    expect(onApproveProposal).toHaveBeenCalledOnce();

    await user.selectOptions(screen.getByLabelText("Proposal version"), "proposal-1");
    expect(screen.queryByRole("button", { name: "Approve plan" })).not.toBeInTheDocument();
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
  duty: AssignmentProjection["duty"],
  agentId: string,
): AssignmentProjection {
  return {
    id,
    taskNodeId,
    duty,
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
    duty: activeAssignment.duty,
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
