import { beforeEach, describe, expect, it, vi } from "vitest";

import { listIssues } from "./native";

const client = vi.hoisted(() => ({ apiRequest: vi.fn() }));
vi.mock("./client", () => client);

const lastPath = () => {
  const calls = client.apiRequest.mock.calls;
  return calls[calls.length - 1]?.[0];
};

describe("listIssues refresh", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    client.apiRequest.mockResolvedValue([]);
  });

  it("sends no query string without a filter", async () => {
    await listIssues();
    expect(lastPath()).toBe("/api/v1/native/issues");
  });

  it("adds refresh=1 only when refresh is true", async () => {
    await listIssues({ refresh: true });
    expect(lastPath()).toBe("/api/v1/native/issues?refresh=1");

    await listIssues({ refresh: false });
    expect(lastPath()).toBe("/api/v1/native/issues");
  });

  it("keeps refresh=1 alongside the other filters", async () => {
    await listIssues({ state: ["todo"], refresh: true });
    expect(lastPath()).toBe("/api/v1/native/issues?state=todo&refresh=1");
  });
});
