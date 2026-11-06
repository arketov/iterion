// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";

import type { BotEntryWithSchema } from "@/api/bots";
import { useBotsStore } from "@/store/bots";
import { useRecentsStore } from "@/store/recents";
import { useTabsStore } from "@/store/tabs";

import RecentFilesPanel from "./RecentFilesPanel";

// Not under test; rendering it bare would be harmless but we keep the test
// hermetic (no catalog fetch on mount).
vi.mock("@/components/Catalog/BotCatalogDialog", () => ({
  BotCatalogDialog: () => null,
}));

const INITIAL = useBotsStore.getState();

function bot(over: Partial<BotEntryWithSchema> & { name: string }): BotEntryWithSchema {
  return {
    path: `/proj/${over.name}`,
    origin: "catalog",
    enabled: true,
    ...over,
  } as BotEntryWithSchema;
}

// The panel asks the store to load when it has not; the tests set the store
// themselves, so the load is a no-op that cannot reach the network.
function setStore(state: Partial<ReturnType<typeof useBotsStore.getState>>) {
  useBotsStore.setState({ fetch: async () => {}, ...state });
}

afterEach(() => {
  cleanup();
  useBotsStore.setState(INITIAL);
  useRecentsStore.setState({ recents: [] });
});
beforeEach(() => {
  window.history.replaceState({}, "", "/");
  useRecentsStore.setState({ recents: [] });
});

describe("RecentFilesPanel — the Bots block is the Bots page's own list", () => {
  it("shows no number until the list has loaded", () => {
    setStore({ bots: null, extraBots: [] });
    render(<RecentFilesPanel />);
    expect(screen.getByText("Bots")).toBeTruthy();
    expect(screen.queryByText(/^Bots \(/)).toBeNull();
  });

  it("counts the rows it lists: catalog bots plus the project's single files", () => {
    setStore({
      bots: [
        bot({ name: "review", display_name: "Revi", rel_path: "bots/review", is_bundle: true }),
        bot({ name: "solo", rel_path: "bots/solo.bot", is_bundle: false }),
      ],
      extraBots: [bot({ name: "dev-flow", origin: "file", rel_path: "dev-flow.bot" })],
    });
    render(<RecentFilesPanel />);
    // No recents: the block opens by itself.
    expect(screen.getByText("Bots (3)")).toBeTruthy();
    expect(screen.getByText("Revi")).toBeTruthy();
    expect(screen.getByText("solo")).toBeTruthy();
    expect(screen.getByText("dev-flow")).toBeTruthy();
  });

  // A bundle named like a former built-in recipe is an ordinary bot: the
  // header and the row come from the same store, so they cannot disagree.
  it("a bundle named feature-dev is one row and counted once", () => {
    setStore({
      bots: [bot({ name: "feature-dev", rel_path: "bots/feature-dev", is_bundle: true })],
      extraBots: [],
    });
    render(<RecentFilesPanel />);
    expect(screen.getByText("Bots (1)")).toBeTruthy();
    expect(screen.getAllByText("feature-dev")).toHaveLength(1);
  });

  it("hides the block when the project has no bots", () => {
    setStore({ bots: [], extraBots: [] });
    render(<RecentFilesPanel />);
    expect(screen.queryByText(/^Bots/)).toBeNull();
    expect(screen.getByText(/No recent files yet — create a new workflow\./)).toBeTruthy();
  });

  it("opens a bundle's main.bot in an editor tab bound to the path", () => {
    setStore({
      bots: [bot({ name: "review", rel_path: "bots/review", is_bundle: true })],
      extraBots: [],
    });
    render(<RecentFilesPanel />);
    fireEvent.click(screen.getByText("review"));
    const tab = useTabsStore.getState().tabs.find((t) => t.params.file === "bots/review/main.bot");
    expect(tab).toBeTruthy();
    expect(useRecentsStore.getState().recents).toContain("bots/review/main.bot");
    expect(window.location.pathname).toBe("/editor");
  });

  it("opens a single project file by its workspace path", () => {
    setStore({
      bots: [],
      extraBots: [bot({ name: "dev-flow", origin: "file", rel_path: "dev-flow.bot" })],
    });
    render(<RecentFilesPanel />);
    fireEvent.click(screen.getByText("dev-flow"));
    expect(useTabsStore.getState().tabs.some((t) => t.params.file === "dev-flow.bot")).toBe(true);
  });

  it("sends a bot with no file in this workspace to its bot page", () => {
    setStore({
      bots: [bot({ name: "team-bot", origin: "tenant" })],
      extraBots: [],
    });
    render(<RecentFilesPanel />);
    fireEvent.click(screen.getByText("team-bot"));
    expect(window.location.pathname).toBe("/bots/team-bot");
  });

  it("says the catalog could not load, in a banner", () => {
    setStore({ bots: null, extraBots: [], error: "boom" });
    render(<RecentFilesPanel />);
    expect(screen.getByText("Bot catalog unavailable")).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
  });
});
