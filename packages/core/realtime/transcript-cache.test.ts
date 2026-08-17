import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { chatKeys } from "../chat/queries";
import { applyTaskMessageToCache } from "./use-realtime-sync";
import type { TaskMessagePayload } from "../types/events";

const message = (seq: number, content: string): TaskMessagePayload => ({
  task_id: "11111111-1111-4111-8111-111111111111",
  issue_id: "22222222-2222-4222-8222-222222222222",
  seq,
  type: "text",
  content,
});

describe("task transcript realtime cache", () => {
  it("merges streamed messages by sequence without duplicate rows", () => {
    const qc = new QueryClient();
    const key = chatKeys.taskMessages(message(1, "first").task_id);
    qc.setQueryData(key, [message(1, "stale")]);

    applyTaskMessageToCache(qc, message(1, "fresh"));
    applyTaskMessageToCache(qc, message(2, "second"));

    expect(qc.getQueryData(key)).toEqual([message(1, "stale"), message(2, "second")]);
  });
});
