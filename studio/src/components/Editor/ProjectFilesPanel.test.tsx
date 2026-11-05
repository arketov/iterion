// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { useTabsStore } from "@/store/tabs";
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
  it("lists the project's .bot files and an Open file… button", async () => {
    renderPanel();
    expect(await screen.findByText("dev-flow.bot")).toBeTruthy();
    expect(screen.getByText("review.bot")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open file…/ })).toBeTruthy();
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
});
