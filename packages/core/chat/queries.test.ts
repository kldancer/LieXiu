import { describe, expect, it } from "vitest";
import {
  chatKeys,
  isTaskMessageTaskId,
  mergeTaskMessagesBySeq,
  taskMessagesOptions,
} from "./queries";
import type { TaskMessagePayload } from "../types/events";

const message = (seq: number): TaskMessagePayload => ({
  task_id: "11111111-1111-4111-8111-111111111111",
  issue_id: "22222222-2222-4222-8222-222222222222",
  seq,
  type: "text",
  content: `message-${seq}`,
});

describe("transcript-only chat query compatibility", () => {
  it("keeps the task transcript key stable", () => {
    expect(chatKeys.taskMessages("task-1")).toEqual(["task-messages", "task-1"]);
    expect(taskMessagesOptions(message(1).task_id).enabled).toBe(true);
  });

  it("rejects non-task identifiers", () => {
    expect(isTaskMessageTaskId("not-a-uuid")).toBe(false);
    expect(taskMessagesOptions("not-a-uuid").enabled).toBe(false);
  });

  it("merges transcript events by sequence without duplicate cache rows", () => {
    const existing = [message(1), message(3)];
    expect(mergeTaskMessagesBySeq(existing, [message(2), message(3)])).toEqual([
      message(1),
      message(2),
      message(3),
    ]);
    expect(mergeTaskMessagesBySeq(existing, [])).toBe(existing);
  });
});
