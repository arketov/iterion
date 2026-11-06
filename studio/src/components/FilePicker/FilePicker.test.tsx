// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as client from "@/api/client";
import { useRecentsStore } from "@/store/recents";
import { useServerInfoStore } from "@/store/serverInfo";

import FilePicker from "./FilePicker";

vi.mock("@/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/client")>();
  return {
    ...actual,
    listFilesPage: vi.fn(async () => ({ files: [] })),
    listExampleEntries: vi.fn(async () => []),
  };
});

beforeEach(() => {
  useRecentsStore.setState({ recents: [] });
});
afterEach(() => {
  cleanup();
  useServerInfoStore.setState({ info: null });
});

function renderPicker(onPick = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <FilePicker open onOpenChange={() => {}} onPick={onPick} />
    </QueryClientProvider>,
  );
  return onPick;
}

describe("FilePicker", () => {
  it("names its search field — a placeholder alone is not a label (RGAA 11.1)", () => {
    renderPicker();
    expect(screen.getByRole("textbox", { name: "Search files" })).toBeTruthy();
  });

  it("opens on the project's files when nothing was opened yet, and lists them", async () => {
    vi.mocked(client.listFilesPage).mockResolvedValueOnce({
      files: [{ name: "bots/review/main.bot", size: 1, kind: "bundle" }, { name: "dev-flow.bot", size: 1 }],
    });
    renderPicker();
    expect(await screen.findByText("bots/review/main.bot")).toBeTruthy();
    expect(screen.getByText("dev-flow.bot")).toBeTruthy();
  });

  it("says so when the server cut the file list at its cap", async () => {
    vi.mocked(client.listFilesPage).mockResolvedValueOnce({
      files: [{ name: "a.bot", size: 1 }],
      truncated: true,
      total: 9,
    });
    renderPicker();
    expect(await screen.findByText(/Showing the first 1 files of 9/)).toBeTruthy();
  });

  it("says so under a search too — a hit past the cap is not a hit that does not exist", async () => {
    vi.mocked(client.listFilesPage).mockResolvedValueOnce({
      files: [{ name: "a.bot", size: 1 }],
      truncated: true,
    });
    renderPicker();
    await screen.findByText("a.bot");
    fireEvent.change(screen.getByRole("textbox", { name: "Search files" }), { target: { value: "zzz" } });
    expect(await screen.findByText("No matches.")).toBeTruthy();
    expect(screen.getByText(/Showing the first 1 files/)).toBeTruthy();
  });

  it("leads an empty project to an action, not to an empty block", async () => {
    renderPicker();
    expect(await screen.findByText(/No bots in this project yet\. Create one on the Bots page/)).toBeTruthy();
  });

  it("opens a file outside the listed folders by its path", async () => {
    const onPick = renderPicker();
    useServerInfoStore.setState({ info: { work_dir: "/home/me/proj" } as never });
    fireEvent.change(screen.getByRole("textbox", { name: "Open a file by path" }), {
      target: { value: "/home/me/proj/workflows/dev-flow.bot" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(onPick).toHaveBeenCalledWith("file", "workflows/dev-flow.bot");
  });

  it("refuses a path that can never open, in place, without picking", () => {
    const onPick = renderPicker();
    fireEvent.change(screen.getByRole("textbox", { name: "Open a file by path" }), {
      target: { value: "notes.txt" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(screen.getByRole("alert").textContent).toContain("Only .bot files");
    expect(onPick).not.toHaveBeenCalled();
  });
});
