// @vitest-environment jsdom
//
// A project moved to another folder opens with an empty board; the dispatcher's
// empty state offers to carry the old data over, and only when the server found
// the old project and the new store is still empty.
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { RelocationOffer as Offer } from "@/api/projects";
import { setupMatchMedia } from "@/__tests__/a11y/axeHelpers";

const getRelocationOffer = vi.fn();
const relocateProject = vi.fn();
vi.mock("@/api/projects", () => ({
  getRelocationOffer: (...args: unknown[]) => getRelocationOffer(...args),
  relocateProject: (...args: unknown[]) => relocateProject(...args),
}));

import RelocationOffer from "./RelocationOffer";

setupMatchMedia();

const OLD_DIR = "C:\Users\me\OneDrive\itterion-test";

function offer(overrides: Partial<Offer> = {}): Offer {
  return {
    sources: [
      {
        id: "old",
        name: "itterion-test",
        dir: OLD_DIR,
        store_dir: "C:\store\old",
        cards: 12,
        runs: 3,
        has_dispatcher_config: true,
      },
    ],
    destination_empty: true,
    ...overrides,
  };
}

beforeEach(() => {
  getRelocationOffer.mockReset();
  relocateProject.mockReset();
});
afterEach(() => cleanup());

describe("dispatcher relocation offer", () => {
  it("offers to move the data from the old path and moves it on click", async () => {
    getRelocationOffer.mockResolvedValue(offer());
    relocateProject.mockResolvedValue({ copied: { cards: 12, runs: 3 }, dispatcher: "stopped" });
    const onMoved = vi.fn();
    render(<RelocationOffer projectId="p-new" onMoved={onMoved} />);

    const button = await screen.findByRole("button", { name: `Move data from ${OLD_DIR}` });
    expect(getRelocationOffer).toHaveBeenCalledWith("p-new");
    expect(screen.getByText(/12 cards, 3 runs/)).toBeTruthy();

    fireEvent.click(button);
    await waitFor(() => expect(onMoved).toHaveBeenCalledTimes(1));
    expect(relocateProject).toHaveBeenCalledWith("p-new", "old");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows the server's refusal and keeps the offer", async () => {
    getRelocationOffer.mockResolvedValue(offer());
    relocateProject.mockRejectedValue(new Error("destination already holds data"));
    const onMoved = vi.fn();
    render(<RelocationOffer projectId="p-new" onMoved={onMoved} />);

    fireEvent.click(await screen.findByRole("button", { name: /Move data from/ }));
    expect(await screen.findByText(/destination already holds data/)).toBeTruthy();
    expect(onMoved).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /Move data from/ })).toBeTruthy();
  });

  it("offers nothing when no old project was found", async () => {
    getRelocationOffer.mockResolvedValue(offer({ sources: [] }));
    render(<RelocationOffer projectId="p-new" onMoved={() => {}} />);
    await waitFor(() => expect(getRelocationOffer).toHaveBeenCalled());
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("offers nothing when the project's own store already holds data", async () => {
    getRelocationOffer.mockResolvedValue(offer({ destination_empty: false }));
    render(<RelocationOffer projectId="p-new" onMoved={() => {}} />);
    await waitFor(() => expect(getRelocationOffer).toHaveBeenCalled());
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("offers nothing when the lookup fails", async () => {
    getRelocationOffer.mockRejectedValue(new Error("boom"));
    render(<RelocationOffer projectId="p-new" onMoved={() => {}} />);
    await waitFor(() => expect(getRelocationOffer).toHaveBeenCalled());
    expect(screen.queryByRole("button")).toBeNull();
  });
});
