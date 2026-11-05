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
    listFiles: vi.fn(async () => [
      { name: "dev-flow.bot", size: 10 },
      { name: "bots/review.bot", size: 20 },
    ]),
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
    vi.mocked(client.listFiles).mockRejectedValueOnce(new Error("boom"));
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
});
