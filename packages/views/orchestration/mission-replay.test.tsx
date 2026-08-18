import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { MissionProjection } from "@liexiu/core/orchestration";
import { MissionReplay, type MissionReplayLabels } from "./mission-replay";

const agentWorldProps = vi.hoisted(() => ({ current: undefined as Record<string, unknown> | undefined }));

vi.mock("./agent-world", () => ({
  AgentWorld: (props: Record<string, unknown>) => {
    agentWorldProps.current = props;
    return <div data-testid="agent-world" />;
  },
}));

const labels: MissionReplayLabels = {
  play: "Play", pause: "Pause", sequence: "Sequence", actor: "Actor", task: "Task", run: "Run", all: "All", events: "Events",
  rateLabels: { 0.5: "0.5x", 1: "1x", 2: "2x", 4: "4x" },
};

describe("MissionReplay", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it("plays to the last sequence and pauses", () => {
    vi.useFakeTimers();
    render(<MissionReplay projection={projection()} onSelectRun={vi.fn()} labels={labels} />);
    fireEvent.click(screen.getByRole("button", { name: labels.play }));
    act(() => vi.advanceTimersByTime(2_000));
    expect(screen.getByRole("button", { name: labels.play })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "2" })).toBeInTheDocument();
  });

  it("supports rate, seek, and Actor/Task/Run filters", () => {
    render(<MissionReplay projection={projection()} onSelectRun={vi.fn()} labels={labels} />);
    fireEvent.click(screen.getByRole("button", { name: labels.rateLabels[4] }));
    const slider = screen.getByRole("slider", { name: labels.sequence });
    fireEvent.change(slider, { target: { value: "2" } });
    expect(agentWorldProps.current?.visualEventsOverride).toHaveLength(2);
    fireEvent.change(screen.getByRole("combobox", { name: labels.task }), { target: { value: "task-1" } });
    expect(agentWorldProps.current?.visualEventsOverride).toEqual([expect.objectContaining({ sequence: 1 })]);
    fireEvent.change(screen.getByRole("combobox", { name: labels.task }), { target: { value: "" } });
    fireEvent.change(screen.getByRole("combobox", { name: labels.run }), { target: { value: "run-1" } });
    expect(agentWorldProps.current?.visualEventsOverride).toEqual([expect.objectContaining({ sequence: 2 })]);
    fireEvent.change(screen.getByRole("combobox", { name: labels.actor }), { target: { value: "agent-1" } });
    expect(agentWorldProps.current?.visualEventsOverride).toEqual([expect.objectContaining({ sequence: 2 })]);
  });

  it("only selects a Run when a replay event resolves to one", () => {
    const onSelectRun = vi.fn();
    render(<MissionReplay projection={projection()} onSelectRun={onSelectRun} labels={labels} />);
    fireEvent.change(screen.getByRole("slider", { name: labels.sequence }), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "2" }));
    expect(onSelectRun).toHaveBeenCalledWith("run-1");
  });
});

function projection(): MissionProjection {
  return {
    mission: { id: "mission-1", title: "", description: "", status: "running", currentPhase: "execution", progress: { completed: 0, total: 1, percent: 0 }, limits: { maxParallelRuns: 1, maxTaskAttempts: 1, maxReworkCycles: 1 }, budget: { status: "ok", consumedTokens: 0, reservedTokens: 0, consumedCostUsdTicks: 0, reservedCostUsdTicks: 0, grantTokens: 0, grantCostUsdTicks: 0 }, revision: 1, lastSequence: 2, createdAt: "", updatedAt: "" },
    nodes: [{ id: "task-1", key: "TASK", title: "Task One", description: "", duty: "executor", status: "running", dependencyIds: [], acceptanceCriteria: [], artifactKinds: [], reworkCount: 0, revision: 1, latestRun: { id: "run-1", taskNodeId: "task-1", assignmentId: "assignment-1", purpose: "work", attempt: 1, status: "running", input: {}, dispatchDeadlineAt: "", timeoutSeconds: 1, createdAt: "" } }],
    team: [{ agentId: "agent-1", agentName: "Agent One", duty: "executor", runtimeId: "runtime-1", runtimeName: "Runtime", runtimeStatus: "online", provider: "test", capabilities: {}, currentNodeIds: ["task-1"] }],
    activities: { items: [activity("task.ready", 1, "task_node", "task-1"), activity("run.succeeded", 2)], firstSequence: 1, lastSequence: 2, hasPrevious: false },
    planning: { assignments: [], runs: [{ id: "run-1", taskNodeId: "task-1", assignmentId: "assignment-1", purpose: "work", attempt: 1, status: "running", input: {}, dispatchDeadlineAt: "", timeoutSeconds: 1, createdAt: "" }], proposals: [] },
    rolePolicySnapshots: [], humanGates: [],
  } as MissionProjection;
}

function activity(type: string, sequence: number, subjectType = "run", subjectId = "run-1") {
  return { id: `activity-${sequence}`, taskNodeId: "task-1", runId: "run-1", type, actorType: "agent", actorId: "agent-1", subjectType, subjectId, causationId: "", correlationId: "mission-1", payloadVersion: 1, payload: {}, sequence, occurredAt: "" };
}
