import { beforeEach, describe, expect, it } from "vitest";
import { useQuickCreateStore } from "./quick-create-store";

const RESET_STATE = {
  keepOpen: false,
};

describe("quick create store", () => {
  beforeEach(() => {
    useQuickCreateStore.setState(RESET_STATE);
  });

  it("keeps no actor or project memory", () => {
    expect(useQuickCreateStore.getState()).not.toHaveProperty("lastActorType");
    expect(useQuickCreateStore.getState()).not.toHaveProperty("lastActorId");
    expect(useQuickCreateStore.getState()).not.toHaveProperty("lastProjectId");
    expect(useQuickCreateStore.getState()).not.toHaveProperty("setLastProjectId");
  });
});
