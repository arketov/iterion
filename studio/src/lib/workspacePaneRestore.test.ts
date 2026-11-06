// @vitest-environment jsdom
//
// What a window reload (Ctrl+R) puts back in the workspace: which connection
// tab was focused, in which order the tabs stood, and the route inside a pane.
// The recorded values go into an iframe `src`, so the validation is a boundary.
import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  clearPaneLocation,
  paneInitialPath,
  readActivePane,
  readPaneOrder,
  sanitizeRestorePath,
  savePaneLocation,
  sortByPaneOrder,
  studioRelativePath,
  usePaneLocationMemory,
  writeActivePane,
  writePaneOrder,
} from "./workspacePaneRestore";

beforeEach(() => window.sessionStorage.clear());
afterEach(() => {
  delete (globalThis as { __ITERION_SCOPE__?: unknown }).__ITERION_SCOPE__;
  window.history.replaceState({}, "", "/");
});

describe("active tab and tab order", () => {
  it("round-trips the focused connection", () => {
    expect(readActivePane()).toBeNull();
    writeActivePane("b");
    expect(readActivePane()).toBe("b");
    writeActivePane(null);
    expect(readActivePane()).toBeNull();
  });

  it("round-trips the order, dropping duplicates", () => {
    writePaneOrder(["b", "a", "b"]);
    expect(readPaneOrder()).toEqual(["b", "a"]);
  });

  it("reads garbage as empty", () => {
    window.sessionStorage.setItem("iterion.workspace.pane-order", "{not json");
    expect(readPaneOrder()).toEqual([]);
    window.sessionStorage.setItem("iterion.workspace.pane-order", '{"a":1}');
    expect(readPaneOrder()).toEqual([]);
    window.sessionStorage.setItem("iterion.workspace.pane-order", '["a",1,null,"b"]');
    expect(readPaneOrder()).toEqual(["a", "b"]);
  });

  it("survives storage that throws", () => {
    const spy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    try {
      expect(readActivePane()).toBeNull();
      expect(readPaneOrder()).toEqual([]);
      expect(paneInitialPath("a")).toBe("/");
    } finally {
      spy.mockRestore();
    }
  });
});

describe("sortByPaneOrder", () => {
  it("puts the known ids in the recorded order and the unknown ones after", () => {
    expect(sortByPaneOrder(["c", "a", "x", "b"], ["b", "a", "c"])).toEqual(["b", "a", "c", "x"]);
  });
  it("ignores recorded ids that are no longer open", () => {
    expect(sortByPaneOrder(["a"], ["z", "a", "y"])).toEqual(["a"]);
  });
  it("keeps the incoming order when nothing was recorded", () => {
    expect(sortByPaneOrder(["b", "a"], [])).toEqual(["b", "a"]);
  });
});

describe("sanitizeRestorePath", () => {
  it.each([
    ["/", "/"],
    ["/editor", "/editor"],
    ["/editor?file=bots%2Fa.bot", "/editor?file=bots%2Fa.bot"],
    ["/runs/abc#logs", "/runs/abc#logs"],
  ])("accepts %s", (raw, expected) => {
    expect(sanitizeRestorePath(raw)).toBe(expected);
  });

  it.each([
    ["", "empty"],
    ["editor", "no leading slash"],
    ["//evil.example/x", "protocol-relative"],
    ["https://evil.example/", "scheme"],
    ["/a\\b", "backslash"],
    ["/a\u0000b", "control character"],
    ["/" + "a".repeat(2100), "too long"],
  ])("refuses %j (%s)", (raw) => {
    expect(sanitizeRestorePath(raw)).toBeNull();
  });

  it("refuses a non-string", () => {
    expect(sanitizeRestorePath(42)).toBeNull();
    expect(sanitizeRestorePath(null)).toBeNull();
  });
});

describe("a pane's recorded route", () => {
  it("defaults to the home", () => {
    expect(paneInitialPath("a")).toBe("/");
  });

  it("returns what was saved, per connection", () => {
    savePaneLocation("a", "/editor?file=x.bot");
    savePaneLocation("b", "/runs");
    expect(paneInitialPath("a")).toBe("/editor?file=x.bot");
    expect(paneInitialPath("b")).toBe("/runs");
  });

  it("does not save, and does not return, a route that fails validation", () => {
    savePaneLocation("a", "//evil.example");
    expect(paneInitialPath("a")).toBe("/");
    window.sessionStorage.setItem("iterion.workspace.pane-location:a", "https://evil.example/");
    expect(paneInitialPath("a")).toBe("/");
  });

  it("is forgotten when the tab is closed", () => {
    savePaneLocation("a", "/runs");
    clearPaneLocation("a");
    expect(paneInitialPath("a")).toBe("/");
  });
});

describe("studioRelativePath", () => {
  const loc = (pathname: string, search = "", hash = "") => ({ pathname, search, hash });

  it("strips the studio base, keeping query and hash", () => {
    expect(studioRelativePath(loc("/x/p1/studio/editor", "?file=a.bot", "#n"), "/x/p1/studio")).toBe(
      "/editor?file=a.bot#n",
    );
  });
  it("the base itself is the home", () => {
    expect(studioRelativePath(loc("/x/p1/studio"), "/x/p1/studio")).toBe("/");
    expect(studioRelativePath(loc("/x/p1/studio/"), "/x/p1/studio")).toBe("/");
  });
  it("is null outside the studio (login, invitations, share links)", () => {
    expect(studioRelativePath(loc("/x/p1/login"), "/x/p1/studio")).toBeNull();
    expect(studioRelativePath(loc("/x/p1/studioX/a"), "/x/p1/studio")).toBeNull();
  });
  it("drops a spent hand-off ticket from the query", () => {
    expect(studioRelativePath(loc("/x/p1/studio/", "?handoff=t0k&tab=1"), "/x/p1/studio")).toBe("/?tab=1");
    expect(studioRelativePath(loc("/x/p1/studio/", "?handoff=t0k"), "/x/p1/studio")).toBe("/");
  });
});

describe("usePaneLocationMemory", () => {
  it("records the route of a workspace pane", () => {
    (globalThis as { __ITERION_SCOPE__?: string }).__ITERION_SCOPE__ = "/x/p1";
    window.history.replaceState({}, "", "/x/p1/studio/editor?file=a.bot");

    renderHook(() => usePaneLocationMemory());

    expect(paneInitialPath("p1")).toBe("/editor?file=a.bot");
  });

  it("records nothing outside a pane", () => {
    window.history.replaceState({}, "", "/studio/editor");

    renderHook(() => usePaneLocationMemory());

    expect(window.sessionStorage.length).toBe(0);
  });
});
