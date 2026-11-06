import { create } from "zustand";

import { readJSONFlag, writeJSONFlag } from "@/lib/localStorageFlag";
import { scopePrefix } from "@/lib/scope";

// Per workspace pane: panes share the origin's localStorage, and a recent
// valid in one project is dropped as "not found" in another.
const STORAGE_KEY = "iterion.recents" + scopePrefix();
const MAX_RECENTS = 10;

function readRecents(): string[] {
  const parsed = readJSONFlag<unknown>(STORAGE_KEY, []);
  if (!Array.isArray(parsed)) return [];
  return parsed.filter((s): s is string => typeof s === "string").slice(0, MAX_RECENTS);
}

function writeRecents(list: string[]) {
  writeJSONFlag(STORAGE_KEY, list);
}

interface RecentsState {
  recents: string[];
  pushRecent: (path: string) => void;
  removeRecent: (path: string) => void;
  clearRecents: () => void;
}

export const useRecentsStore = create<RecentsState>((set) => ({
  recents: readRecents(),
  pushRecent: (path) =>
    set((s) => {
      const filtered = s.recents.filter((p) => p !== path);
      const next = [path, ...filtered].slice(0, MAX_RECENTS);
      writeRecents(next);
      return { recents: next };
    }),
  removeRecent: (path) =>
    set((s) => {
      const next = s.recents.filter((p) => p !== path);
      writeRecents(next);
      return { recents: next };
    }),
  clearRecents: () => {
    writeRecents([]);
    set({ recents: [] });
  },
}));
