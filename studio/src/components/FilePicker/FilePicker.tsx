import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import * as api from "@/api/client";
import type { FileEntry, ListFilesResponse } from "@/api/types";
import { errorMessage } from "@/lib/errorHints";
import { filesCutNotice } from "@/lib/filesCutNotice";
import { normalizeWorkflowPath } from "@/lib/openByPath";
import { useRecentsStore } from "@/store/recents";
import { useServerInfoStore } from "@/store/serverInfo";
import {
  Badge,
  Dialog,
  Tabs,
  Button,
  Input,
  InlineBanner,
  Spinner,
} from "@/components/ui";
import { MagnifyingGlassIcon, FileIcon, ClockIcon, RocketIcon, TrashIcon } from "@radix-ui/react-icons";
import { buildSearchResults, type SearchResult } from "./searchResults";

interface FilePickerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called with the chosen file path (workspace-relative) or example name. */
  onPick: (kind: "file" | "example", path: string) => void;
}

// Stable empty fallbacks so error/pre-load renders don't hand the
// search memo a fresh [] reference each render.
const EMPTY_FILES: FileEntry[] = [];
const EMPTY_EXAMPLE_NAMES: string[] = [];

export default function FilePicker({ open, onOpenChange, onPick }: FilePickerProps) {
  const [tab, setTab] = useState<string>("recents");
  const [query, setQuery] = useState("");
  const recents = useRecentsStore((s) => s.recents);
  const removeRecent = useRecentsStore((s) => s.removeRecent);
  const clearRecents = useRecentsStore((s) => s.clearRecents);

  // Both queries gate on the dialog being open and refetch on every
  // open (staleTime 0). Loading + error stay tracked per source so the
  // panels can distinguish "still fetching" / "fetch failed" /
  // "genuinely empty" — collapsing a failure into an empty list hides
  // the real problem from the user. isFetching (not isLoading) so each
  // reopen shows the loading state again, like the pre-query behavior.
  // The same query (and cache entry) as the editor's Project files panel: one
  // list, with the server's `truncated` flag, so a list cut at the cap says so
  // here as well instead of silently missing files.
  const filesQuery = useQuery<ListFilesResponse>({
    queryKey: ["workspace-files-page"],
    queryFn: () => api.listFilesPage(),
    enabled: open,
  });
  const examplesQuery = useQuery({
    queryKey: ["example-entries"],
    queryFn: () => api.listExampleEntries(),
    enabled: open,
  });
  const loadingFiles = filesQuery.isFetching;
  const filesError =
    !loadingFiles && filesQuery.error ? errorMessage(filesQuery.error) : null;
  const files = filesQuery.error ? EMPTY_FILES : (filesQuery.data?.files ?? EMPTY_FILES);
  const filesCut = !filesQuery.error && !!filesQuery.data?.truncated;
  const cutNotice = filesCut ? filesCutNotice(files.length, filesQuery.data?.total) : null;
  const workDir = useServerInfoStore((s) => s.info?.work_dir);
  const [pathInput, setPathInput] = useState("");
  const [pathError, setPathError] = useState<string | null>(null);
  const loadingExamples = examplesQuery.isFetching;
  const examplesError =
    !loadingExamples && examplesQuery.error
      ? errorMessage(examplesQuery.error)
      : null;
  const examples = useMemo<string[]>(
    () =>
      examplesQuery.error
        ? EMPTY_EXAMPLE_NAMES
        : (examplesQuery.data?.map((e) => e.name) ?? EMPTY_EXAMPLE_NAMES),
    [examplesQuery.data, examplesQuery.error],
  );

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setPathInput("");
    setPathError(null);
    // Fresh / first-time users have no recents: the project's own bots (the
    // Files tab) are the way in. Returning users keep Recents as the default
    // once they've opened anything.
    setTab(recents.length > 0 ? "recents" : "files");
    // Only opening the dialog resets it: removing a recent while it is open
    // must not throw away what the author typed or the tab they are on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const isSearching = query.trim() !== "";

  // Per-tab panels are only rendered when !isSearching, so the raw
  // arrays double as the displayed lists — no per-tab filtering needed.
  const searchResults = useMemo<SearchResult[]>(
    () => (isSearching ? buildSearchResults(query, recents, files, examples) : []),
    [isSearching, query, recents, files, examples],
  );

  const pick = (kind: "file" | "example", path: string) => {
    onPick(kind, path);
    onOpenChange(false);
  };

  // Open a project file that no list shows (workflows/, docs/, tests/…): the
  // tab binds to the path, so Save and Run act on the file itself.
  const openByPath = () => {
    const res = normalizeWorkflowPath(pathInput, workDir);
    if (!res.ok) {
      setPathError(res.error);
      return;
    }
    pick("file", res.path);
  };

  // While searching we visually highlight the Files tab (the
  // "exhaustive" view) without mutating `tab` — clearing the query
  // restores the user's prior selection.
  const displayTab = isSearching ? "files" : tab;

  // Clicking a tab while searching clears the query so the chosen
  // tab actually becomes visible — otherwise Radix would flip
  // `displayTab` back to "files" via `isSearching`.
  const handleTabChange = (next: string) => {
    if (isSearching) setQuery("");
    setTab(next);
  };

  const renderRecentRow = (path: string, source?: RowSource) => (
    <Row
      key={source ? `recent:${path}` : path}
      label={path}
      source={source}
      onPick={() => pick("file", path)}
      trailing={
        <button
          type="button"
          aria-label={`Remove ${path} from recents`}
          className="text-fg-subtle hover:text-danger"
          onClick={(e) => {
            e.stopPropagation();
            removeRecent(path);
          }}
        >
          <TrashIcon />
        </button>
      }
    />
  );

  // All three tabs render the same unified list while searching, so
  // whichever tab Radix has active still shows the cross-source hits.
  const searchPanel =
    searchResults.length === 0 ? (
      <>
        <Empty>No matches.</Empty>
        {cutNotice && <CutNotice>{cutNotice}</CutNotice>}
      </>
    ) : (
      <>
      <List>
        {searchResults.map((r) => {
          if (r.kind === "recent") return renderRecentRow(r.path, "recent");
          if (r.kind === "file") {
            return (
              <Row
                key={`file:${r.path}`}
                label={r.path}
                source="file"
                onPick={() => pick("file", r.path)}
              />
            );
          }
          return (
            <Row
              key={`example:${r.name}`}
              label={r.name}
              source="example"
              onPick={() => pick("example", r.name)}
            />
          );
        })}
      </List>
      {cutNotice && <CutNotice>{cutNotice}</CutNotice>}
      </>
    );

  const recentsPanel =
    recents.length === 0 ? (
      <Empty>Open a file to see it here.</Empty>
    ) : (
      <List>
        {recents.map((path) => renderRecentRow(path))}
        <li className="pt-2 mt-2 border-t border-border-default">
          <button
            type="button"
            className="text-xs text-fg-subtle hover:text-danger"
            onClick={clearRecents}
          >
            Clear all recents
          </button>
        </li>
      </List>
    );

  const filesPanel = loadingFiles ? (
    <LoadingState>Loading files…</LoadingState>
  ) : filesError ? (
    <InlineBanner
      tone="danger"
      layout="inline"
      title="Could not load workspace files"
    >
      {filesError}
    </InlineBanner>
  ) : files.length === 0 ? (
    <Empty>
      No bots in this project yet. Create one on the Bots page, or open a file by its path below.
    </Empty>
  ) : (
    <>
      <List>
        {files.map((f) => (
          <Row key={f.name} label={f.name} onPick={() => pick("file", f.name)} />
        ))}
      </List>
      {cutNotice && <CutNotice>{cutNotice}</CutNotice>}
    </>
  );

  const examplesPanel = loadingExamples ? (
    <LoadingState>Loading examples…</LoadingState>
  ) : examplesError ? (
    <InlineBanner
      tone="danger"
      layout="inline"
      title="Could not load examples"
    >
      {examplesError}
    </InlineBanner>
  ) : examples.length === 0 ? (
    <Empty>
      No examples in this project. Your bots are in the Files tab; create one on the Bots page, or
      open a file by its path below.
    </Empty>
  ) : (
    <List>
      {examples.map((n) => (
        <Row key={n} label={n} onPick={() => pick("example", n)} />
      ))}
    </List>
  );

  const panels: Record<string, React.ReactNode> = isSearching
    ? { recents: searchPanel, files: searchPanel, examples: searchPanel }
    : { recents: recentsPanel, files: filesPanel, examples: examplesPanel };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Open workflow"
      widthClass="max-w-xl"
    >
      <div className="space-y-3">
        <Input
          autoFocus
          placeholder="Search files..."
          aria-label="Search files"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          leadingIcon={<MagnifyingGlassIcon />}
          size="md"
        />
        {isSearching && (
          <p
            data-testid="filepicker-search-note"
            className="text-xs text-fg-subtle px-1"
            role="status"
            aria-live="polite"
          >
            Searching across all workflows ({searchResults.length}{" "}
            {searchResults.length === 1 ? "result" : "results"})
          </p>
        )}
        <Tabs
          value={displayTab}
          onValueChange={handleTabChange}
          variant="pill"
          items={[
            {
              value: "recents",
              label: `Recents${recents.length ? ` (${recents.length})` : ""}`,
              icon: <ClockIcon />,
            },
            {
              value: "files",
              label: `Files${files.length ? ` (${files.length}${filesCut ? "+" : ""})` : ""}`,
              icon: <FileIcon />,
            },
            {
              value: "examples",
              label: `Examples${examples.length ? ` (${examples.length})` : ""}`,
              icon: <RocketIcon />,
            },
          ]}
          panels={panels}
          listClassName="mb-2"
        />
        <form
          className="space-y-1 border-t border-border-default pt-3"
          onSubmit={(e) => {
            e.preventDefault();
            openByPath();
          }}
        >
          <div className="flex items-center gap-2">
            <Input
              placeholder="Open by path, e.g. workflows/dev-flow.bot"
              aria-label="Open a file by path"
              value={pathInput}
              onChange={(e) => {
                setPathInput(e.target.value);
                setPathError(null);
              }}
              size="md"
              className="min-w-0 flex-1"
            />
            <Button type="submit" variant="secondary" size="md" disabled={pathInput.trim() === ""}>
              Open
            </Button>
          </div>
          {pathError ? (
            <p role="alert" className="px-1 text-xs text-danger">
              {pathError}
            </p>
          ) : (
            <p className="px-1 text-caption text-fg-subtle">
              A file in this project outside bots/ and examples/ opens here, bound to its path.
            </p>
          )}
        </form>
      </div>
    </Dialog>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="text-xs text-fg-subtle py-6 text-center">{children}</p>;
}

function CutNotice({ children }: { children: React.ReactNode }) {
  return (
    <p role="status" className="px-1 pt-2 text-caption text-fg-subtle">
      {children}
    </p>
  );
}

function LoadingState({ children }: { children: React.ReactNode }) {
  return (
    <div className="py-6 text-center text-xs text-fg-subtle flex items-center justify-center gap-2">
      <Spinner size="sm" />
      <span>{children}</span>
    </div>
  );
}

function List({ children }: { children: React.ReactNode }) {
  return (
    <ul className="max-h-[320px] overflow-y-auto divide-y divide-border-default">{children}</ul>
  );
}

type RowSource = "recent" | "file" | "example";

const SOURCE_META: Record<RowSource, { icon: React.ReactNode; label: string }> = {
  recent: { icon: <ClockIcon />, label: "Recent" },
  file: { icon: <FileIcon />, label: "File" },
  example: { icon: <RocketIcon />, label: "Example" },
};

function SourceChip({ source }: { source: RowSource }) {
  const { icon, label } = SOURCE_META[source];
  return (
    <Badge
      variant="neutral"
      size="sm"
      leadingIcon={icon}
      className="uppercase tracking-wide mr-2 shrink-0"
      aria-label={`Source: ${label}`}
    >
      {label}
    </Badge>
  );
}

function Row({
  label,
  onPick,
  trailing,
  source,
}: {
  label: string;
  onPick: () => void;
  trailing?: React.ReactNode;
  source?: RowSource;
}) {
  // The trailing slot can itself be a <button> (e.g. "remove from
  // recents"), so it must be a DOM sibling of the row's main button —
  // not a child — to avoid the "<button> cannot be a descendant of
  // <button>" HTML invalidity. Absolute positioning preserves the
  // visual layout while keeping the elements as siblings.
  return (
    <li className="relative">
      <button
        type="button"
        onClick={onPick}
        className="w-full flex items-center px-2 py-2 text-sm text-fg-default hover:bg-surface-2 rounded-sm"
      >
        {source && <SourceChip source={source} />}
        <span className="truncate text-left flex-1">{label}</span>
        {trailing && <span aria-hidden className="w-5 shrink-0" />}
      </button>
      {trailing && (
        <span className="absolute right-2 top-1/2 -translate-y-1/2">
          {trailing}
        </span>
      )}
    </li>
  );
}
