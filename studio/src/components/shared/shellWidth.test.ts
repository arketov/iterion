import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// Pinned as a source contract: mounting WorkspaceShell / AppShell drags the
// whole app (stores, router, sidebar) into a test, and the contract is the
// class list, not one render. `w-screen` is 100vw, which on Windows includes
// the classic vertical scrollbar, so a full-window shell shifts the layout
// by the scrollbar width and grows a horizontal scroller. Shells size to
// their container with `w-full min-w-0` instead.
const shells = [
  "src/components/shared/AppShell.tsx",
  "src/workspace/WorkspaceShell.tsx",
  "src/components/Runs/runView/RunViewLoadStates.tsx",
];

describe("full-window shells width", () => {
  it.each(shells)("%s does not size itself with w-screen", (file) => {
    const src = readFileSync(resolve(process.cwd(), file), "utf8");
    expect(src).not.toMatch(/(^|[\s"'`])w-screen([\s"'`]|$)/);
  });
});
