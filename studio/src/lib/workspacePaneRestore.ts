/**
 * workspacePaneRestore.ts — what a window reload (Ctrl+R) puts back in the
 * desktop / browser workspace.
 *
 * The workspace shell (main frame) hosts one <iframe src="/x/<id>/studio/"> per
 * open connection. A reload re-creates the shell and every pane from scratch:
 * before this module the shell forgot which connection tab was active and in
 * which order the tabs stood, and every pane came back on the studio home, so
 * Ctrl+R threw the author back to the first tab's Home page.
 *
 * Three small records, all in `sessionStorage` — it survives a reload of the
 * window, is private to one window (two browser windows on the workspace do not
 * overwrite each other), and is shared by the shell and its same-origin panes:
 *
 *   iterion.workspace.active                  id of the focused connection tab
 *   iterion.workspace.pane-order              ids of the open tabs, in order
 *   iterion.workspace.pane-location:<id>      a pane's route, studio-relative
 *
 * Every access is guarded: storage can be unavailable (private mode, quota) or
 * hold something a previous build or another script wrote, and the workspace
 * must still come up — on the default route — rather than fail on it.
 */

import { useEffect } from "react";
import { useLocation, useSearch } from "wouter";

import { scopePrefix, studioBase } from "@/lib/scope";

const ACTIVE_KEY = "iterion.workspace.active";
const ORDER_KEY = "iterion.workspace.pane-order";
const LOCATION_PREFIX = "iterion.workspace.pane-location:";

/** Longest route kept: a deep link with a long query is still a route, a
 *  megabyte of garbage in storage is not. */
const MAX_PATH_LENGTH = 2048;

function readItem(key: string): string | null {
  try {
    return window.sessionStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeItem(key: string, value: string | null): void {
  try {
    if (value === null) window.sessionStorage.removeItem(key);
    else window.sessionStorage.setItem(key, value);
  } catch {
    // storage unavailable: the reload then lands on the defaults
  }
}

/** The connection tab that was focused, if one was recorded. */
export function readActivePane(): string | null {
  const id = readItem(ACTIVE_KEY);
  return id ? id : null;
}

export function writeActivePane(id: string | null): void {
  writeItem(ACTIVE_KEY, id);
}

/** The order the connection tabs stood in, ids only. */
export function readPaneOrder(): string[] {
  const raw = readItem(ORDER_KEY);
  if (!raw) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === "string") : [];
  } catch {
    return [];
  }
}

export function writePaneOrder(ids: readonly string[]): void {
  writeItem(ORDER_KEY, JSON.stringify([...new Set(ids)]));
}

/**
 * `open` in the recorded order: ids the record knows come first in its order,
 * ids it does not know follow in the order they came. The desktop's registry
 * lists its open connections in opening order, so without this a tab order
 * changed since (or a registry that does not keep one) would be lost on reload.
 */
export function sortByPaneOrder(open: readonly string[], order: readonly string[]): string[] {
  const openSet = new Set(open);
  const known = order.filter((id, i) => openSet.has(id) && order.indexOf(id) === i);
  const knownSet = new Set(known);
  return [...known, ...open.filter((id) => !knownSet.has(id))];
}

/**
 * A recorded route is accepted only if it can be nothing but a route under the
 * studio: it starts with a single "/", carries no scheme or backslash (so it
 * cannot turn into another origin or path), no control characters, no `.` or
 * `..` segment (so it cannot climb out of the studio), and is not absurdly long. The value goes into an iframe `src`, so this is a boundary.
 */
export function sanitizeRestorePath(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  if (raw.length === 0 || raw.length > MAX_PATH_LENGTH) return null;
  if (!raw.startsWith("/") || raw.startsWith("//")) return null;
  if (raw.includes("\\")) return null;
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(raw)) return null;
  // No dot segments (also percent-encoded): the browser resolves them before
  // the request, so "/../../x/<other id>/studio/" would leave the studio.
  const pathOnly = raw.split(/[?#]/, 1)[0] ?? "";
  if (pathOnly.split("/").some((seg) => /^(\.|%2e){1,2}$/i.test(seg))) return null;
  return raw;
}

/** The route a pane for connection `id` should open on, studio-relative; "/"
 *  (the home) when nothing usable was recorded. */
export function paneInitialPath(id: string): string {
  return sanitizeRestorePath(readItem(LOCATION_PREFIX + id)) ?? "/";
}

export function savePaneLocation(id: string, path: string): void {
  const clean = sanitizeRestorePath(path);
  if (clean) writeItem(LOCATION_PREFIX + id, clean);
}

/** Forgets a pane's route: a tab the author closed starts at the home again. */
export function clearPaneLocation(id: string): void {
  writeItem(LOCATION_PREFIX + id, null);
}

/**
 * The studio-relative route of a document location, or null when the location
 * is not under the studio base (a /login page, an invitation, a share link —
 * routes that must not be restored over the home). A one-time `handoff` ticket
 * is dropped from the query: it is spent by the first load, and restoring it
 * would replay a dead ticket.
 */
export function studioRelativePath(
  loc: { pathname: string; search: string; hash: string },
  base: string,
): string | null {
  if (loc.pathname !== base && !loc.pathname.startsWith(base + "/")) return null;
  const rel = loc.pathname.slice(base.length) || "/";
  const params = new URLSearchParams(loc.search);
  params.delete("handoff");
  const query = params.toString();
  return rel + (query ? `?${query}` : "") + loc.hash;
}

/**
 * Pane side: remembers this pane's route so the shell can reopen it there after
 * a reload. A no-op outside a workspace pane (plain browser, the shell itself).
 * Mounted once, inside the studio router.
 */
export function usePaneLocationMemory(): void {
  const [location] = useLocation();
  const search = useSearch();
  useEffect(() => {
    const scope = scopePrefix();
    if (!scope) return;
    const rel = studioRelativePath(window.location, studioBase());
    if (rel) savePaneLocation(scope.slice("/x/".length), rel);
  }, [location, search]);
}
