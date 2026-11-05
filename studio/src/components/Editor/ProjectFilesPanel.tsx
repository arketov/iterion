import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation } from "wouter";
import { useQuery } from "@tanstack/react-query";
import { FileIcon, UploadIcon } from "@radix-ui/react-icons";

import * as api from "@/api/client";
import type { FileEntry } from "@/api/types";
import { Button, InlineBanner } from "@/components/ui";
import { ClampText } from "@/components/ui/ClampText";
import { EmptyState } from "@/components/ui/EmptyState";
import FilePicker from "@/components/FilePicker/FilePicker";
import { HiddenFileInput } from "@/components/shared/HiddenFileInput";
import { openExampleIntoStore } from "@/lib/openExample";
import { errorMessage, toastError } from "@/lib/errorHints";
import { basename } from "@/lib/format";
import { replaceDocument } from "@/lib/replaceDocument";
import { applyParsedSource, refuseReadRefusal } from "@/lib/salvage";
import { getOrCreateDocumentStore, stampEditor, stampHolds } from "@/store/document";
import { useRecentsStore } from "@/store/recents";
import { useTabsStore } from "@/store/tabs";
import { useUIStore } from "@/store/ui";

const EMPTY_FILES: FileEntry[] = [];

// ProjectFilesPanel is the "open an existing file" half of the editor home:
// the `.bot` files of the current project (GET /api/files — including ones
// never opened in the studio, e.g. written by hand or by a bot) and an
// "Open…" button (Ctrl+O, the searchable picker) and an "Import copy…" button
// for a file anywhere on disk. All open a fresh editor tab; a project file is
// bound to its workspace path (Save/Run work), a file picked from disk is an
// off-disk copy exactly like the toolbar's "Import copy from disk…" (Save As
// proposes its own name). Ctrl+O is answered here while this pane is shown:
// with no tab open there is no Toolbar to listen for it.
export default function ProjectFilesPanel() {
  const [, setLocation] = useLocation();
  const pushRecent = useRecentsStore((s) => s.pushRecent);
  const addToast = useUIStore((s) => s.addToast);
  const inputRef = useRef<HTMLInputElement>(null);
  const [pickerOpen, setPickerOpen] = useState(false);

  const filesQuery = useQuery({
    // Not ["workspace-files"]: that cache holds the bare entry array for the
    // FilePicker; this one keeps the truncation flag too.
    queryKey: ["workspace-files-page"],
    queryFn: () => api.listFilesPage(),
    // The folder changes under the studio (a bot writes a file, the author
    // edits one by hand): refetch each time the home pane is shown.
    staleTime: 0,
  });
  const files = filesQuery.error ? EMPTY_FILES : (filesQuery.data?.files ?? EMPTY_FILES);
  const bundles = files.filter((f) => f.kind === "bundle");
  const singles = files.filter((f) => f.kind !== "bundle");
  const truncated = !filesQuery.error && !!filesQuery.data?.truncated;
  const filesError = filesQuery.error ? errorMessage(filesQuery.error) : null;

  const openProjectFile = useCallback(
    (path: string) => {
      useTabsStore.getState().openTab("editor", { file: path });
      pushRecent(path);
      setLocation(`/editor?file=${encodeURIComponent(path)}`);
    },
    [pushRecent, setLocation],
  );

  // The pane is mounted only on the editor home, so the key is this pane's
  // while it is on screen and nobody else's (a Toolbar answers it in a tab).
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if ((e.ctrlKey || e.metaKey) && e.key === "o") {
        e.preventDefault();
        setPickerOpen(true);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  const handlePick = useCallback(
    async (kind: "file" | "example", path: string) => {
      if (kind === "file") {
        openProjectFile(path);
        return;
      }
      const tabId = useTabsStore.getState().newEditorTab(path);
      const store = getOrCreateDocumentStore(tabId);
      try {
        if ((await openExampleIntoStore(path, store)) !== "superseded") setLocation("/editor");
      } catch (err) {
        useTabsStore.getState().closeTab(tabId);
        toastError(addToast, err, "Failed to open example");
      }
    },
    [openProjectFile, addToast, setLocation],
  );

  const handleOpenFromDisk = useCallback(
    async (e: React.ChangeEvent<HTMLInputElement>) => {
      const input = e.target;
      const file = input.files?.[0];
      if (!file) return;
      if (!file.name.endsWith(".bot")) {
        addToast("Only .bot files can be opened", "error");
        input.value = "";
        return;
      }
      // The tab keeps its default label until the file has loaded: a named
      // tab with no bound file would read as a lost binding after a reload.
      const tabId = useTabsStore.getState().newEditorTab();
      const store = getOrCreateDocumentStore(tabId);
      // The tab is editable at once; what it held when made is all a failure
      // may throw away (see RecentFilesPanel.handleOpenExample).
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
        const outcome = await replaceDocument(
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
            s.setCurrentFilePath(null);
            applyParsedSource(result, store.getState());
            s.setCurrentSource(text);
            // No path, but the file's own name: Save As proposes it.
            s.setImportedFileName(file.name);
          },
        );
        if (outcome === "applied") {
          useTabsStore.getState().rename(tabId, file.name);
          setLocation("/editor");
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
        if (!dropped) setLocation("/editor");
      }
      input.value = "";
    },
    [addToast, setLocation],
  );

  return (
    <div>
      <div className="flex items-center justify-between px-1">
        <span className="text-xs font-medium text-fg-muted">
          Project files{files.length > 0 ? ` (${files.length}${truncated ? "+" : ""})` : ""}
        </span>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            leadingIcon={<FileIcon className="w-3.5 h-3.5" />}
            onClick={() => setPickerOpen(true)}
            title="Open a project file or example (Ctrl+O)"
          >
            Open…
          </Button>
          <Button
            variant="ghost"
            size="sm"
            leadingIcon={<UploadIcon className="w-3.5 h-3.5" />}
            onClick={() => inputRef.current?.click()}
            title="Open a copy of a .bot file from disk in a new tab. Save creates a new file in the project; it does not write over the original."
          >
            Import copy…
          </Button>
        </div>
      </div>
      <FilePicker open={pickerOpen} onOpenChange={setPickerOpen} onPick={handlePick} />
      <HiddenFileInput inputRef={inputRef} accept=".bot" onChange={handleOpenFromDisk} />
      {filesError ? (
        <InlineBanner tone="warning" layout="inline" title="Could not list project files">
          {filesError}
        </InlineBanner>
      ) : files.length === 0 ? (
        <EmptyState
          className="mt-2 py-3"
          message={
            filesQuery.isLoading
              ? "Loading project files…"
              : "No .bot files in this project yet — create a new workflow or import one from disk."
          }
        />
      ) : (
        <div className="mt-1 space-y-2">
          {bundles.length > 0 && <FileGroup title="Bot bundles" files={bundles} onOpen={openProjectFile} />}
          {singles.length > 0 && (
            <FileGroup
              title={bundles.length > 0 ? "Single files" : undefined}
              files={singles}
              onOpen={openProjectFile}
            />
          )}
        </div>
      )}
      {truncated && (
        <p className="mt-1 px-1 text-caption text-fg-subtle">
          Showing the first {files.length} files. Use Open file… for the rest, or raise
          ITERION_STUDIO_FILES_LIMIT.
        </p>
      )}
    </div>
  );
}

// A bundle's entry file is always main.bot: name the row after its directory.
function displayName(f: FileEntry): string {
  if (f.kind === "bundle") {
    const dir = f.name.split("/").slice(0, -1);
    if (dir.length > 0) return dir[dir.length - 1];
  }
  return basename(f.name);
}

function FileGroup({
  title,
  files,
  onOpen,
}: {
  title?: string;
  files: FileEntry[];
  onOpen: (path: string) => void;
}) {
  return (
    <div>
      {title && <div className="px-1 text-caption text-fg-subtle">{title}</div>}
      <ul className="space-y-0.5">
        {files.map((f) => (
          <li key={f.name}>
            <button
              type="button"
              onClick={() => onOpen(f.name)}
              className="w-full flex items-center gap-2 px-2 py-1.5 rounded hover:bg-surface-2 text-left text-xs focus-visible:ring-1 focus-visible:ring-accent"
              title={f.name}
            >
              <FileIcon className="w-3.5 h-3.5 text-fg-subtle shrink-0" />
              <ClampText className="font-medium min-w-0" text={displayName(f)} focusable={false} />
              {displayName(f) !== f.name && (
                <ClampText className="text-fg-subtle text-caption min-w-0" text={f.name} focusable={false} />
              )}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
