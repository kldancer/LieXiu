"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";
import { registerDraftCleanup } from "../../drafts/cleanup-registry";

// The in-progress agent prompt no longer lives here — it moved into the
// unified issue-create draft's `agent` slot (draft-store) so it shares one
// lifecycle with the manual draft (MUL-5181). This store keeps only the
// shared keep-open toggle.
interface QuickCreateState {
  keepOpen: boolean;
  setKeepOpen: (v: boolean) => void;
}

export const useQuickCreateStore = create<QuickCreateState>()(
  persist(
    (set) => ({
      keepOpen: false,
      setKeepOpen: (v) => set({ keepOpen: v }),
    }),
    {
      name: "liexiu_quick_create",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
    },
  ),
);

registerForWorkspaceRehydration(() => useQuickCreateStore.persist.rehydrate());

registerDraftCleanup({
  storageKey: "liexiu_quick_create",
  workspaceScoped: true,
  resetInMemory: () => useQuickCreateStore.setState({ keepOpen: false }),
});
