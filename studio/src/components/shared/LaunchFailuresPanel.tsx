import { useQuery } from "@tanstack/react-query";
import { Link } from "wouter";

import { listIssues } from "@/api/native";
import { LaunchFailureDetails } from "@/components/shared/LaunchFailureDetails";
import {
  issuesWithLaunchFailures,
  latestLaunchFailure,
} from "@/lib/launchFailure";

// useLaunchFailureIssues lists the board cards whose dispatch attempts never
// became a run. Read from the card on disk, so it answers the same after the
// dispatcher was stopped or the studio restarted. A server without the native
// board (or any read failure) simply yields an empty list.
export function useLaunchFailureIssues() {
  return useQuery({
    queryKey: ["launch-failure-issues"],
    queryFn: async () => issuesWithLaunchFailures(await listIssues()),
    refetchInterval: 5000,
    retry: false,
  });
}

// LaunchFailuresPanel is the "did not start" list for the Runs and Dispatcher
// pages: a dispatch that failed before its run existed is not a run, so no
// run list can show it — the card is where it is recorded. Renders nothing
// when there is none.
export function LaunchFailuresPanel({ className = "" }: { className?: string }) {
  const { data } = useLaunchFailureIssues();
  if (!data || data.length === 0) return null;
  return (
    <section
      className={`rounded border border-danger/40 bg-danger-soft ${className}`.trim()}
      aria-label="Dispatches that did not start"
      data-testid="launch-failures-panel"
    >
      <header className="px-4 py-2 border-b border-danger/30 text-sm font-semibold text-danger-fg flex flex-wrap items-baseline gap-x-2">
        <span>✗ Did not start ({data.length})</span>
        <span className="text-micro font-normal">
          dispatches that failed before a run was created — no run exists, the
          reason is kept on the card until its next successful launch
        </span>
      </header>
      <ul className="divide-y divide-danger/20">
        {data.map((iss) => {
          const f = latestLaunchFailure(iss)!;
          return (
            <li key={iss.id} className="px-4 py-2 space-y-1">
              <div className="flex items-center gap-2 text-xs">
                <Link
                  href={`/board?focus=${encodeURIComponent(iss.id)}`}
                  className="font-mono text-accent-text hover:underline"
                  title="Open the card on the board"
                >
                  {iss.identifier || iss.id}
                </Link>
                <span className="truncate text-fg-default">{iss.title}</span>
                {iss.launch_failures!.length > 1 && (
                  <span className="ml-auto text-fg-muted whitespace-nowrap">
                    {iss.launch_failures!.length} attempts
                  </span>
                )}
              </div>
              <LaunchFailureDetails failure={f} />
            </li>
          );
        })}
      </ul>
    </section>
  );
}
