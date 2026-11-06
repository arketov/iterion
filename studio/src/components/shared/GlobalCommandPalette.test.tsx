// @vitest-environment jsdom
//
// Cmd+K on the editor's start pane (no editor tab open). The editor route owns
// a Canvas-scoped palette, but with no tab there is no Canvas mounted, so the
// global palette must answer — and its "Open file…" must raise the file picker
// the start pane renders.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useTabsStore } from "@/store/tabs";
import { useUIStore } from "@/store/ui";

import GlobalCommandPalette from "./GlobalCommandPalette";

vi.mock("@/hooks/useRuns", () => ({ useRuns: () => ({ runs: [] }) }));
vi.mock("@/hooks/useActiveRepo", () => ({
  useActiveRepo: () => ({ repos: [], choose: vi.fn(), enabled: false }),
}));

function pressCmdK() {
  fireEvent.keyDown(window, { key: "k", metaKey: true });
}

beforeEach(() => {
  window.history.replaceState({}, "", "/");
  useTabsStore.setState({ tabs: [], activeEditorTabId: null, currentProjectKey: null });
  useUIStore.setState({ commandPaletteOpen: false, filePickerOpen: false });
});
afterEach(cleanup);

describe("GlobalCommandPalette — Cmd+K", () => {
  it("opens on an ordinary route", () => {
    render(<GlobalCommandPalette />);
    pressCmdK();
    expect(useUIStore.getState().commandPaletteOpen).toBe(true);
  });

  it("opens on the editor's start pane, where no tab and so no Canvas is mounted", () => {
    window.history.replaceState({}, "", "/editor");
    render(<GlobalCommandPalette />);
    pressCmdK();
    expect(useUIStore.getState().commandPaletteOpen).toBe(true);
  });

  it("leaves the key to the Canvas palette while an editor tab is on screen", () => {
    window.history.replaceState({}, "", "/editor");
    const id = useTabsStore.getState().newEditorTab();
    expect(useTabsStore.getState().activeEditorTabId).toBe(id);
    render(<GlobalCommandPalette />);
    pressCmdK();
    expect(useUIStore.getState().commandPaletteOpen).toBe(false);
  });
});

describe("GlobalCommandPalette — Open file…", () => {
  it("raises the file picker flag from the editor's start pane", async () => {
    window.history.replaceState({}, "", "/editor");
    render(<GlobalCommandPalette />);
    act(() => useUIStore.getState().setCommandPaletteOpen(true));

    fireEvent.click(await screen.findByText("Open file…"));

    expect(useUIStore.getState().filePickerOpen).toBe(true);
    expect(window.location.pathname).toBe("/editor");
  });

  it("takes the author to the editor first when asked from another page", async () => {
    window.history.replaceState({}, "", "/runs");
    render(<GlobalCommandPalette />);
    act(() => useUIStore.getState().setCommandPaletteOpen(true));

    fireEvent.click(await screen.findByText("Open file…"));

    expect(useUIStore.getState().filePickerOpen).toBe(true);
    expect(window.location.pathname).toBe("/editor");
  });
});
