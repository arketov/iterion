// The one place that opens a file, an example or an imported copy into an
// editor TAB, and that imports a file into an existing document store.
//
// Before this module the same logic lived three times — the Toolbar's
// handleImport (useDocumentFileOps), ProjectFilesPanel.handleOpenFromDisk and
// RecentFilesPanel.handleOpenExample — and the copies drifted: an import once
// lost the file's name in one of them, and only the Toolbar pruned a recent
// file that no longer exists on disk. No React in here: the callers pass what
// they own (the router's navigate, the toast function).

import * as api from "@/api/client";
import { applyOpenedFile } from "@/lib/openedFile";
import { openExampleIntoStore } from "@/lib/openExample";
import { replaceDocument, ReplaceDeadlineError, type ReplaceOutcome } from "@/lib/replaceDocument";
import { applyParsedSource, refuseReadRefusal } from "@/lib/salvage";
import { errorMessage, toastError } from "@/lib/errorHints";
import { getOrCreateDocumentStore, stampEditor, stampHolds } from "@/store/document";
import type { DocumentStore } from "@/store/document";
import { useRecentsStore } from "@/store/recents";
import { selectEditorTabs, useTabsStore } from "@/store/tabs";

type AddToast = Parameters<typeof toastError>[0];

/** What the callers own and this module must not reach for itself. */
export interface OpenDeps {
  navigate: (to: string) => void;
  addToast: AddToast;
}

/** `focused`: the file already had a tab. `opened`: a new tab holds it.
 *  `missing`: the file is gone from disk (its recent entry was removed).
 *  `failed`: the open failed for another reason; nothing was opened. */
export type OpenFileOutcome = "focused" | "opened" | "missing" | "failed";

/**
 * True when an open failed because the file is not there (deleted, moved, or
 * the workspace switched to a project that does not contain it) — not because
 * of a timeout or a server fault. Decides whether a recent entry is pruned.
 */
export function isMissingFileError(err: unknown): boolean {
  if (err instanceof ReplaceDeadlineError) return false;
  return /file not found|no such file|404/i.test(errorMessage(err) ?? "");
}

/** The editor's route for a file: a deep link EditorView reads on `?file=`. */
export function editorFileHref(path: string): string {
  return `/editor?file=${encodeURIComponent(path)}`;
}

/** Only `.bot` files are opened or imported from disk. */
export function isBotFile(file: File): boolean {
  return file.name.endsWith(".bot");
}

/**
 * Opens a project file in an editor tab, reading it BEFORE the tab exists.
 *
 * A file that is already open just gets its tab focused (no request). A file
 * the server cannot find opens no tab: a tab for it would show only an error,
 * and its entry in the recent list would stay for the next click — so the
 * entry is removed and the author told (the same words the Toolbar's picker
 * uses). Any other failure keeps the recent entry: the file may be fine.
 */
export async function openFileInNewTab(path: string, deps: OpenDeps): Promise<OpenFileOutcome> {
  const { navigate, addToast } = deps;
  const { pushRecent, removeRecent } = useRecentsStore.getState();
  const focusExisting = (file: string): boolean => {
    const existing = selectEditorTabs(useTabsStore.getState()).find((t) => t.params.file === file);
    if (!existing) return false;
    // setActive, not openTab: openTab matches the params exactly, and a tab
    // that gained a `draft` param (a draft saved with Save As) would get a
    // second, empty tab for the same file.
    useTabsStore.getState().setActive(existing.id);
    pushRecent(path);
    navigate(editorFileHref(file));
    return true;
  };
  if (focusExisting(path)) return "focused";
  let result: Awaited<ReturnType<typeof api.openFile>>;
  try {
    result = await api.openFile(path);
  } catch (err) {
    console.error("Open failed:", err);
    if (isMissingFileError(err)) {
      removeRecent(path);
      addToast(`Removed missing file from recents: ${path}`, "warning");
      return "missing";
    }
    toastError(addToast, err, "Open failed");
    return "failed";
  }
  // The server may name the file differently from the picker (normalised
  // path); the tab is about the name it gave. A tab already on THAT name must
  // not have the fresh reading applied over its edits.
  const bound = result.path || path;
  if (focusExisting(bound)) return "focused";
  const tabId = useTabsStore.getState().openTab("editor", { file: bound });
  // The reading goes into the tab's store before the tab mounts, so
  // EditorTabHost finds the file bound and does not fetch it again.
  applyOpenedFile(result, getOrCreateDocumentStore(tabId).getState());
  pushRecent(path);
  navigate(editorFileHref(bound));
  return "opened";
}

/**
 * Opens a bundled example (a first-class bot) in a fresh editor tab. The tab
 * is created first so its store exists; a failed load closes the tab again
 * unless the author started typing in it meanwhile.
 */
export async function openExampleInNewTab(name: string, deps: OpenDeps): Promise<void> {
  const { navigate, addToast } = deps;
  const tabId = useTabsStore.getState().newEditorTab(name);
  const store = getOrCreateDocumentStore(tabId);
  // The tab is active and editable at once — from the editor's home pane the
  // author can be typing into it while the example loads. What it held when it
  // was made is what a failure may throw away, and nothing else.
  const created = stampEditor(store.getState());
  try {
    // Applied or refused, the tab is kept — the example in it, or the work the
    // author typed into it while the example loaded. Not when a newer request
    // in that tab superseded it: that request is the author's, made from
    // wherever they are.
    if ((await openExampleIntoStore(name, store)) !== "superseded") navigate("/editor");
  } catch (err) {
    const holds = stampHolds(created, store.getState());
    if (holds.path && holds.generation && holds.source) {
      useTabsStore.getState().closeTab(tabId);
      toastError(addToast, err, "Failed to open example");
    } else {
      toastError(addToast, err, "Failed to open example — the tab you had started editing is kept", {
        persistent: true,
      });
      navigate("/editor");
    }
  }
}

/** What a FilePicker choice opens: a project file or a bundled example. */
export async function pickIntoNewTab(
  kind: "file" | "example",
  path: string,
  deps: OpenDeps,
): Promise<void> {
  if (kind === "file") {
    await openFileInNewTab(path, deps);
    return;
  }
  await openExampleInNewTab(path, deps);
}

/**
 * Imports a `.bot` from disk into `store` as an off-disk copy: parsed by the
 * server, bound to no path, its text kept as the source and its own file name
 * kept for Save As (dev-flow.bot must not come back as dev_flow.bot).
 *
 * The file is read INSIDE the load: a confirm was answered for the work
 * present when it was asked, and an edit made while the file is read or parsed
 * is work nobody was asked about. A reader refusal throws before anything is
 * applied: an import the server cut short has no program to install.
 */
export function importFileIntoStore(store: DocumentStore, file: File): Promise<ReplaceOutcome> {
  return replaceDocument(
    store,
    file.name,
    async (signal) => {
      const text = await file.text();
      const result = await api.parseSource(text, { signal });
      refuseReadRefusal(result);
      return { text, result };
    },
    ({ text, result }, s) => {
      s.setDiagnostics(result.diagnostics);
      // The path first — it clears the salvage flag — then the document and
      // the verdict together. Unbinding does NOT protect an import: Save As is
      // the only write an unbound buffer offers, and it would put a file
      // missing what the parser could not read under the name the author chose.
      s.setCurrentFilePath(null);
      // The store as the unbinding left it: `s` is the state before.
      applyParsedSource(result, store.getState());
      // Imported files are off-disk; the original text is the source.
      s.setCurrentSource(text);
      // The picker names no path, but it does name the file.
      s.setImportedFileName(file.name);
    },
  );
}

/**
 * Imports a `.bot` from disk into a NEW editor tab. The tab keeps its default
 * label until the file has loaded (a named tab with no bound file would read as
 * a lost binding after a reload); a failure or a refusal drops it again unless
 * the author started typing in it meanwhile.
 */
export async function importFileInNewTab(file: File, deps: OpenDeps): Promise<void> {
  const { navigate, addToast } = deps;
  const tabId = useTabsStore.getState().newEditorTab();
  const store = getOrCreateDocumentStore(tabId);
  // The tab is editable at once; what it held when made is all a failure may
  // throw away.
  const created = stampEditor(store.getState());
  const dropIfUntouched = () => {
    const holds = stampHolds(created, store.getState());
    if (holds.path && holds.generation && holds.source) {
      useTabsStore.getState().closeTab(tabId);
      return true;
    }
    return false;
  };
  try {
    const outcome = await importFileIntoStore(store, file);
    if (outcome === "applied") {
      useTabsStore.getState().rename(tabId, file.name);
      navigate("/editor");
    } else if (outcome === "refused") {
      // Nothing was applied: do not leave an empty tab posing as the file.
      dropIfUntouched();
    }
  } catch (err) {
    const dropped = dropIfUntouched();
    toastError(
      addToast,
      err,
      dropped ? "Open failed" : "Open failed — the tab you had started editing is kept",
    );
    if (!dropped) navigate("/editor");
  }
}
