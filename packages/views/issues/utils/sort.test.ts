import { describe, expect, it } from "vitest";
import type { Issue } from "@liexiu/core/types";
import { sortIssues } from "./sort";

function issueWith(id: string, position = 0): Issue {
  return {
    id,
    position,
  } as unknown as Issue;
}

describe("sortIssues", () => {
  it("falls back to position order for the static fields", () => {
    const sorted = sortIssues(
      [issueWith("b", 2), issueWith("a", 1)],
      "position",
      "asc",
    );
    expect(sorted.map((i) => i.id)).toEqual(["a", "b"]);
  });
});
