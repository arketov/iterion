// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";

import type { BotEntryWithSchema } from "@/api/bots";
import { useBotsStore } from "@/store/bots";
import { useServerInfoStore } from "@/store/serverInfo";
import { useTabsStore } from "@/store/tabs";

import { BotCatalogDialog } from "./BotCatalogDialog";

vi.mock("@/auth/AuthContext", () => ({ useAuth: () => ({ activeTeam: null }) }));

const BOTS_STATE = useBotsStore.getState();
const SERVER_STATE = useServerInfoStore.getState();

const CATALOG = {
  name: "review",
  display_name: "Revi",
  path: "/w/bots/review",
  rel_path: "bots/review",
  is_bundle: true,
  origin: "catalog",
  enabled: true,
} as BotEntryWithSchema;

const FILE = {
  name: "dev-flow",
  origin: "file",
  path: "/w/dev-flow.bot",
  rel_path: "dev-flow.bot",
  enabled: true,
} as BotEntryWithSchema;

function seed(bots: BotEntryWithSchema[], extra: BotEntryWithSchema[]) {
  useBotsStore.setState({
    bots,
    extraBots: extra,
    loading: false,
    error: null,
    fetch: () => Promise.resolve(),
    refetch: () => Promise.resolve(),
  });
}

beforeEach(() => {
  useServerInfoStore.setState({ info: { mode: "local" } as never });
});
afterEach(() => {
  cleanup();
  useBotsStore.setState(BOTS_STATE);
  useServerInfoStore.setState(SERVER_STATE);
});

describe("BotCatalogDialog (Manage) lists the same set as the Bots page", () => {
  it("shows the project's single files too, without a switch, and counts them", () => {
    seed([CATALOG], [FILE]);
    render(<BotCatalogDialog open onOpenChange={() => {}} />);
    expect(screen.getByText(/2 bots: 1 can be switched on or off here/)).toBeTruthy();
    const files = screen.getByRole("region", { name: "Project files" });
    expect(within(files).getByText("dev-flow")).toBeTruthy();
    // A file has no manifest, so no overlay to flip: no switch in its row.
    expect(within(files).queryByRole("switch")).toBeNull();
    expect(within(files).getByText("Always on")).toBeTruthy();
    // The catalog bot keeps its switch.
    expect(screen.getAllByRole("switch")).toHaveLength(1);
  });

  it("opens a project file in the editor", () => {
    seed([], [FILE]);
    const onOpenChange = vi.fn();
    render(<BotCatalogDialog open onOpenChange={onOpenChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Open in editor: dev-flow" }));
    expect(useTabsStore.getState().tabs.some((t) => t.params.file === "dev-flow.bot")).toBe(true);
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("an empty project says what to do, in the words the other pickers use", () => {
    seed([], []);
    render(<BotCatalogDialog open onOpenChange={() => {}} />);
    expect(screen.getByText(/No bots available\. Create or import one on the Bots page/)).toBeTruthy();
    expect(screen.queryByText(/No bots discovered/)).toBeNull();
  });
});
