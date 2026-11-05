import { describe, expect, it } from "vitest";

import type { NativeIssue } from "@/api/native";
import {
  describeLaunchFailure,
  issuesWithLaunchFailures,
  latestLaunchFailure,
} from "./launchFailure";

const iss = (id: string, ats: string[]): NativeIssue =>
  ({
    id,
    launch_failures: ats.map((at) => ({ at, error: "boom", hook: "after_create" })),
  }) as unknown as NativeIssue;

describe("describeLaunchFailure", () => {
  it("names the hook and exit code", () => {
    expect(describeLaunchFailure({ hook: "after_create", exit_code: 128 })).toBe(
      "did not start: after_create hook, exit code 128",
    );
  });
  it("treats a missing exit code as no exit status", () => {
    expect(describeLaunchFailure({ hook: "before_run" })).toContain("no exit code");
  });
  it("is generic without a hook", () => {
    expect(describeLaunchFailure({})).toBe("did not start");
  });
});

describe("issuesWithLaunchFailures", () => {
  it("keeps only cards with failures, newest failure first", () => {
    const a = iss("a", ["2026-11-06T10:00:00Z"]);
    const b = iss("b", ["2026-11-06T09:00:00Z", "2026-11-06T11:00:00Z"]);
    const none = { id: "n" } as NativeIssue;
    expect(issuesWithLaunchFailures([a, none, b]).map((i) => i.id)).toEqual(["b", "a"]);
    expect(latestLaunchFailure(b)?.at).toBe("2026-11-06T11:00:00Z");
    expect(latestLaunchFailure(none)).toBeUndefined();
  });
});
