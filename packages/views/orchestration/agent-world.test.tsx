import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { MissionProjection } from "@liexiu/core/orchestration";
import { renderWithI18n } from "../test/i18n";
import { AgentWorld } from "./agent-world";

const renderer = vi.hoisted(() => ({
  mount: vi.fn(async (container: HTMLElement) => {
    container.append(document.createElement("canvas"));
  }),
  update: vi.fn(),
  destroy: vi.fn(),
}));
const rendererOptions = vi.hoisted(() => ({
  current: undefined as {
    onActorClick?: (actorId: string) => void;
    onZoneClick?: (zoneId: "execution_workshop") => void;
    onArtifactClick?: (artifactId: string) => void;
    onSignalClick?: (signalId: string) => void;
  } | undefined,
}));
const createWorldRenderer = vi.hoisted(() => vi.fn(
  (options: typeof rendererOptions.current) => {
    rendererOptions.current = options;
    return renderer;
  },
));

vi.mock("./world/phaser-scene", () => ({ createWorldRenderer }));

describe("AgentWorld Phaser bridge", () => {
  it("loads client-side, diffs canonical inputs, selects by domain ID, and destroys cleanly", async () => {
    const onSelectRun = vi.fn();
    const { rerender, unmount } = renderWithI18n(
      <AgentWorld projection={projection(1)} onSelectRun={onSelectRun} />,
    );

    await waitFor(() => expect(renderer.mount).toHaveBeenCalledOnce());
    expect(screen.getByTestId("phaser-world-host")).toHaveAttribute(
      "data-renderer-state",
      "ready",
    );
    expect(renderer.update).toHaveBeenCalledWith(
      expect.objectContaining({ missionId: "mission-1", revision: 1 }),
      expect.arrayContaining([expect.objectContaining({ sequence: 1 })]),
    );

    rendererOptions.current?.onActorClick?.("agent-1:executor:runtime-1");
    expect(onSelectRun).toHaveBeenCalledWith("run-1");
    rendererOptions.current?.onArtifactClick?.("artifact-1");
    expect(onSelectRun).toHaveBeenLastCalledWith("run-1");
    rendererOptions.current?.onSignalClick?.("review:verdict-1");
    expect(onSelectRun).toHaveBeenLastCalledWith("review-run-1");
    act(() => rendererOptions.current?.onZoneClick?.("execution_workshop"));
    expect(screen.getByTestId("phaser-world-host")).toHaveAttribute("data-world-zone-filter", "execution_workshop");

    rerender(<AgentWorld projection={projection(2)} onSelectRun={onSelectRun} />);
    await waitFor(() => expect(renderer.update).toHaveBeenLastCalledWith(
      expect.objectContaining({ revision: 2 }),
      expect.any(Array),
    ));

    fireEvent.click(screen.getByRole("button", { name: "Pause motion" }));
    expect(screen.getByRole("button", { name: "Resume motion" })).toHaveAttribute("aria-pressed", "true");
    await waitFor(() => expect(renderer.mount).toHaveBeenCalledTimes(2));

    unmount();
    expect(renderer.destroy).toHaveBeenCalledTimes(2);
  });
});

function projection(revision: number): MissionProjection {
  return {
    mission: {
      id: "mission-1",
      title: "Mission",
      description: "",
      status: "running",
      currentPhase: "execution",
      progress: { completed: 0, total: 1, percent: 0 },
      limits: { maxParallelRuns: 1, maxTaskAttempts: 1, maxReworkCycles: 1 },
      budget: {
        status: "ok",
        consumedTokens: 0,
        reservedTokens: 0,
        consumedCostUsdTicks: 0,
        reservedCostUsdTicks: 0,
        grantTokens: 0,
        grantCostUsdTicks: 0,
      },
      revision,
      lastSequence: 1,
      createdAt: "2026-08-19T00:00:00Z",
      updatedAt: "2026-08-19T00:00:00Z",
    },
    nodes: [{
      id: "node-1",
      key: "EXECUTE",
      title: "Execute",
      description: "",
      duty: "executor",
      status: "running",
      dependencyIds: [],
      acceptanceCriteria: [],
      artifactKinds: [],
      reworkCount: 0,
      revision: 1,
      latestRun: {
        id: "run-1",
        taskNodeId: "node-1",
        assignmentId: "assignment-1",
        purpose: "work",
        attempt: 1,
        status: "running",
        input: {},
        dispatchDeadlineAt: "",
        timeoutSeconds: 60,
        createdAt: "",
      },
      latestArtifact: {
        id: "artifact-1",
        taskNodeId: "node-1",
        runId: "run-1",
        kind: "patch",
        version: 1,
        uri: "artifact://1",
        summary: "Patch",
        metadata: {},
        createdAt: "",
      },
      latestVerdict: {
        id: "verdict-1",
        taskNodeId: "node-1",
        reviewRunId: "review-run-1",
        artifactId: "artifact-1",
        decision: "changes_requested",
        evidence: {},
        requestedChanges: [],
        createdAt: "",
      },
    }],
    team: [{
      agentId: "agent-1",
      agentName: "Agent One",
      duty: "executor",
      runtimeId: "runtime-1",
      runtimeName: "Runtime One",
      runtimeStatus: "online",
      provider: "test",
      capabilities: {},
      currentNodeIds: ["node-1"],
    }],
    activities: {
      items: [{
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
        sequence: 1,
        occurredAt: "2026-08-19T00:00:00Z",
      }],
      firstSequence: 1,
      lastSequence: 1,
      hasPrevious: false,
    },
    planning: { assignments: [], runs: [], proposals: [] },
    rolePolicySnapshots: [],
    humanGates: [],
  };
}
