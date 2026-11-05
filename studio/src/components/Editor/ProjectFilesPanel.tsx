import { useCallback, useRef } from "react";
import { useLocation } from "wouter";
import { useQuery } from "@tanstack/react-query";
import { FileIcon, UploadIcon } from "@radix-ui/react-icons";

import * as api from "@/api/client";
import type { FileEntry } from "@/api/types";
import { Button, InlineBanner } from "@/components/ui";
import { EmptyState } from "@/components/ui/EmptyState";
import { HiddenFileInput } from "@/components/shared/HiddenFileInput";
import { errorMessage, toastError } from "@/lib/errorHints";
import { basename } from "@/lib/format";
import { replaceDocument } from "@/lib/replaceDocument";
import { applyParsedSource, refuseReadRefusal } from "@/lib/salvage";
import { getOrCreateDocumentStore } from "@/store/document";
import { useRecentsStore } from "@/store/recents";
import { useTabsStore } from "@/store/tabs";
import { useUIStore } from "@/store/ui";

const EMPTY_FILES: FileEntry[] = [];

// ProjectFilesPanel is the "open an existing file" half of the editor home:
// the `.bot` files of the current project (GET /api/files — including ones
// never opened in the studio, e.g. written by hand or by a bot) and an
// "Open file…" button for a file anywhere on disk. Both open a fresh editor
// tab; a project file is bound to its workspace path (Save/Run work), a file
// picked from disk is an off-disk buffer exactly like the toolbar's Open….
export default function ProjectFilesPanel() {
  const [, setLocation] = useLocation();
  const pushRecent = useRecentsStore((s) => s.pushRecent);
  const addToast = useUIStore((s) => s.addToast);
  const inputRef = useRef<HTMLInputElement>(null);

  const filesQuery = useQuery({
    queryKey: ["workspace-files"],
    queryFn: () => api.listFiles(),
    // The folder changes under the studio (a bot writes a file, the author
    // edits one by hand): refetch each time the home pane is shown.
    staleTime: 0,
  });
  const files = filesQuery.error ? EMPTY_FILES : (filesQuery.data ?? EMPTY_FILES);
  const filesError = filesQuery.error ? errorMessage(filesQuery.error) : null;

  const openProjectFile = useCallback(
    (path: string) => {
      useTabsStore.getState().openTab("editor", { file: path });
      pushRecent(path);
      setLocation(`/editor?file=${encodeURIComponent(path)}`);
    },
    [pushRecent, setLocation],
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
      const tabId = useTabsStore.getState().newEditorTab(file.name);
      const store = getOrCreateDocumentStore(tabId);
      try {
        await replaceDocument(
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
          },
        );
        setLocation("/editor");
      } catch (err) {
        useTabsStore.getState().closeTab(tabId);
        toastError(addToast, err, "Open failed");
      }
      input.value = "";
    },
    [addToast, setLocation],
  );

  return (
    <div>
      <div className="flex items-center justify-between px-1">
        <span className="text-xs font-medium text-fg-muted">
          Project files{files.length > 0 ? ` (${files.length})` : ""}
        </span>
        <Button
          variant="ghost"
          size="sm"
          leadingIcon={<UploadIcon className="w-3.5 h-3.5" />}
          onClick={() => inputRef.current?.click()}
          title="Open a .bot file from disk in a new tab"
        >
          Open file…
        </Button>
      </div>
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
              : "No .bot files in this project yet — create a new workflow or open one from disk."
          }
        />
      ) : (
        <ul className="mt-1 space-y-0.5">
          {files.map((f) => (
            <li key={f.name}>
              <button
                type="button"
                onClick={() => openProjectFile(f.name)}
                className="w-full flex items-center gap-2 px-2 py-1.5 rounded hover:bg-surface-2 text-left text-xs focus-visible:ring-1 focus-visible:ring-accent"
                title={f.name}
              >
                <FileIcon className="w-3.5 h-3.5 text-fg-subtle shrink-0" />
                <span className="font-medium truncate">{basename(f.name)}</span>
                {basename(f.name) !== f.name && (
                  <span className="text-fg-subtle text-caption truncate">{f.name}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
