import type { StorageAdapter } from "../types/storage";

function browserStorage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    const storage = window.localStorage;
    return storage &&
      typeof storage.getItem === "function" &&
      typeof storage.setItem === "function" &&
      typeof storage.removeItem === "function"
      ? storage
      : null;
  } catch {
    // Sandboxed documents and privacy policies may expose window while
    // denying access to localStorage. Persistence must remain best-effort.
    return null;
  }
}

/** SSR- and sandbox-safe localStorage for Next.js and Electron renderers. */
export const defaultStorage: StorageAdapter = {
  getItem: (k) => browserStorage()?.getItem(k) ?? null,
  setItem: (k, v) => {
    browserStorage()?.setItem(k, v);
  },
  removeItem: (k) => {
    browserStorage()?.removeItem(k);
  },
};
