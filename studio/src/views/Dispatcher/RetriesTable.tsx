import { Fragment, useEffect, useState } from "react";

import { Button } from "@/components/ui/Button";
import { Tooltip } from "@/components/ui";
import { Table, THead, Th, TBody, Tr, Td } from "@/components/ui/Table";
import { pointerRowProps } from "@/lib/a11y";
import type { DispatcherSnapshot } from "@/api/dispatcher";

import { hookFailureLabel, logHint, shellLabel } from "@/lib/retryError";

import { relTime } from "./format";

// useTick re-renders the caller at intervalMs while `active`. Used by
// RetriesTable to keep countdowns smooth without a full dispatcher poll
// each second — the retry table only needs to recompute due_at minus
// now() on its own clock.
function useTick(intervalMs: number, active: boolean): number {
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setTick(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs, active]);
  return tick;
}

// formatRetryDue returns a short human label for "in 12s" / "due now"
// derived purely from due_at + now. Lives next to RetriesTable so the
// formatting stays scoped to the retry context (the rest of the page
// uses relTime).
function formatRetryDue(dueIso: string, nowMs: number): string {
  if (!dueIso) return "";
  const due = Date.parse(dueIso);
  if (!Number.isFinite(due)) return "";
  const deltaS = Math.round((due - nowMs) / 1000);
  if (deltaS <= 0) return "due";
  if (deltaS < 60) return `in ${deltaS}s`;
  if (deltaS < 3600) return `in ${Math.round(deltaS / 60)}m`;
  return `in ${Math.round(deltaS / 3600)}h`;
}

export interface RetriesTableProps {
  rows: DispatcherSnapshot["retries"];
  // Host log file (DispatcherSnapshot.log_path), when known.
  logPath?: string;
  canPollDispatches: boolean;
  pollTitle: string;
  onFocusIssue: (issueID: string) => void;
  onRefreshNow: () => void;
  // Row actions. "Retry now" makes the retry due at once; "Remove from queue"
  // stops retrying the card (it is filed under the failed state, reopen it on
  // the board to try again). Both need a running dispatcher.
  onRetryNow: (issueID: string) => void;
  onDropRetry: (issueID: string) => void;
  canActOnRetries: boolean;
}

export default function RetriesTable({
  rows,
  logPath,
  canPollDispatches,
  pollTitle,
  onFocusIssue,
  onRefreshNow,
  onRetryNow,
  onDropRetry,
  canActOnRetries,
}: RetriesTableProps) {
  // Tick every 1s when at least one retry is due in under 5 minutes so
  // the countdown is responsive without burning CPU on long-deferred
  // queues.
  const needsTick = (rows ?? []).some((r) => {
    const due = Date.parse(r.due_at);
    return Number.isFinite(due) && due - Date.now() < 5 * 60_000;
  });
  const now = useTick(1000, needsTick);
  // Issue ids whose full last-error is unfolded.
  const [open, setOpen] = useState<Set<string>>(new Set());
  const toggle = (id: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  return (
    <section className="rounded-[var(--radius-lg)] border border-border-default bg-surface-1 shadow-[var(--shadow-sm)] overflow-hidden">
      <header className="px-4 py-2 border-b border-border-default text-title font-semibold flex items-center justify-between gap-2">
        <span>Retry queue ({rows?.length ?? 0})</span>
        {rows && rows.length > 0 && (
          <Tooltip content={pollTitle}>
            <Button
              variant="secondary"
              size="sm"
              onClick={onRefreshNow}
              disabled={!canPollDispatches}
            >
              Poll now
            </Button>
          </Tooltip>
        )}
      </header>
      {!rows || rows.length === 0 ? (
        <div className="p-4 text-xs text-fg-muted">No retries pending.</div>
      ) : (
        <Table caption="Failed dispatches queued for retry" density="sm" className="min-w-full">
          <THead>
            <Th className="whitespace-nowrap">Issue</Th>
            <Th>Attempt</Th>
            <Th className="whitespace-nowrap">Due</Th>
            <Th>Last error</Th>
            <Th align="right">Actions</Th>
          </THead>
          <TBody>
            {rows!.map((r) => {
              const dueLabel = formatRetryDue(r.due_at, now);
              const isDue = dueLabel === "due";
              const hookLabel = hookFailureLabel(r);
              const shell = shellLabel(r.hook_shell, r.hook_shell_path, r.hook_shell_source);
              const rowName = r.identifier || r.issue_id;
              const expanded = open.has(r.issue_id);
              return (
              <Fragment key={r.issue_id}>
              {/* The row click is a pointer convenience: the row holds controls
                  (the issue button, "Show full error"), so it carries no widget
                  role — the keyboard path to the card is the issue button. */}
              <Tr
                className={`cursor-pointer ${isDue ? "bg-warning-soft" : ""}`}
                {...pointerRowProps(() => onFocusIssue(r.issue_id))}
              >
                <Td className="font-mono whitespace-nowrap">
                  <button
                    type="button"
                    className="font-mono text-accent-text hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent rounded"
                    title="Show this issue on the board"
                    onClick={(e) => {
                      e.stopPropagation();
                      onFocusIssue(r.issue_id);
                    }}
                  >
                    {r.identifier || r.issue_id}
                  </button>
                </Td>
                <Td>{r.attempt}</Td>
                <Td className="whitespace-nowrap">
                  <span className={isDue ? "text-warning-fg" : "text-fg-muted"}>
                    {dueLabel || relTime(r.due_at)}
                  </span>
                </Td>
                <Td className="text-danger-fg max-w-[24rem]">
                  {hookLabel && (
                    <div className="text-caption font-semibold">{hookLabel}</div>
                  )}
                  {shell && (
                    <div
                      className="text-caption text-fg-muted font-mono break-all"
                      data-testid="retry-shell"
                    >
                      Shell: {shell}
                    </div>
                  )}
                  <div className={expanded ? "whitespace-pre-wrap break-words" : "truncate"}>
                    {r.error}
                  </div>
                  {r.error && (
                    <button
                      type="button"
                      className="text-caption text-accent underline"
                      aria-expanded={expanded}
                      onClick={(e) => {
                        e.stopPropagation();
                        toggle(r.issue_id);
                      }}
                    >
                      {expanded ? "Hide full error" : "Show full error"}
                    </button>
                  )}
                </Td>
                <Td align="right" className="whitespace-nowrap">
                  {/* One pair per queued retry: the accessible name carries
                      the row's identifier (visible word first, SC 2.5.3). */}
                  <Button
                    variant="secondary"
                    size="sm"
                    aria-label={`Retry now ${rowName}`}
                    disabled={!canActOnRetries}
                    onClick={(e) => {
                      e.stopPropagation();
                      onRetryNow(r.issue_id);
                    }}
                  >
                    Retry now
                  </Button>{" "}
                  <Button
                    variant="danger"
                    size="sm"
                    aria-label={`Remove from queue ${rowName}`}
                    disabled={!canActOnRetries}
                    onClick={(e) => {
                      e.stopPropagation();
                      onDropRetry(r.issue_id);
                    }}
                  >
                    Remove from queue
                  </Button>
                </Td>
              </Tr>
              {expanded && (
                <Tr>
                  <Td colSpan={5} className="bg-surface-2/40">
                    {r.hook_stderr && (
                      <pre className="mt-1 whitespace-pre-wrap break-words text-xs text-fg-muted">
                        {r.hook_stderr}
                      </pre>
                    )}
                    {logHint(logPath) && (
                      <div className="mt-1 text-caption text-fg-muted font-mono break-all">
                        {logHint(logPath)}
                      </div>
                    )}
                  </Td>
                </Tr>
              )}
              </Fragment>
              );
            })}
          </TBody>
        </Table>
      )}
    </section>
  );
}
