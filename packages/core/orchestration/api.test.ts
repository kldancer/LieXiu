import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../api/client";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("ApiClient mission budget approval", () => {
  it("posts the command contract and parses the command receipt", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        mission_id: "mission-1",
        status: "in_progress",
        revision: 8,
        created_run_ids: ["run-3"],
        replayed: false,
      }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      new ApiClient("https://api.example.test").approveMissionBudget("mission-1", {
        commandId: "019ec09d-6222-722b-bdfa-427b105d80be",
        expectedRevision: 7,
        grantTokens: 500,
        grantCostUsdTicks: 1250,
        reason: "Owner approved the additional execution allowance",
      }),
    ).resolves.toEqual({
      missionId: "mission-1",
      status: "in_progress",
      revision: 8,
      createdRunIds: ["run-3"],
      replayed: false,
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/missions/mission-1/budget/approve",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          command_id: "019ec09d-6222-722b-bdfa-427b105d80be",
          expected_revision: 7,
          grant_tokens: 500,
          grant_cost_usd_ticks: 1250,
          reason: "Owner approved the additional execution allowance",
        }),
      }),
    );
  });
});

describe("ApiClient Human Gate resolution", () => {
  it("posts only the conservative retry resolution and parses revisions", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      mission_id: "mission-1", task_node_id: "node-1", gate_id: "gate-1", status: "resolved",
      revision: 9, task_revision: 4, gate_revision: 2, created_run_ids: ["run-4"], replayed: false,
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(new ApiClient("https://api.example.test").resolveHumanGate("mission-1", "gate-1", {
      commandId: "command-gate", expectedRevision: 8, expectedTaskRevision: 3,
      expectedGateRevision: 1, resolution: "retry", reason: "Owner confirmed retry",
    })).resolves.toMatchObject({ gateId: "gate-1", taskRevision: 4, gateRevision: 2, createdRunIds: ["run-4"] });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/missions/mission-1/human-gates/gate-1/resolve",
      expect.objectContaining({ method: "POST", body: JSON.stringify({
        command_id: "command-gate", expected_revision: 8, expected_task_revision: 3,
        expected_gate_revision: 1, resolution: "retry", reason: "Owner confirmed retry",
      }) }),
    );
  });
});

describe("ApiClient mission lifecycle commands", () => {
  it("posts explicit start/cancel command receipts", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        mission_id: "mission-1", status: "running", revision: 3,
        affected_run_ids: ["run-1"], replayed: false,
      }), { status: 202, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        mission_id: "mission-1", status: "cancelled", revision: 4,
        affected_run_ids: ["run-1"], replayed: false,
      }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.startMission("mission-1", {
      commandId: "command-start", expectedRevision: 2,
      rolePolicyBindings: [
        { duty: "executor", profileKey: "execute-safe", version: 2 },
        { duty: "reviewer", profileKey: "review-strict", version: 3, agentId: "agent-review" },
        { duty: "integrator", profileKey: "integrate-release", version: 1 },
      ],
    })).resolves.toMatchObject({ status: "running", affectedRunIds: ["run-1"] });
    await expect(client.cancelMission("mission-1", {
      commandId: "command-cancel", expectedRevision: 3, reason: "Owner stopped execution",
    })).resolves.toMatchObject({ status: "cancelled", affectedRunIds: ["run-1"] });

    expect(fetchMock).toHaveBeenNthCalledWith(1,
      "https://api.example.test/api/missions/mission-1/start",
      expect.objectContaining({ method: "POST", body: JSON.stringify({
        command_id: "command-start", expected_revision: 2,
        role_policy_bindings: [
          { duty: "executor", profile_key: "execute-safe", version: 2 },
          { duty: "reviewer", profile_key: "review-strict", version: 3, agent_id: "agent-review" },
          { duty: "integrator", profile_key: "integrate-release", version: 1 },
        ],
      }) }),
    );
  });

  it("posts task and mission revisions for an explicit retry", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      mission_id: "mission-1", task_node_id: "node-1", status: "assigned",
      revision: 6, created_run_ids: ["run-2"], replayed: false,
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(new ApiClient("https://api.example.test").retryMissionTask("mission-1", {
      taskNodeId: "node-1", commandId: "command-retry", expectedRevision: 5,
      expectedTaskRevision: 3, reason: "Owner retry",
    })).resolves.toMatchObject({ taskNodeId: "node-1", createdRunIds: ["run-2"] });

    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/missions/mission-1/tasks/node-1/retry",
      expect.objectContaining({ method: "POST", body: JSON.stringify({
        command_id: "command-retry", expected_revision: 5,
        expected_task_revision: 3, reason: "Owner retry",
      }) }),
    );
  });
});

describe("ApiClient owner planning gate commands", () => {
  it("posts the request, edit, reject, and approve contracts", async () => {
    const response = {
      mission_id: "mission-1",
      status: "draft",
      revision: 8,
      artifact_id: "proposal-2",
      run_id: "plan-run-2",
      replayed: false,
    };
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(
      JSON.stringify(response),
      { status: 202, headers: { "Content-Type": "application/json" } },
    )));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.requestPlan("mission-1", {
      commandId: "command-request",
      expectedRevision: 4,
      rolePolicyBinding: { duty: "planner", profileKey: "plan-balanced", version: 4 },
      objective: "Ship the planning gate",
      contextRefs: [{ kind: "issue", id: "issue-1" }],
      deliveryCriteria: ["Owner can approve"],
    })).resolves.toMatchObject({ missionId: "mission-1", runId: "plan-run-2" });
    await client.editPlanProposal("mission-1", "proposal-1", {
      commandId: "command-edit",
      expectedRevision: 5,
      proposal: { objective: "Owner edited" },
    });
    await client.rejectPlanProposal("mission-1", "proposal-2", {
      commandId: "command-reject",
      expectedRevision: 6,
      reason: "Reduce scope",
    });
    await client.approvePlanProposal("mission-1", "proposal-3", {
      commandId: "command-approve",
      expectedRevision: 7,
    });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "https://api.example.test/api/missions/mission-1/plan/request",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          command_id: "command-request",
          expected_revision: 4,
          objective: "Ship the planning gate",
          context_refs: [{ kind: "issue", id: "issue-1" }],
          delivery_criteria: ["Owner can approve"],
          role_policy_binding: { duty: "planner", profile_key: "plan-balanced", version: 4 },
        }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "https://api.example.test/api/missions/mission-1/plan-proposals/proposal-1/edit",
      expect.objectContaining({ body: JSON.stringify({
        command_id: "command-edit",
        expected_revision: 5,
        proposal: { objective: "Owner edited" },
      }) }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "https://api.example.test/api/missions/mission-1/plan-proposals/proposal-2/reject",
      expect.objectContaining({ body: JSON.stringify({
        command_id: "command-reject",
        expected_revision: 6,
        reason: "Reduce scope",
      }) }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      4,
      "https://api.example.test/api/missions/mission-1/plan-proposals/proposal-3/approve",
      expect.objectContaining({ body: JSON.stringify({
        command_id: "command-approve",
        expected_revision: 7,
      }) }),
    );
  });
});
