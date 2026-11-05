import { describe, expect, it } from "vitest";

import { hookFailureLabel, logHint } from "./retryError";

describe("hookFailureLabel", () => {
  it("is empty when the failure was not a hook's", () => {
    expect(hookFailureLabel({})).toBe("");
  });
  it("names the hook and its exit code", () => {
    expect(hookFailureLabel({ hook: "after_create", hook_exit_code: 127 })).toBe(
      "after_create hook failed (exit code 127)",
    );
  });
  it("explains -1 as no exit status", () => {
    expect(hookFailureLabel({ hook: "before_run", hook_exit_code: -1 })).toContain(
      "no exit code",
    );
  });
});

describe("logHint", () => {
  it("is empty without a path", () => expect(logHint(undefined)).toBe(""));
  it("mentions the path", () => expect(logHint("C:/x/d.log")).toContain("C:/x/d.log"));
});
