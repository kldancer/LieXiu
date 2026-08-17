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
    })).resolves.toMatchObject({ status: "running", affectedRunIds: ["run-1"] });
    await expect(client.cancelMission("mission-1", {
      commandId: "command-cancel", expectedRevision: 3, reason: "Owner stopped execution",
    })).resolves.toMatchObject({ status: "cancelled", affectedRunIds: ["run-1"] });

    expect(fetchMock).toHaveBeenNthCalledWith(1,
      "https://api.example.test/api/missions/mission-1/start",
      expect.objectContaining({ method: "POST", body: JSON.stringify({
        command_id: "command-start", expected_revision: 2, reason: undefined,
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
