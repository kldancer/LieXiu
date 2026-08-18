import { describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

describe("ApiClient retained task and bootstrap APIs", () => {
  it("posts the local bootstrap contract", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({
        token: "token",
        user: { id: "user-1" },
        workspace: { id: "workspace-1" },
      }), { status: 200, headers: { "content-type": "application/json" } }),
    );
    const client = new ApiClient("https://api.example.test");

    await expect(client.bootstrap({
      secret: "secret",
      owner_name: "Owner",
      owner_email: "owner@example.test",
      workspace_name: "Workspace",
      workspace_slug: "workspace",
      workspace_id: "workspace-1",
    })).resolves.toMatchObject({
      token: "token",
      user: { id: "user-1" },
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/bootstrap",
      expect.objectContaining({ method: "POST" }),
    );
    fetchMock.mockRestore();
  });

  it("establishes a localhost personal session without returning a token", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({
        user: { id: "owner-1" },
        workspace: { id: "workspace-1", slug: "liexiu" },
        provisioned: false,
      }), { status: 200, headers: { "content-type": "application/json" } }),
    );
    const client = new ApiClient("https://api.example.test");

    await expect(client.startLocalSession()).resolves.toMatchObject({
      user: { id: "owner-1" },
      workspace: { id: "workspace-1" },
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/auth/local-session",
      expect.objectContaining({ method: "POST" }),
    );
    fetchMock.mockRestore();
  });

  it("lists task transcript messages without restoring a Chat API", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify([{ task_id: "task-1", issue_id: "issue-1", seq: 1, type: "text" }]), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    const client = new ApiClient("https://api.example.test");

    await expect(client.listTaskMessages("task-1")).resolves.toHaveLength(1);
    vi.restoreAllMocks();
  });
});
