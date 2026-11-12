// @vitest-environment jsdom

import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useBoardData } from "./useBoardData";

const api = vi.hoisted(() => ({ getBoard: vi.fn(), listIssues: vi.fn() }));
vi.mock("@/api/native", () => api);

function render() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return renderHook(() => useBoardData(), { wrapper });
}

const refreshFlags = () =>
  api.listIssues.mock.calls.map((call) => Boolean(call[0]?.refresh));

describe("useBoardData refresh", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.getBoard.mockResolvedValue({ id: "b1" });
    api.listIssues.mockResolvedValue([]);
  });

  it("asks the server to rescan the disk only for forceRefresh, and only once", async () => {
    const { result } = render();
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(() => result.current.forceRefresh());
    await act(() => result.current.refresh());

    expect(refreshFlags()).toEqual([false, true, false]);
  });

  it("shows a card that only a disk rescan returns", async () => {
    const card = { id: "i1", title: "written by the CLI" };
    api.listIssues.mockImplementation(async (filter) => (filter?.refresh ? [card] : []));
    const { result } = render();
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.issues).toEqual([]);

    await act(() => result.current.forceRefresh());

    expect(result.current.issues).toEqual([card]);
  });

  it.each(["refresh", "forceRefresh"] as const)("%s clears an action error", async (name) => {
    const { result } = render();
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => result.current.setError("drop failed"));
    expect(result.current.error).toBe("drop failed");

    await act(() => result.current[name]());

    expect(result.current.error).toBeNull();
  });
});
