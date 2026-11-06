// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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

  it("names how many of how many are shown, and how to reach the rest", async () => {
    vi.mocked(client.listFilesPage).mockResolvedValueOnce({
      files: [{ name: "a.bot", size: 1 }],
      truncated: true,
      total: 7,
    });
    renderPanel();
    expect(await screen.findByText(/Showing the first 1 files of 7/)).toBeTruthy();
    expect(screen.getByText(/ITERION_STUDIO_FILES_LIMIT/)).toBeTruthy();
  });

  it("reports a bundle the server could not list instead of dropping it silently", async () => {
    vi.mocked(client.listFilesPage).mockResolvedValueOnce({
      files: [{ name: "a.bot", size: 1 }],
      discovery_errors: [
        { path: "bots/broken", error: "the directory is claimed as a bundle and carries no main.bot", kind: "unopenable" },
      ],
    });
    renderPanel();
    expect(await screen.findByText("Some bundles cannot be opened")).toBeTruthy();
    expect(screen.getByText(/bots\/broken: the directory is claimed as a bundle/)).toBeTruthy();
  });

  it("leads an empty project to an action", async () => {
    vi.mocked(client.listFilesPage).mockResolvedValueOnce({ files: [] });
    renderPanel();
    expect(await screen.findByText(/No bots in this project yet — create a new workflow, open a file by its path/)).toBeTruthy();
  });

  it("tells a .botz archive picked from disk how to install it, not that it is the wrong type", async () => {
    const { container } = renderPanel();
    await screen.findByText("dev-flow.bot");
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [new File(["x"], "pack.botz")] } });
    await waitFor(() => {
      expect(useUIStore.getState().toasts.map((t) => t.message).join(" ")).toContain("Bots → Import");
    });
  });
});
