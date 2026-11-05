import { useState } from "react";

import { CopyButton } from "@/components/ui/CopyButton";
import { describeLaunchFailure } from "@/lib/launchFailure";
import type { LaunchFailure } from "@/api/native";

// LaunchFailureDetails renders one dispatch attempt that never became a run:
// the reason in one line, a "Show full error" fold with the full text and the
// hook's stderr, and the host log path (copyable). No run link, no diff — no
// run exists under the attempt's id.
export function LaunchFailureDetails({ failure }: { failure: LaunchFailure }) {
  const [open, setOpen] = useState(false);
  const when = Number.isFinite(Date.parse(failure.at))
    ? new Date(failure.at).toLocaleString()
    : failure.at;
  return (
    <div className="text-xs space-y-1" data-testid="launch-failure">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <span className="font-semibold text-danger-fg">
          {describeLaunchFailure(failure)}
        </span>
        <span className="text-fg-muted">
          {failure.attempt ? `attempt ${failure.attempt} · ` : ""}
          {when}
        </span>
        <button
          type="button"
          className="text-accent-text underline"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          {open ? "Hide full error" : "Show full error"}
        </button>
      </div>
      {open && (
        <div className="space-y-1">
          <pre className="whitespace-pre-wrap break-words text-danger-fg bg-surface-2/40 rounded p-1.5">
            {failure.error}
          </pre>
          {failure.stderr && (
            <pre className="whitespace-pre-wrap break-words text-fg-muted bg-surface-2/40 rounded p-1.5">
              {failure.stderr}
            </pre>
          )}
          {failure.run_id && (
            <div className="text-fg-subtle">
              Attempt id {failure.run_id} — no run was created under it.
            </div>
          )}
        </div>
      )}
      {failure.log_path && (
        <div className="flex items-center gap-1.5 text-fg-muted">
          <span>Log:</span>
          <code className="min-w-0 break-all">{failure.log_path}</code>
          <CopyButton
            value={failure.log_path}
            variant="icon"
            label="Copy log path"
          />
        </div>
      )}
    </div>
  );
}
