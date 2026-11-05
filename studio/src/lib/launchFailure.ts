import type { LaunchFailure, NativeIssue } from "@/api/native";

// describeLaunchFailure is the one-line reason a dispatch attempt never became
// a run: "did not start: after_create hook, exit code 128". Exit code is absent
// when the hook produced none (it could not start, or timed out).
export function describeLaunchFailure(
  f: Pick<LaunchFailure, "hook" | "exit_code">,
): string {
  if (!f.hook) return "did not start";
  if (f.exit_code === undefined || f.exit_code === null) {
    return `did not start: ${f.hook} hook (no exit code: could not start or timed out)`;
  }
  return `did not start: ${f.hook} hook, exit code ${f.exit_code}`;
}

// latestLaunchFailure is the newest recorded attempt of a card (the list is
// newest-last), or undefined when it has none.
export function latestLaunchFailure(
  iss: Pick<NativeIssue, "launch_failures">,
): LaunchFailure | undefined {
  const fs = iss.launch_failures;
  return fs && fs.length > 0 ? fs[fs.length - 1] : undefined;
}

// A card in one of these states was dealt with by a person; its old failed
// attempts are history, not something the "did not start" lists should keep
// raising (the Ticket tab still shows them). "blocked" is NOT settled: it is
// where the dispatcher files a card it gave up on after repeated failures.
const SETTLED_STATES: ReadonlySet<string> = new Set(["done", "merged", "cancelled"]);

// issuesWithLaunchFailures keeps the open cards that carry at least one
// recorded failed attempt, the most recently failed first.
export function issuesWithLaunchFailures(issues: NativeIssue[]): NativeIssue[] {
  return issues
    .filter(
      (i) => (i.launch_failures?.length ?? 0) > 0 && !SETTLED_STATES.has(i.state),
    )
    .sort(
      (a, b) =>
        Date.parse(latestLaunchFailure(b)!.at) -
        Date.parse(latestLaunchFailure(a)!.at),
    );
}
