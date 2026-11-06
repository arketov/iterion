// @vitest-environment jsdom
//
// The + menu's OPEN CONNECTION list can forget a connection: a stale entry (a
// project folder that moved), or one the author no longer wants. Each row has
// an always-visible × beside the name; a click asks first (a dialog that says
// the files on disk stay), then calls the desktop's RemoveConnection and
// re-reads the list. A refusal is shown, and the × never opens the connection.
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { expectNoViolationsStrict, setupMatchMedia } from "@/__tests__/a11y/axeHelpers";

setupMatchMedia();

const browser = vi.hoisted(() => ({ value: false }));
const desktop = vi.hoisted(() => ({
  isFirstRunPending: vi.fn(),
  listConnections: vi.fn(),
  getOpenConnections: vi.fn(),
  getCurrentProject: vi.fn(),
  openConnection: vi.fn(),
  closeConnection: vi.fn(),
  removeConnection: vi.fn(),
}));
vi.mock("@/lib/desktopBridge", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/desktopBridge")>()),
  desktop,
  isBrowserWorkspace: () => browser.value,
  onDesktopEvent: () => () => {},
}));

import WorkspaceShell from "./WorkspaceShell";

const alpha = { id: "a", name: "alpha", dir: "/a", last_opened: "" };
const beta = { id: "b", name: "beta", dir: "C:\\old\\beta", last_opened: "" };
const cloud = {
  id: "c",
  name: "acme",
  kind: "cloud" as const,
  dir: "",
  cloud_url: "https://cloud.example.com",
  last_opened: "",
};

// bootWith lists `all`, opens only alpha, and opens the + menu.
async function bootWith(all: unknown[]) {
  desktop.isFirstRunPending.mockResolvedValue(false);
  desktop.listConnections.mockResolvedValue(all);
  desktop.getOpenConnections.mockResolvedValue(["a"]);
  const view = render(<WorkspaceShell />);
  fireEvent.click(await screen.findByLabelText("Open a connection"));
  return view;
}

afterEach(() => {
  cleanup();
  browser.value = false;
  window.sessionStorage.clear();
  vi.resetAllMocks();
});

describe("removing a connection from the + menu", () => {
  it("puts a × with the connection's name beside every row, not only on hover", async () => {
    await bootWith([alpha, beta, cloud]);

    const remove = await screen.findByRole("button", { name: "Remove connection beta" });
    expect(screen.getByRole("button", { name: "Remove connection acme" })).toBeTruthy();
    // alpha is open: it is a tab, not a row of this list.
    expect(screen.queryByRole("button", { name: "Remove connection alpha" })).toBeNull();
    expect(remove.textContent).toBe("×");
    // Always visible: no hover-only opacity on the control or its row.
    expect(remove.className).not.toMatch(/opacity-0|invisible|hidden/);
    // A sibling of the open button, never inside it (nested-interactive).
    expect(remove.closest("button")).toBe(remove);
    expect(remove.parentElement?.querySelectorAll("button")).toHaveLength(2);
  });

  it("asks first, says the files stay, and removes only after the yes", async () => {
    await bootWith([alpha, beta]);
    desktop.removeConnection.mockResolvedValue(undefined);

    fireEvent.click(await screen.findByRole("button", { name: "Remove connection beta" }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/project files on disk are not deleted/i)).toBeTruthy();
    expect(within(dialog).getByText(/Remove connection .beta.\?/)).toBeTruthy();
    expect(desktop.removeConnection).not.toHaveBeenCalled();

    // The list after the removal no longer holds beta.
    desktop.listConnections.mockResolvedValue([alpha]);
    fireEvent.click(within(dialog).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(desktop.removeConnection).toHaveBeenCalledWith("b"));
    expect(desktop.removeConnection).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole("button", { name: "Remove connection beta" })).toBeNull());
    expect(desktop.openConnection).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("does nothing when the question is cancelled", async () => {
    await bootWith([alpha, beta]);

    fireEvent.click(await screen.findByRole("button", { name: "Remove connection beta" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(desktop.removeConnection).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Remove connection beta" })).toBeTruthy();
  });

  it("does not open the connection when × is clicked", async () => {
    await bootWith([alpha, beta]);

    fireEvent.click(await screen.findByRole("button", { name: "Remove connection beta" }));
    await screen.findByRole("dialog");

    expect(desktop.openConnection).not.toHaveBeenCalled();
  });

  it("tells a cloud connection's person they will be signed out, and nothing on the server goes", async () => {
    await bootWith([alpha, cloud]);

    fireEvent.click(await screen.findByRole("button", { name: "Remove connection acme" }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/signed out of https:\/\/cloud\.example\.com/)).toBeTruthy();
    expect(within(dialog).getByText(/Nothing on the cloud server is deleted/)).toBeTruthy();
  });

  it("shows why a removal was refused, keeps the row, and clears the notice on dismiss", async () => {
    await bootWith([alpha, cloud]);
    desktop.removeConnection.mockRejectedValue("cloud sign-out was not confirmed");

    fireEvent.click(await screen.findByRole("button", { name: "Remove connection acme" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("Could not remove the connection: cloud sign-out was not confirmed×");
    // The list is re-read either way; the refused connection is still there.
    await waitFor(() => expect(desktop.listConnections.mock.calls.length).toBeGreaterThanOrEqual(2));
    expect(screen.getByRole("button", { name: "Remove connection acme" })).toBeTruthy();

    fireEvent.click(within(alert).getByLabelText("Dismiss"));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("names an empty rejection instead of leaving the notice blank", async () => {
    await bootWith([alpha, beta]);
    desktop.removeConnection.mockRejectedValue(undefined);

    fireEvent.click(await screen.findByRole("button", { name: "Remove connection beta" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));

    expect((await screen.findByRole("alert")).textContent).toBe(
      "Could not remove the connection: unknown error×",
    );
  });

  it("clears an earlier notice when the next removal of that connection starts", async () => {
    await bootWith([alpha, beta]);
    desktop.removeConnection.mockRejectedValueOnce("disk full").mockResolvedValueOnce(undefined);

    fireEvent.click(await screen.findByRole("button", { name: "Remove connection beta" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));
    await screen.findByRole("alert");

    desktop.listConnections.mockResolvedValue([alpha]);
    fireEvent.click(await screen.findByRole("button", { name: "Remove connection beta" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(desktop.removeConnection).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("has no axe violations in the open menu (nested-interactive among them)", async () => {
    const { container } = await bootWith([alpha, beta, cloud]);
    await screen.findByRole("button", { name: "Remove connection beta" });

    await expectNoViolationsStrict(container, "WorkspaceShell open-connection menu");
  });

  it("shows no × in the browser workspace, which has no desktop binding", async () => {
    browser.value = true;
    desktop.isFirstRunPending.mockResolvedValue(false);
    desktop.listConnections.mockResolvedValue([alpha, beta]);
    desktop.getOpenConnections.mockResolvedValue([]);
    desktop.getCurrentProject.mockResolvedValue(null);
    render(<WorkspaceShell />);

    await waitFor(() => expect(desktop.listConnections).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: /Remove connection/ })).toBeNull();
  });
});
