import { describe, expect, it } from "vitest";

import { hookFailureLabel, logHint, shellLabel } from "./retryError";

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

describe("shellLabel", () => {
  it("is empty when the failure carries no shell", () => {
    expect(shellLabel()).toBe("");
    expect(shellLabel("", "", "default")).toBe("");
  });
  it("names the shell, its path and why it was picked", () => {
    expect(shellLabel("pwsh", "C:/p/pwsh.exe", "env")).toBe(
      "pwsh C:/p/pwsh.exe, ITERION_SHELL",
    );
    expect(shellLabel("sh", "sh", "default")).toBe("sh, platform default");
  });
  it("keeps a shell asked for but not found, with no path", () => {
    expect(shellLabel("zsh", undefined, "explicit")).toBe("zsh, `shell:` on the hook");
  });
  it("leaves an unknown source out rather than guessing", () => {
    expect(shellLabel("bash", "/bin/bash", "future-source")).toBe("bash /bin/bash");
  });
});
