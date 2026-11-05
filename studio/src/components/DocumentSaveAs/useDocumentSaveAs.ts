import { useCallback, useRef, useState } from "react";

import * as api from "@/api/client";
import { errorMessage } from "@/lib/errorHints";
import { stampEditor, stampHolds, unreachableSourceBuffer, type DocumentStore } from "@/store/document";
import { useRecentsStore } from "@/store/recents";
import { useServerInfoStore } from "@/store/serverInfo";
import { useUIStore } from "@/store/ui";
import { salvageRefusal } from "@/lib/salvage";

export interface DocumentSaveAsResult {
  path: string;
  source: string;
  clean: boolean;
}

export interface DocumentSaveAsRequest {
  store: DocumentStore;
  /** Assistant requests bind Save As to one captured editor generation. */
  expectedGeneration?: number;
  /** Revalidates the editor session/tab at confirmation and after the write. */
  isTargetCurrent?: () => boolean;
  /** Rechecks caller-owned policy or other mutable authorization at confirm. */
  validate?: () => string | null;
  onSaved?: (result: DocumentSaveAsResult) => void;
}

type PendingSaveAs = DocumentSaveAsRequest;

/** The names a new document goes by: what the author reads (the display name
 *  of the contract the first workflow keeps, else the workflow's name) and the
 *  DSL id behind it. Both are empty-safe: a blank document is "workflow". */
export function documentNames(store: DocumentStore): { label: string; id: string } {
  const doc = store.getState().document;
  const workflow = doc?.workflows?.[0];
  const id = workflow?.name || "workflow";
  const contract = workflow?.contract ? doc?.contracts?.find((c) => c.name === workflow.contract) : undefined;
  return { label: contract?.display_name?.trim() || id, id };
}

/** A file name from a human name: lower-case words joined by hyphens
 *  ("Feature dev" → feature-dev, dev_flow → dev-flow). A DSL id cannot carry
 *  a hyphen, a file name can, and the project's own files use them. */
export function slugFileName(name: string, ...alternatives: string[]): string {
  for (const candidate of [name, ...alternatives]) {
    const slug = candidate
      .normalize("NFKD")
      .replace(/[\u0300-\u036f]/g, "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "");
    if (slug) return `${slug}.bot`;
  }
  return "workflow.bot";
}

function suggestedFileName(store: DocumentStore): string {
  const state = store.getState();
  // A document that came from a file keeps that file's name — saved under
  // its workflow's name instead, dev-flow.bot comes back as dev_flow.bot.
  if (state.currentFilePath) {
    return state.currentFilePath.split("/").pop() || slugFileName(documentNames(store).label, documentNames(store).id);
  }
  return state.importedFileName || slugFileName(documentNames(store).label, documentNames(store).id);
}

export function useDocumentSaveAs() {
  const isCloud = useServerInfoStore((state) => state.info?.mode === "cloud");
  const addToast = useUIStore((state) => state.addToast);
  const pushRecent = useRecentsStore((state) => state.pushRecent);
  const pending = useRef<PendingSaveAs | null>(null);
  const [open, setOpenState] = useState(false);
  const [fileName, setFileName] = useState("");
  const [names, setNames] = useState<{ label: string; id: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const setOpen = useCallback((next: boolean) => {
    setOpenState(next);
    if (!next) {
      pending.current = null;
      setError(null);
      setBusy(false);
    }
  }, []);

  const requestSaveAs = useCallback(
    (request: DocumentSaveAsRequest): boolean => {
      if (isCloud) {
        addToast(
          "Save As isn't available in cloud — create a bot from the Bots page, or Duplicate & edit an existing one.",
          "warning",
        );
        return false;
      }
      // One replacement at a time: an Open still loading in this tab is the
      // author's latest request for it, and Save As is refused until it ends.
      if (request.store.getState()._pendingIntent !== null) {
        addToast("A file is still opening in this tab. Use Save As once it has opened.", "warning");
        return false;
      }
      if (request.store.getState().unit) {
        addToast(
          "Save As isn't available for a bot in several files — save it in place; its fragments stay in lib/.",
          "warning",
        );
        return false;
      }
      // A salvage is refused here too: writing it to a NEW file leaves the
      // original whole but hands the author a copy missing the region the
      // parser could not read, under the name they chose — a loss they have
      // no reason to suspect.
      const refusal = salvageRefusal(request.store.getState());
      if (refusal) {
        addToast(refusal, "warning", { persistent: true });
        return false;
      }
      if (!request.store.getState().document) return false;
      pending.current = request;
      setFileName(suggestedFileName(request.store));
      setNames(request.store.getState().currentFilePath || request.store.getState().importedFileName ? null : documentNames(request.store));
      setError(null);
      setOpenState(true);
      return true;
    },
    [addToast, isCloud],
  );

  const confirmSaveAs = useCallback(async () => {
    const target = pending.current;
    const trimmed = fileName.trim();
    if (!target || !trimmed || busy) return;

    const state = target.store.getState();
    const validationError = target.validate?.() ?? null;
    if (validationError) {
      setError(validationError);
      return;
    }
    if (
      target.isTargetCurrent?.() === false ||
      (target.expectedGeneration !== undefined &&
        state._generation !== target.expectedGeneration)
    ) {
      setError(
        "The editor changed since this save was requested. Close this dialog and ask again.",
      );
      return;
    }
    if (!state.document) {
      setError("The editor document is unavailable.");
      return;
    }
    // Re-read HERE, next to the write, not only when the dialog opened: the
    // author spends seconds typing a name, and an external write reaching the
    // watcher in that window swaps the document for a salvage. A check that
    // ran at the dialog certifies the state the name was chosen in, not the
    // one about to be written.
    const refusal = salvageRefusal(state);
    if (refusal) {
      setError(refusal);
      return;
    }

    if (state._pendingIntent !== null) {
      setError("A file is still opening in this tab. Close this dialog and use Save As once it has opened.");
      return;
    }
    const path = trimmed.endsWith(".bot") ? trimmed : `${trimmed}.bot`;
    const document = state.document;
    // The write holds the document of this moment. The answer binds the new
    // name only if the tab still shows it — no replacement applied since —
    // or a tab showing another document would be told it now lives in that
    // file. Intrinsic, not left to the optional `isTargetCurrent`, which the
    // Toolbar does not pass.
    const asked = stampEditor(state);
    setBusy(true);
    setError(null);
    try {
      const result = await api.saveFile(path, document, { createOnly: true });
      const after = target.store.getState();
      const holds = stampHolds(asked, after);
      const stillCurrent = target.isTargetCurrent?.() !== false && holds.path && holds.replaced;
      const clean = stillCurrent && holds.generation;

      if (stillCurrent) {
        const heldSource = after.sourceBuffer;
        // The tab is about another file from here: an answer asked about the
        // old binding (a save still in flight) must not settle on it.
        after.markReplaced();
        after.setCurrentFilePath(result.path);
        // `setCurrentFilePath` drops the Source view's buffer. Save As is the
        // SAME program under a new name, so un-applied text the view can
        // still show there is carried to it; text it cannot show is said to be
        // discarded rather than held where nothing reaches it. The question is
        // the one the view's own release asks, put to the tab as it is now.
        if (heldSource && heldSource.text !== heldSource.base) {
          const carried = { ...heldSource, path: result.path };
          const now = target.store.getState();
          const unreachable = unreachableSourceBuffer(carried, now.currentFilePath, now.unit);
          if (unreachable) addToast(unreachable, "warning", { persistent: true });
          else after.setSourceBuffer(carried);
        }
        after.setCurrentSource(result.source);
        // The creation's answer is the claim the next save presents (the
        // digest rule): setCurrentFilePath above cleared the open's — there
        // is no open; the file begins with THIS write. Without it the first
        // save of the newly named file presents no digest over a file that
        // now exists, and is refused.
        after.setOpenedDigest(result.digest ?? null);
        if (clean) after.markSaved();
      }
      pushRecent(result.path);
      setOpenState(false);
      pending.current = null;
      if (stillCurrent) {
        addToast(
          clean
            ? "Saved successfully"
            : "Saved, but newer editor changes remain unsaved",
          clean ? "success" : "warning",
        );
      } else {
        addToast(
          `Saved ${result.path}, but the original editor session is no longer active`,
          "warning",
        );
      }
      target.onSaved?.({ path: result.path, source: result.source, clean });
    } catch (err) {
      const message = errorMessage(err);
      setError(
        /already exists/i.test(message)
          ? `${message} — Save As creates a new file and never overwrites. Pick another name, or open the existing file from Open recent to save over it.`
          : message,
      );
    } finally {
      setBusy(false);
    }
  }, [addToast, busy, fileName, pushRecent]);

  return {
    open,
    setOpen,
    fileName,
    setFileName,
    names,
    busy,
    error,
    requestSaveAs,
    confirmSaveAs,
  };
}

export type DocumentSaveAsController = ReturnType<typeof useDocumentSaveAs>;
