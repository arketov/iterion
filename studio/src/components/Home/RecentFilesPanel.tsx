import { ClampText } from "@/components/ui/ClampText";
import { useCallback, useEffect, useState } from "react";
import { useLocation } from "wouter";
import {
  FilePlusIcon,
  FileIcon,
  TrashIcon,
  ChevronDownIcon,
  ChevronRightIcon,
} from "@radix-ui/react-icons";

import type { BotEntryWithSchema } from "@/api/bots";
import { useRecentsStore } from "@/store/recents";
import { useTabsStore } from "@/store/tabs";
import { useBotsStore } from "@/store/bots";
import { countBots, projectFileRows, BOT_SOURCE_HINT } from "@/lib/botOrigin";
import { useConfirm } from "@/hooks/useConfirm";
import { basename } from "@/lib/format";
import { botIdentity } from "@/lib/personas";
import { Button, IconButton, InlineBanner } from "@/components/ui";
import { EmptyState } from "@/components/ui/EmptyState";
import { BotCatalogDialog } from "@/components/Catalog/BotCatalogDialog";

type Variant = "card" | "plain";

interface Props {
  // "card" (default): bordered section with header, used on Home.
  // "plain": no chrome, used as the EditorTabsView empty state where
  // the host already provides a centered container.
  variant?: Variant;
}

// The workspace-relative file the editor opens for a bot of the unified list,
// or null when the bot has no file in this workspace (a team or platform bot,
// a catalog outside the project): its bot page is the way in.
function botEditorFile(b: BotEntryWithSchema): string | null {
  if (!b.rel_path) return null;
  return b.is_bundle ? `${b.rel_path}/main.bot` : b.rel_path;
}

// RecentFilesPanel (a.k.a. workflow picker) shows the ways to start an
// editor tab: a fresh blank workflow, one of the project's own bots, or one of
// the user's recent files. The bots are the same list the Bots page and the
// Manage dialog show (the bots store) — one source for the rows and for the
// number in the header. Every entry point opens (or focuses) an editor tab via
// the tabs store — never the singleton document store — so multi-file editing
// stays consistent.
export default function RecentFilesPanel({ variant = "card" }: Props) {
  const [, setLocation] = useLocation();
  const recents = useRecentsStore((s) => s.recents);
  const pushRecent = useRecentsStore((s) => s.pushRecent);
  const removeRecent = useRecentsStore((s) => s.removeRecent);
  const clearRecents = useRecentsStore((s) => s.clearRecents);

  const [botsOpen, setBotsOpen] = useState(false);
  const [catalogOpen, setCatalogOpen] = useState(false);
  const { confirm, dialog: confirmDialog } = useConfirm();

  // The catalog bots plus the project's single .bot files come from one
  // response (GET /api/v1/bots?include=all); the header number is counted
  // from the very rows listed below, so the two cannot disagree.
  const catalogBots = useBotsStore((s) => s.bots);
  const extraBots = useBotsStore((s) => s.extraBots);
  const botsError = useBotsStore((s) => s.error);
  const projectFiles = projectFileRows(extraBots, "");
  const botsLoaded = catalogBots !== null;
  const botTotal = countBots(catalogBots, extraBots).total;
  const fetchBots = useBotsStore((s) => s.fetch);
  useEffect(() => {
    if (!botsLoaded) void fetchBots();
  }, [botsLoaded, fetchBots]);

  // A first-time user has no recents: show the bots open.
  useEffect(() => {
    if (recents.length === 0 && botTotal > 0) setBotsOpen(true);
  }, [recents.length, botTotal]);

  const handleNewBlank = useCallback(() => {
    useTabsStore.getState().newEditorTab();
    setLocation("/editor");
  }, [setLocation]);

  const handleOpenRecent = useCallback(
    (path: string) => {
      // Opening a file routes through openTab — EditorTabHost
      // will fetch the document via api.openFile on mount and bind it
      // to the per-tab store. Re-clicking the same path focuses the
      // existing tab instead of reloading.
      useTabsStore.getState().openTab("editor", { file: path });
      pushRecent(path);
      setLocation(`/editor?file=${encodeURIComponent(path)}`);
    },
    [pushRecent, setLocation],
  );

  const handleOpenBot = useCallback(
    (b: BotEntryWithSchema) => {
      const file = botEditorFile(b);
      if (file) handleOpenRecent(file);
      else setLocation(`/bots/${encodeURIComponent(b.name)}`);
    },
    [handleOpenRecent, setLocation],
  );

  const body = (
    <div className={variant === "card" ? "p-3 space-y-3" : "space-y-3"}>
      <Button
        onClick={handleNewBlank}
        variant="primary"
        size="md"
        leadingIcon={<FilePlusIcon className="w-4 h-4" />}
        className="w-full justify-start"
      >
        New blank workflow
      </Button>
      {botsError && (
        <InlineBanner
          tone="warning"
          layout="inline"
          title="Bot catalog unavailable"
        >
          {botsError}
        </InlineBanner>
      )}

      {(!botsLoaded || botTotal > 0) && (
        <div>
          <div className="flex items-center justify-between px-1">
            <button
              type="button"
              onClick={() => setBotsOpen((v) => !v)}
              className="flex items-center gap-1 text-xs font-medium text-fg-muted hover:text-fg-default focus-visible:ring-1 focus-visible:ring-accent rounded"
              aria-expanded={botsOpen}
            >
              {botsOpen ? (
                <ChevronDownIcon className="w-3 h-3" />
              ) : (
                <ChevronRightIcon className="w-3 h-3" />
              )}
              {/* No number until the list has loaded: the count is the
                  rows' own, never a placeholder that changes under the eye. */}
              <span title="Your bots in this project — the same list and count as the Bots page">
                {botsLoaded ? `Bots (${botTotal})` : "Bots"}
              </span>
            </button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setCatalogOpen(true)}
              title="Enable/disable bots and edit their metadata"
            >
              Manage
            </Button>
          </div>
          {botsOpen && !botsLoaded && (
            <p className="mt-1 px-2 text-xs text-fg-subtle" role="status">
              Loading bots…
            </p>
          )}
          {botsOpen && botsLoaded && (
            <ul className="mt-1 space-y-0.5">
              {(catalogBots ?? []).map((b) => {
                // Persona (manifest display_name) + emoji on line 1, with
                // the technical name muted beside it and a one-line
                // description below. The technical id also drives the
                // emoji/colour lookup.
                const identity = botIdentity(b.name);
                const persona = b.display_name?.trim();
                return (
                  <li key={`${b.origin}:${b.name}`}>
                    <button
                      type="button"
                      onClick={() => handleOpenBot(b)}
                      className={`w-full flex items-start gap-2 px-2 py-1.5 rounded hover:bg-surface-2 text-left focus-visible:ring-1 focus-visible:ring-accent ${b.enabled === false ? "opacity-60" : ""}`}
                    >
                      <span
                        className="text-sm leading-none shrink-0 mt-0.5"
                        aria-hidden="true"
                      >
                        {identity.emoji}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-baseline gap-1.5">
                          <span
                            className={`truncate font-medium text-xs ${identity.color}`}
                          >
                            {persona || b.name}
                          </span>
                          {persona && (
                            <span className="font-mono text-fg-subtle text-caption truncate shrink-0">
                              {b.name}
                            </span>
                          )}
                        </span>
                        {b.description && (
                          // The bot button is the one tab stop; its
                          // accessible name carries the description (#2803).
                          <ClampText
                            as="span"
                            className="block text-fg-muted text-micro mt-0.5"
                            text={b.description}
                            focusable={false}
                          />
                        )}
                      </span>
                    </button>
                  </li>
                );
              })}
              {projectFiles.map((f) => (
                <li key={f.path}>
                  <button
                    type="button"
                    onClick={() => handleOpenRecent(f.rel_path || f.path)}
                    className="w-full flex items-center gap-2 px-2 py-1.5 rounded hover:bg-surface-2 text-left text-xs focus-visible:ring-1 focus-visible:ring-accent"
                    title={`${BOT_SOURCE_HINT.file} ${f.path}`}
                  >
                    <FileIcon className="w-3.5 h-3.5 text-fg-subtle shrink-0" />
                    <span className="font-medium truncate">{f.name}</span>
                    <span className="text-fg-subtle text-caption shrink-0">file</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <div>
        <div className="flex items-center justify-between px-1">
          <span className="text-xs font-medium text-fg-muted">
            Recent ({recents.length})
          </span>
          {recents.length > 0 && (
            <Button
              variant="ghost"
              size="sm"
              onClick={async () => {
                const ok = await confirm({
                  title: "Clear recent files?",
                  message:
                    "All entries in the Recent list will be removed. Your files on disk are not affected.",
                  confirmLabel: "Clear",
                  confirmVariant: "danger",
                });
                if (ok) clearRecents();
              }}
            >
              Clear all
            </Button>
          )}
        </div>
        {recents.length === 0 ? (
          <EmptyState
            className="mt-2 py-3"
            message={
              botTotal > 0
                ? "No recent files yet — open one of your bots above or create a new workflow."
                : "No recent files yet — create a new workflow."
            }
          />
        ) : (
          <ul className="mt-1 space-y-0.5">
            {recents.map((path) => (
              <li key={path} className="group flex items-center gap-1">
                <button
                  type="button"
                  onClick={() => handleOpenRecent(path)}
                  className="flex-1 min-w-0 flex items-center gap-2 px-2 py-1.5 rounded hover:bg-surface-2 text-left text-xs focus-visible:ring-1 focus-visible:ring-accent"
                  title={path}
                >
                  <FileIcon className="w-3.5 h-3.5 text-fg-subtle shrink-0" />
                  <span className="font-medium truncate">
                    {basename(path)}
                  </span>
                  {basename(path) !== path && (
                    <span className="text-fg-subtle text-caption truncate">
                      {path}
                    </span>
                  )}
                </button>
                <IconButton
                  label={`Remove ${path} from recents`}
                  tooltip="Remove from recents"
                  size="sm"
                  variant="ghost"
                  onClick={(e) => {
                    e.stopPropagation();
                    removeRecent(path);
                  }}
                  className="h-7 w-7 text-fg-subtle hover:text-danger opacity-0 group-hover:opacity-100 focus-visible:opacity-100 transition-opacity"
                >
                  <TrashIcon className="w-3.5 h-3.5" />
                </IconButton>
              </li>
            ))}
          </ul>
        )}
      </div>
      {confirmDialog}
      <BotCatalogDialog open={catalogOpen} onOpenChange={setCatalogOpen} />
    </div>
  );

  if (variant === "plain") return body;

  return (
    <section className="flex flex-col bg-surface-1 border border-border-default rounded-[var(--radius-lg)] shadow-[var(--shadow-sm)] overflow-hidden">
      <header className="px-4 py-2.5 border-b border-border-default flex items-center justify-between">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-fg-muted">
          Workflows
        </h2>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setCatalogOpen(true)}
          title="Enable/disable bots and edit their metadata"
        >
          Manage bots
        </Button>
      </header>
      {body}
    </section>
  );
}
