import type { RetryView } from "@/api/dispatcher";

// hookFailureLabel names the lifecycle hook that failed and how, or "" when
// the retry's last failure was not a hook's. exit code -1 means the hook
// produced no exit status: it could not start (e.g. missing shell) or timed out.
export function hookFailureLabel(
  r: Pick<RetryView, "hook" | "hook_exit_code">,
): string {
  if (!r.hook) return "";
  const code = r.hook_exit_code;
  if (code === undefined || code === null) return `${r.hook} hook failed`;
  if (code < 0) return `${r.hook} hook failed (no exit code: could not start or timed out)`;
  return `${r.hook} hook failed (exit code ${code})`;
}

// logHint is the one-line pointer to the full log, "" when its path is unknown.
export function logHint(logPath: string | undefined): string {
  return logPath ? `Full output and chronology: ${logPath}` : "";
}
