// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { getOrCreateDocumentStore } from "@/store/document";
import { useTabsStore } from "@/store/tabs";
import { useUIStore } from "@/store/ui";
import * as client from "@/api/client";

import ProjectFilesPanel from "./ProjectFilesPanel";

vi.mock("@/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/client")>();
  return {
    ...actual,
    listFilesPage: vi.fn(async () => ({
      files: [
        { name: "dev-flow.bot", size: 10 },
        { name: "bots/review.bot", size: 20 },
      ],
    })),
    // A project file is read before its tab opens.
    openFile: vi.fn(async (path: string) => ({
      path,
      source: "",
      document: { version: "1", workflows: [] },
      diagnostics: [],
    })),
  };
});

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ProjectFilesPanel />
    </QueryClientProvider>,
  );
}

afterEach(cleanup);
beforeEach(() => {
  window.history.replaceState({}, "", "/");
  // The picker's open flag is shared state (the command palette raises it).
  useUIStore.setState({ filePickerOpen: false, toasts: [] });
});

describe("ProjectFilesPanel", () => {
  it("lists the project's .bot files, an Open… button and an Import copy… button", async () => {
    renderPanel();
    expect(await screen.findByText("dev-flow.bot")).toBeTruthy();
    expect(screen.getByText("review.bot")).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Open…/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Import copy…/ })).toBeTruthy();
  });

  it("answers Ctrl+O with the file picker while the pane is shown", async () => {
    renderPanel();
    await screen.findByText("dev-flow.bot");
    fireEvent.keyDown(window, { key: "o", ctrlKey: true });
    expect(await screen.findByRole("dialog")).toBeTruthy();
    expect(screen.getByText("Open workflow")).toBeTruthy();
  });

  it("opens the file picker when the shared flag is raised (Cmd+K → Open file…)", async () => {
    renderPanel();
    await screen.findByText("dev-flow.bot");
    expect(screen.queryByRole("dialog")).toBeNull();
    act(() => useUIStore.getState().setFilePickerOpen(true));
    expect(await screen.findByRole("dialog")).toBeTruthy();
    expect(screen.getByText("Open workflow")).toBeTruthy();
  });

  it("a project file that is gone from disk opens no tab", async () => {
    vi.mocked(client.openFile).mockRejectedValueOnce(new Error("file not found: dev-flow.bot"));
    renderPanel();
    const before = useTabsStore.getState().tabs.length;
    fireEvent.click(await screen.findByText("dev-flow.bot"));
    await waitFor(() => {
      expect(useUIStore.getState().toasts.map((t) => t.message).join(" ")).toContain(
        "Removed missing file from recents: dev-flow.bot",
      );
    });
    expect(useTabsStore.getState().tabs.length).toBe(before);
  });

  it("a file imported from disk keeps its own name for Save As", async () => {
    const { container } = renderPanel();
    await screen.findByText("dev-flow.bot");
    vi.spyOn(client, "parseSource").mockResolvedValueOnce({
      document: { version: "1", workflows: [{ name: "dev_flow" }] },
      diagnostics: [],
    } as never);
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["workflow dev_flow:\n"], "dev-flow.bot", { type: "text/plain" });
    Object.defineProperty(file, "text", { value: async () => "workflow dev_flow:\n" });
    fireEvent.change(input, { target: { files: [file] } });
    await waitFor(() => {
      const tab = useTabsStore.getState().tabs.find((t) => t.label === "dev-flow.bot");
      expect(tab).toBeTruthy();
      if (!tab) return;
      const store = getOrCreateDocumentStore(tab.id);
      expect(store.getState().currentFilePath).toBeNull();
      expect(store.getState().importedFileName).toBe("dev-flow.bot");
    });
  });

  it("opens a project file in an editor tab bound to its path", async () => {
    renderPanel();
    fireEvent.click(await screen.findByText("dev-flow.bot"));
    await waitFor(() => {
      const tab = useTabsStore.getState().tabs.find((t) => t.params.file === "dev-flow.bot");
      expect(tab).toBeTruthy();
    });
  });

  it("shows an error banner when the file list cannot load", async () => {
    vi.mocked(client.listFilesPage).mockRejectedValueOnce(new Error("boom"));
    renderPanel();
    expect(await screen.findByText("Could not list project files")).toBeTruthy();
  });

  it("refuses a non-.bot file picked from disk without opening a tab", async () => {
    const { container } = renderPanel();
    await screen.findByText("dev-flow.bot");
    const before = useTabsStore.getState().tabs.length;
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["x"], "notes.txt", { type: "text/plain" });
    fireEvent.change(input, { target: { files: [file] } });
    await waitFor(() => {
      expect(useUIStore.getState().toasts.map((t) => t.message).join(" ")).toContain("Only .bot files");
    });
    expect(useTabsStore.getState().tabs.length).toBe(before);
  });

  it("says so when the server cut the list at its cap", async () => {
    vi.mocked(client.listFilesPage).mockResolvedValueOnce({
      files: [{ name: "a.bot", size: 1 }],
      truncated: true,
    });
    renderPanel();
    expect(await screen.findByText(/Showing the first 1 files/)).toBeTruthy();
  });
});
