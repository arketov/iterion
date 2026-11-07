// @vitest-environment jsdom
//
// Ctrl+R reloads the whole workspace window. The shell used to come back with
// its first connection tab focused and every pane on the studio home; it now
// restores the focused tab, the tab order and each pane's route from this
// window's sessionStorage (see lib/workspacePaneRestore).
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const desktop = vi.hoisted(() => ({
  isFirstRunPending: vi.fn(),
  listConnections: vi.fn(),
  getOpenConnections: vi.fn(),
  getCurrentProject: vi.fn(),
  openConnection: vi.fn(),
  closeConnection: vi.fn(),
  pickProjectDirectory: vi.fn(),
  addProjectSilently: vi.fn(),
}));
const mode = vi.hoisted(() => ({ browser: false }));
vi.mock("@/lib/desktopBridge", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/desktopBridge")>()),
  desktop,
  isBrowserWorkspace: () => mode.browser,
  onDesktopEvent: () => () => {},
}));

import { paneInitialPath, readActivePane, readPaneOrder } from "@/lib/workspacePaneRestore";

import WorkspaceShell from "./WorkspaceShell";

const projects = [
  { id: "a", name: "alpha", dir: "/a", runtime_ready: true },
  { id: "b", name: "beta", dir: "/b", runtime_ready: true },
];

function boot(open: string[]) {
  desktop.isFirstRunPending.mockResolvedValue(false);
  desktop.listConnections.mockResolvedValue(projects);
  desktop.getOpenConnections.mockResolvedValue(open);
  desktop.getCurrentProject.mockResolvedValue(null);
  desktop.openConnection.mockResolvedValue(undefined);
  desktop.closeConnection.mockResolvedValue(undefined);
  render(<WorkspaceShell />);
}

const pane = (name: string) => screen.findByTitle(name) as Promise<HTMLIFrameElement>;
const visible = (el: HTMLElement) => el.style.display === "block";

beforeEach(() => {
  mode.browser = false;
  window.sessionStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

describe("reloading the desktop workspace window", () => {
  it("first load: the first open connection is focused, panes open on the home", async () => {
    boot(["a", "b"]);

    const a = await pane("alpha");
    const b = await pane("beta");
    expect(visible(a)).toBe(true);
    expect(visible(b)).toBe(false);
    expect(a.getAttribute("src")).toBe("/x/a/studio/");
  });

  it("restores the focused connection tab", async () => {
    window.sessionStorage.setItem("iterion.workspace.active", "b");
    boot(["a", "b"]);

    expect(visible(await pane("beta"))).toBe(true);
    expect(visible(await pane("alpha"))).toBe(false);
  });

  it("restores the saved tab order over the order the registry lists", async () => {
    window.sessionStorage.setItem("iterion.workspace.pane-order", JSON.stringify(["b", "a"]));
    boot(["a", "b"]);

    await pane("alpha");
    const names = [...screen.getByTestId("workspace-tab-bar").querySelectorAll("span.truncate")].map(
      (el) => el.textContent,
    );
    expect(names).toEqual(["beta", "alpha"]);
  });

  it("ignores a recorded tab that is no longer open", async () => {
    window.sessionStorage.setItem("iterion.workspace.active", "gone");
    boot(["a", "b"]);

    expect(visible(await pane("alpha"))).toBe(true);
  });

  it("reopens a pane on the route it was left on", async () => {
    window.sessionStorage.setItem(
      "iterion.workspace.pane-location:a",
      "/editor?file=bots%2Fdev-flow.bot",
    );
    boot(["a", "b"]);

    expect((await pane("alpha")).getAttribute("src")).toBe(
      "/x/a/studio/editor?file=bots%2Fdev-flow.bot",
    );
    expect((await pane("beta")).getAttribute("src")).toBe("/x/b/studio/");
  });

  it("falls back to the home for a recorded route that is not a studio route", async () => {
    window.sessionStorage.setItem("iterion.workspace.pane-location:a", "//evil.example/x");
    boot(["a"]);

    expect((await pane("alpha")).getAttribute("src")).toBe("/x/a/studio/");
  });

  it("records the focused tab and the order as the author works", async () => {
    boot(["a", "b"]);
    await pane("alpha");
    await waitFor(() => expect(readPaneOrder()).toEqual(["a", "b"]));

    fireEvent.click(screen.getByText("beta"));

    await waitFor(() => expect(readActivePane()).toBe("b"));
  });

  it("does not overwrite the record before the first load has read it", async () => {
    window.sessionStorage.setItem("iterion.workspace.active", "b");
    window.sessionStorage.setItem("iterion.workspace.pane-order", JSON.stringify(["b", "a"]));
    desktop.isFirstRunPending.mockReturnValue(new Promise(() => {})); // bootstrap still pending
    render(<WorkspaceShell />);

    expect(readActivePane()).toBe("b");
    expect(readPaneOrder()).toEqual(["b", "a"]);
  });

  it("a closed tab reopens on the home", async () => {
    window.sessionStorage.setItem("iterion.workspace.pane-location:a", "/runs");
    boot(["a", "b"]);
    await pane("alpha");

    fireEvent.click(screen.getByLabelText("Close alpha"));

    await waitFor(() => expect(paneInitialPath("a")).toBe("/"));
  });
});

describe("reloading the browser workspace window", () => {
  beforeEach(() => {
    mode.browser = true;
  });

  it("this window's focused project beats the server-wide current project", async () => {
    // Another browser window switched the server's current project to "a".
    window.sessionStorage.setItem("iterion.workspace.active", "b");
    desktop.isFirstRunPending.mockResolvedValue(false);
    desktop.listConnections.mockResolvedValue(projects);
    desktop.getOpenConnections.mockResolvedValue(["a", "b"]);
    desktop.getCurrentProject.mockResolvedValue(projects[0]);
    desktop.openConnection.mockResolvedValue(undefined);
    render(<WorkspaceShell />);

    expect(visible(await pane("beta"))).toBe(true);
    expect(desktop.openConnection).toHaveBeenCalledWith("b");
  });

  it("without a recorded focus, the server's current project is still the default", async () => {
    desktop.isFirstRunPending.mockResolvedValue(false);
    desktop.listConnections.mockResolvedValue(projects);
    desktop.getOpenConnections.mockResolvedValue(["a", "b"]);
    desktop.getCurrentProject.mockResolvedValue(projects[1]);
    desktop.openConnection.mockResolvedValue(undefined);
    render(<WorkspaceShell />);

    expect(visible(await pane("beta"))).toBe(true);
  });
});
