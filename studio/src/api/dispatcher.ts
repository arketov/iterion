// Dispatcher REST client — mirrors pkg/dispatcher/manager.go +
// pkg/dispatcher/state.go. Covers both lifecycle (status / config /
// start / pause / resume / stop) and operational (state / refresh /
// reload / issue cancel / ws) endpoints.

import { errorMessage } from "@/lib/errorHints";
import { apiRequest } from "./client";
import { getDesktopWsBase, isDesktop, isWailsHosted } from "@/lib/desktopBridge";
import { apiBase, isScopedPane, resolveScopedWsUrl } from "@/lib/scope";

const BASE = "/api/v1/dispatcher";
const API_BASE = apiBase();

function request<T>(path: string, init?: RequestInit): Promise<T> {
  return apiRequest<T>(BASE + path, init);
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

export type ManagerState = "idle" | "running" | "paused" | "error";

export interface ManagerStatus {
  state: ManagerState;
  has_config: boolean;
  started_at?: string;
  last_error?: string;
}

export function getStatus(): Promise<ManagerStatus> {
  return request("/status");
}

export function start(): Promise<ManagerStatus> {
  return request("/start", { method: "POST" });
}

export function stop(): Promise<ManagerStatus> {
  return request("/stop", { method: "POST" });
}

export function pause(): Promise<ManagerStatus> {
  return request("/pause", { method: "POST" });
}

export function resume(): Promise<ManagerStatus> {
  return request("/resume", { method: "POST" });
}

// applyDefaults asks the server to build the default dispatcher Config for
// this project (its own bots under bots/, examples/ and .botz/, the local
// board, require_assignee on), persist it, and start the dispatcher in one
// call. Returns 409 if a config already exists (pass `replace: true` to reset
// it, or PUT /config to edit it), 501 if the running binary was built without
// the defaults closure wired (out-of-process dispatch mode).
//
// `replace: true` is the operator's explicit "back to the default
// settings": the server overwrites the saved config (and live-reloads a
// running dispatcher) instead of answering 409.
export function applyDefaults(opts?: { replace?: boolean }): Promise<ManagerStatus> {
  return request(opts?.replace ? "/defaults/apply?replace=1" : "/defaults/apply", {
    method: "POST",
  });
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Mirror pkg/dispatcher/config.go. snake_case throughout — same wire
// shape on both YAML and JSON sides.
export type TrackerKind = "native" | "github" | "forgejo" | "gitlab";

export interface LabelSelector {
  labels_include?: string[];
  labels_exclude?: string[];
}

export interface GitHubTrackerConfig {
  repo: string;
  token?: string;
  state_mapping?: Record<string, LabelSelector>;
  claimed_label?: string;
  include_labels?: string[];
  exclude_labels?: string[];
}

export interface ForgejoTrackerConfig {
  host: string;
  repo: string;
  token?: string;
  state_mapping?: Record<string, LabelSelector>;
  claimed_label?: string;
  include_labels?: string[];
  exclude_labels?: string[];
}

export interface GitLabTrackerConfig {
  host: string;
  repo: string;
  token?: string;
  state_mapping?: Record<string, LabelSelector>;
  claimed_label?: string;
  include_labels?: string[];
  exclude_labels?: string[];
}

export interface TrackerConfig {
  kind: TrackerKind;
  native?: Record<string, never>;
  github?: GitHubTrackerConfig;
  forgejo?: ForgejoTrackerConfig;
  gitlab?: GitLabTrackerConfig;
}

export interface DispatchConfig {
  vars?: Record<string, string>;
  attachments?: Record<string, string>;
}

export interface PollingConfig {
  interval_ms?: number;
}

export interface AgentConfig {
  max_concurrent?: number;
  max_concurrent_by_state?: Record<string, number>;
  max_turns?: number;
  max_retry_backoff_ms?: number;
  /** Total attempts per card; a negative value retries forever. */
  max_attempts?: number;
  /** Board states the dispatcher moves a card through; "none" opts out. */
  running_state?: string;
  completed_state?: string;
  merged_state?: string;
  failed_state?: string;
}

export type WorkspacePersistPolicy = "" | "keep" | "cleanup_on_done" | "cleanup_on_terminal";

export interface WorkspaceConfig {
  root?: string;
  persist?: WorkspacePersistPolicy;
}

export interface HookSpec {
  script?: string;
  path?: string;
  timeout_ms?: number;
  /** sh, bash, pwsh, powershell or a path; empty = the host default. */
  shell?: string;
}

export interface HooksConfig {
  after_create?: HookSpec | null;
  before_run?: HookSpec | null;
  after_run?: HookSpec | null;
  before_remove?: HookSpec | null;
}

export interface StallConfig {
  timeout_ms?: number;
}

export interface ServerConfig {
  port?: number;
}

/** Where the dispatcher looks for bots: discovery roots, relative ones taken
 *  from the project root. Empty = the project's bots/, examples/ and .botz/. */
export interface BotsConfig {
  paths?: string[];
}

/** Per-bot input vars (var name → template) that replace the bot's manifest
 *  `dispatch_vars` and the global `dispatch.vars` for that bot. */
export interface AssigneeDispatchConfig {
  vars?: Record<string, string>;
  attachments?: Record<string, string>;
}

export interface TicketRouterConfig {
  enabled: boolean;
  implement_bot?: string;
}

export interface LimitsConfig {
  max_cost_per_day_usd?: number;
}

export interface DispatcherConfig {
  name?: string;
  /** Fallback workflow for a card that names no bot. May be empty when
   *  `require_assignee` is on: the dispatcher then runs only bots it can route
   *  to. A relative path is resolved against the project root. */
  workflow: string;
  /** assignee name → workflow path: a card assigned to that name runs the
   *  given workflow instead of `workflow`. Relative paths are resolved
   *  against the project root, like `workflow`. */
  assignee_workflows?: Record<string, string>;
  /** Per-bot dispatch vars (see AssigneeDispatchConfig). */
  assignee_dispatch?: Record<string, AssigneeDispatchConfig>;
  /** A card that names no bot — or whose assignee is not a bot of the
   *  project — is not run (it waits as "no assignee" / "no bot"). */
  require_assignee?: boolean;
  bots?: BotsConfig;
  ticket_router?: TicketRouterConfig;
  limits?: LimitsConfig;
  tracker: TrackerConfig;
  dispatch?: DispatchConfig;
  polling?: PollingConfig;
  agent?: AgentConfig;
  workspace?: WorkspaceConfig;
  hooks?: HooksConfig;
  stall?: StallConfig;
  server?: ServerConfig;
}

export async function getConfig(): Promise<DispatcherConfig | null> {
  try {
    return await request<DispatcherConfig>("/config");
  } catch (err: unknown) {
    // 404 = no config persisted yet → null is a meaningful "empty"
    // state the form binds to.
    const msg = errorMessage(err);
    if (msg.includes(" 404")) return null;
    throw err;
  }
}

export function saveConfig(cfg: DispatcherConfig): Promise<DispatcherConfig> {
  return request("/config", { method: "PUT", body: JSON.stringify(cfg) });
}

// ---------------------------------------------------------------------------
// Operational
// ---------------------------------------------------------------------------

export interface DispatcherSnapshot {
  name: string;
  tracker: string;
  generated_at: string;
  polling_interval_seconds: number;
  stall_timeout_seconds: number;
  paused: boolean;
  running: RunningView[] | null;
  retries: RetryView[] | null;
  slots: SlotsView;
  // Stickied error from the last failed tracker.ListCandidates call.
  // Empty / absent once the next poll succeeds.
  last_tracker_error?: string;
  last_tracker_error_at?: string;
  // File the host process logs to (full hook output / chronology), when known.
  log_path?: string;
  // Per-(store, UTC-day) spend-cap status when a cap is configured;
  // absent when disabled. The dashboard renders the CostCapBanner off
  // the live /api/v1/limits/cost endpoint; this mirrors the gate state
  // the dispatcher acted on.
  cost_cap?: {
    date: string;
    spent_usd: number;
    limit_usd: number;
    exceeded: boolean;
    override_active: boolean;
  };
  // Eligible issues the dispatcher refused to claim this scan because
  // their explicit `bot` is unresolvable / unrouteable. Absent when
  // none. Surfaced so a misconfigured `bot:` is visible on the board +
  // dashboard instead of silently never running.
  dispatch_skips?: DispatchSkipView[];
}

export interface DispatchSkipView {
  issue_id: string;
  identifier: string;
  bot?: string;
  reason: string;
  /** The card's assignee, so the UI can say which name is not a bot. */
  assignee?: string;
  // "no_assignee": a ready card with no assignee/bot (require_assignee on).
  // "no_bot": its assignee is not a bot the dispatcher can route to.
  // Absent for the bot-misconfiguration family.
  kind?: string;
}

export interface RunningView {
  issue_id: string;
  identifier: string;
  run_id: string;
  workflow_state: string;
  workspace_path?: string;
  started_at: string;
  last_event_at: string;
  last_event_name?: string;
  attempt?: number;
}

export interface RetryView {
  issue_id: string;
  identifier: string;
  attempt: number;
  due_at: string;
  // Full last-failure text (not truncated server-side).
  error?: string;
  // Lifecycle hook (after_create, before_run, …) whose failure this is.
  hook?: string;
  // That hook's exit code; -1 = no exit status (could not start / timeout).
  hook_exit_code?: number;
  // Tail of the failed hook's stderr.
  hook_stderr?: string;
}

export interface SlotsView {
  global_max: number;
  global_used: number;
  per_state_max?: Record<string, number>;
  per_state_used?: Record<string, number>;
}

export function getState(): Promise<DispatcherSnapshot> {
  return request("/state");
}

export function refresh(): Promise<{ queued: boolean }> {
  return request("/refresh", { method: "POST" });
}

export function reload(): Promise<{ reloaded: boolean; polling_interval_s: number }> {
  return request("/reload", { method: "POST" });
}

export function cancelIssue(id: string): Promise<{ status: string }> {
  return request(`/issues/${encodeURIComponent(id)}/cancel`, { method: "POST" });
}

// openWS returns a connected WebSocket that broadcasts Snapshot
// updates. The caller must close it. Reconnection is the caller's
// responsibility.
//
// Mirrors api/ws.ts:deriveWsUrl for the desktop bridge — without it,
// window.location.host on Wails is "wails.localhost" which can't
// accept WS upgrades, so the dashboard would only show the static
// snapshot with no live updates.
export async function openWS(): Promise<WebSocket> {
  const path = `${BASE}/ws`;
  // Workspace pane: dial the pane's backend directly (ws base + ticket over
  // the demux proxy). `path` is already the backend-native /api/... form, so
  // resolveScopedWsUrl leaves it intact and only prepends the ws base.
  if (isScopedPane()) {
    return new WebSocket(await resolveScopedWsUrl(path));
  }
  if (isDesktop()) {
    const desktopUrl = await getDesktopWsBase(path);
    if (desktopUrl) return new WebSocket(desktopUrl);
  }
  if (isWailsHosted()) {
    throw new Error("desktop bindings not ready");
  }
  if (API_BASE.startsWith("http")) {
    return new WebSocket(API_BASE.replace(/^http/, "ws") + BASE + "/ws");
  }
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return new WebSocket(`${proto}//${window.location.host}${path}`);
}
