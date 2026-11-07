// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./run", () => ({ disposeRunStore: vi.fn() }));
vi.mock("./document", () => ({ disposeDocumentStore: vi.fn() }));
vi.mock("./selection", () => ({ disposeSelectionStore: vi.fn() }));

// Each workspace pane is its own JS realm (an iframe) over the SAME origin,
// hence the same localStorage. loadPane re-imports the stores under a given
// __ITERION_SCOPE__ to simulate one realm: fresh module state, shared storage.
async function loadPane(scope: string) {
  vi.resetModules();
  (globalThis as { __ITERION_SCOPE__?: string }).__ITERION_SCOPE__ = scope;
  const tabs = await import("./tabs");
  const recents = await import("./recents");
  return { tabs: tabs.useTabsStore, recents: recents.useRecentsStore };
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  delete (globalThis as { __ITERION_SCOPE__?: string }).__ITERION_SCOPE__;
});

describe("pane-scoped persistence", () => {
  it("keeps each pane's editor tabs when another pane writes its own", async () => {
    const a = await loadPane("/x/a");
    a.tabs.getState().setCurrentProjectKey("projA");
    const aTab = a.tabs.getState().openTab("editor", { file: "a.bot" });

    const b = await loadPane("/x/b");
    // A fresh pane must not inherit the tabs another pane persisted.
    expect(b.tabs.getState().tabs).toEqual([]);
    b.tabs.getState().setCurrentProjectKey("projB");
    b.tabs.getState().openTab("editor", { file: "b.bot" });

    const reloadedA = await loadPane("/x/a");
    expect(reloadedA.tabs.getState().tabs.map((t) => t.id)).toEqual([aTab]);
    expect(reloadedA.tabs.getState().activeEditorTabId).toBe(aTab);
  });

  it("keeps each pane's recent files when another pane writes its own", async () => {
    const a = await loadPane("/x/a");
    a.recents.getState().pushRecent("/projA/a.bot");

    const b = await loadPane("/x/b");
    expect(b.recents.getState().recents).toEqual([]);
    b.recents.getState().pushRecent("/projB/b.bot");
    b.recents.getState().removeRecent("/projA/a.bot");

    const reloadedA = await loadPane("/x/a");
    expect(reloadedA.recents.getState().recents).toEqual(["/projA/a.bot"]);
  });
});
