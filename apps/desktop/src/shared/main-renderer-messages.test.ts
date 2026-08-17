import { describe, expect, it, vi } from "vitest";
import {
  MainRendererMessageQueue,
  parseMainRendererChannelState,
} from "./main-renderer-messages";

describe("MainRendererMessageQueue", () => {
  it("holds messages until their matching listener is ready", () => {
    const queue = new MainRendererMessageQueue();
    const send = vi.fn();

    queue.enqueue("auth:token", "token-a", send);
    expect(send).not.toHaveBeenCalled();

    queue.setReady("auth:token", true, send);
    expect(send).toHaveBeenCalledWith("auth:token", "token-a");
  });

  it("delivers immediately while a channel is ready", () => {
    const queue = new MainRendererMessageQueue();
    const send = vi.fn();

    queue.setReady("auth:token", true, send);
    queue.enqueue("auth:token", "token-a", send);

    expect(send).toHaveBeenCalledOnce();
  });

  it("keeps queued work across a renderer readiness reset", () => {
    const queue = new MainRendererMessageQueue();
    const send = vi.fn();

    queue.setReady("auth:token", true, send);
    queue.resetReady();
    queue.enqueue("auth:token", "token-b", send);
    expect(send).not.toHaveBeenCalled();

    queue.setReady("auth:token", true, send);
    expect(send).toHaveBeenCalledWith("auth:token", "token-b");
  });

  it("can discard account-scoped pending messages", () => {
    const queue = new MainRendererMessageQueue();
    const send = vi.fn();

    queue.enqueue("auth:token", "old-account-token", send);
    queue.clear("auth:token");
    queue.setReady("auth:token", true, send);

    expect(send).not.toHaveBeenCalled();
  });
});

describe("parseMainRendererChannelState", () => {
  it("accepts only allowlisted channels with an explicit boolean", () => {
    expect(
      parseMainRendererChannelState({ channel: "auth:token", ready: true }),
    ).toEqual({ channel: "auth:token", ready: true });
    expect(
      parseMainRendererChannelState({ channel: "shell:openExternal", ready: true }),
    ).toBeNull();
    expect(
      parseMainRendererChannelState({ channel: "auth:token", ready: "yes" }),
    ).toBeNull();
  });
});
