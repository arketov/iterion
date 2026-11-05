import { errorMessage } from "@/lib/errorHints";
import { isManifestless } from "@/lib/botOrigin";
import { useMemo } from "react";
import { create } from "zustand";

import {
  listBotsWithDiagnostics,
  setBotOverlay,
  updateBot,
  type BotDiscoveryError,
  type BotEntryWithSchema,
  type BotPatch,
} from "@/api/bots";

// Single source of truth for the bot catalog across Home, the board
// BotPicker, the Inspector "Bot" tab, and the Catalog manager. Replaces
// the per-component one-shot fetches so a metadata edit or a catalog
// toggle re-renders every surface.
interface BotsState {
  bots: BotEntryWithSchema[] | null;
  /** Manifest-less bots from the same response: the project's single
   *  .bot files (origin "file"). Kept apart from `bots` on purpose: integrations,
   *  launch forms and routing only deal in catalog bots, while the Bots
   *  page, Home panel and pickers show `bots` + `extraBots` — one list. */
  extraBots: BotEntryWithSchema[];
  loading: boolean;
  error: string | null;
  /** One entry per bundle the registry SKIPPED as malformed — the bot
   *  is absent from `bots`, so the gallery warns instead of letting it
   *  vanish silently. Empty when everything discovered cleanly. */
  discoveryErrors: BotDiscoveryError[];
  /** Fetch once (no-op if already loaded or in flight). */
  fetch: () => Promise<void>;
  /** Force a new request that supersedes any in-flight catalog load. */
  refetch: () => Promise<void>;
  /** Persist manifest metadata, then refresh the cache. Returns the
   *  updated entry; throws on failure (caller toasts). */
  saveBot: (name: string, patch: BotPatch) => Promise<BotEntryWithSchema>;
  /** Toggle workspace catalog visibility via the overlay, then refresh. */
  setOverlay: (name: string, enabled: boolean | null) => Promise<BotEntryWithSchema>;
}

let inflight: Promise<void> | null = null;
// Monotonic request stamp: only the LATEST load may write the store.
// Without it, a slow initial fetch scoped to the previous project can
// resolve after the project-switch refetch and clobber the new catalog
// with stale bots for the rest of the session (fetch() no-ops once
// bots !== null).
let loadSeq = 0;

async function load(set: (partial: Partial<BotsState>) => void): Promise<void> {
  const seq = ++loadSeq;
  set({ loading: true, error: null });
  try {
    const { bots: listed, discoveryErrors } = await listBotsWithDiagnostics();
    if (seq !== loadSeq) return; // superseded (e.g. project switch)
    // The built-in catalogue is not a listed bot (owner decision
    // 2026-11-05): an older server that still sends origin "builtin" entries
    // is filtered here so no screen shows them.
    const bots = listed.filter((b) => (b.origin as string) !== "builtin");
    set({
      bots: bots.filter((b) => !isManifestless(b)),
      extraBots: bots.filter(isManifestless),
      discoveryErrors,
      loading: false,
    });
  } catch (e) {
    if (seq !== loadSeq) return;
    // The failed request may belong to a newly selected project. Do not keep
    // showing the previous project's skipped-bundle diagnostics beside it.
    set({ error: errorMessage(e), discoveryErrors: [], loading: false });
  }
}

export const useBotsStore = create<BotsState>((set, get) => ({
  bots: null,
  extraBots: [],
  loading: false,
  error: null,
  discoveryErrors: [],
  fetch: () => {
    if (get().bots !== null) return Promise.resolve();
    if (inflight) return inflight;
    inflight = load(set).finally(() => {
      inflight = null;
    });
    return inflight;
  },
  refetch: () => load(set),
  saveBot: async (name, patch) => {
    const updated = await updateBot(name, patch);
    set((s) => ({ bots: mergeEntry(s.bots, updated) }));
    return updated;
  },
  setOverlay: async (name, enabled) => {
    const updated = await setBotOverlay(name, enabled);
    set((s) => ({ bots: mergeEntry(s.bots, updated) }));
    return updated;
  },
}));

// mergeEntry replaces the matching entry in place (or appends) so the
// cached list reflects a saved bot without a full refetch.
function mergeEntry(
  bots: BotEntryWithSchema[] | null,
  updated: BotEntryWithSchema,
): BotEntryWithSchema[] {
  if (!bots) return [updated];
  const i = bots.findIndex((b) => b.name === updated.name);
  if (i === -1) return [...bots, updated];
  const next = bots.slice();
  next[i] = updated;
  return next;
}

/** useAllBots is the one list every picker and list screen reads: catalog
 *  bots followed by the manifest-less ones. null until the first load. */
export function useAllBots(): BotEntryWithSchema[] | null {
  const bots = useBotsStore((s) => s.bots);
  const extra = useBotsStore((s) => s.extraBots);
  return useMemo(() => (bots === null ? null : [...bots, ...extra]), [bots, extra]);
}
