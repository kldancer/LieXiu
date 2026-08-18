import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../api/client";

afterEach(() => vi.unstubAllGlobals());

const emptyProjectionWire = {
  project: {
    id: "project-1",
    title: "Project",
    status: "in_progress",
    updated_at: "2026-08-19T00:00:00Z",
  },
  generated_at: "2026-08-19T00:00:01Z",
  truncated: false,
  missions: [],
  attention: [],
  capacity: { agents: [], runtimes: [] },
  totals: {
    mission_count: 0,
    active_missions: 0,
    blocked_missions: 0,
    completed_missions: 0,
    attention_count: 0,
    active_runs: 0,
    queued_runs: 0,
    offline_agents: 0,
    pending_human_gates: 0,
    pending_reviews: 0,
    consumed_tokens: 0,
    reserved_tokens: 0,
    consumed_cost_usd_ticks: 0,
    reserved_cost_usd_ticks: 0,
  },
};

describe("ApiClient Project Command Center", () => {
  it("loads the bounded projection through the project-scoped endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(emptyProjectionWire), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      new ApiClient("https://api.example.test").getProjectCommandCenter(
        "project/1",
      ),
    ).resolves.toMatchObject({
      project: { id: "project-1" },
      missions: [],
      attention: [],
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/projects/project%2F1/command-center",
      expect.any(Object),
    );
  });

  it("fails closed when the server projection drifts", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ ...emptyProjectionWire, payload: "secret" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    await expect(
      new ApiClient("https://api.example.test").getProjectCommandCenter(
        "project-1",
      ),
    ).rejects.toThrow("Invalid Project Command Center response");
  });
});
