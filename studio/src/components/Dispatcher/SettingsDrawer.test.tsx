// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { DispatcherConfig } from "@/api/dispatcher";

import SettingsDrawer from "./SettingsDrawer";

const api = vi.hoisted(() => ({
  getConfig: vi.fn(),
  saveConfig: vi.fn(),
  applyDefaults: vi.fn(),
}));

vi.mock("@/api/dispatcher", () => api);
// The drawer chrome is not under test: render its body and footer inline.
vi.mock("@/components/ui/Drawer", () => ({
  Drawer: ({
    open,
    children,
    footer,
  }: {
    open: boolean;
    children: React.ReactNode;
    footer?: React.ReactNode;
  }) =>
    open ? (
      <div>
        <div>{children}</div>
        <div>{footer}</div>
      </div>
    ) : null,
}));
// The bot picker's data source: an empty, already-loaded catalogue.
vi.mock("@/store/bots", () => ({
  useAllBots: () => [],
  useBotsStore: (select: (s: { bots: unknown[]; fetch: () => void }) => unknown) =>
    select({ bots: [], fetch: () => {} }),
}));

// A saved config that carries every field the form has no — or only a recently
// added — control for. Saving the form must hand all of them back unchanged.
const saved: DispatcherConfig = {
  name: "iterion-default",
  workflow: "",
  require_assignee: true,
  bots: { paths: ["/proj/bots", "/proj/examples"] },
  assignee_dispatch: { "feature-dev": { vars: { task: "{{issue.title}}" } } },
  ticket_router: { enabled: true, implement_bot: "feature-dev" },
  limits: { max_cost_per_day_usd: 5 },
  tracker: { kind: "gitlab", gitlab: { host: "https://gitlab.example", repo: "o/r", token: "***" } },
  agent: { max_concurrent: 2, max_attempts: -1, running_state: "none" },
  polling: { interval_ms: 30000 },
};

beforeEach(() => {
  api.getConfig.mockReset();
  api.saveConfig.mockReset();
  api.saveConfig.mockResolvedValue(saved);
});
afterEach(cleanup);

async function openAndSave(): Promise<DispatcherConfig> {
  render(<SettingsDrawer open onClose={() => {}} onSaved={() => {}} />);
  await screen.findByText("Routing");
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(api.saveConfig).toHaveBeenCalledTimes(1));
  return api.saveConfig.mock.calls[0]![0] as DispatcherConfig;
}

describe("SettingsDrawer saves what it shows and what it does not show", () => {
  it("keeps require_assignee, bots.paths, assignee_dispatch and the fields without a control", async () => {
    api.getConfig.mockResolvedValue(structuredClone(saved));
    const sent = await openAndSave();
    expect(sent).toEqual(saved);
  });

  it("shows require_assignee as a checkbox and saves the toggle", async () => {
    api.getConfig.mockResolvedValue(structuredClone(saved));
    render(<SettingsDrawer open onClose={() => {}} onSaved={() => {}} />);
    const box = (await screen.findByRole("checkbox", {
      name: /only run cards assigned to a project bot/i,
    })) as HTMLInputElement;
    expect(box.checked).toBe(true);
    fireEvent.click(box);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.saveConfig).toHaveBeenCalledTimes(1));
    expect((api.saveConfig.mock.calls[0]![0] as DispatcherConfig).require_assignee).toBe(false);
  });

  it("lists the bot folders and drops an emptied row on save", async () => {
    api.getConfig.mockResolvedValue(structuredClone(saved));
    render(<SettingsDrawer open onClose={() => {}} onSaved={() => {}} />);
    const first = (await screen.findByLabelText("Bot folder 1")) as HTMLInputElement;
    expect(first.value).toBe("/proj/bots");
    expect((screen.getByLabelText("Bot folder 2") as HTMLInputElement).value).toBe("/proj/examples");
    fireEvent.change(first, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.saveConfig).toHaveBeenCalledTimes(1));
    expect((api.saveConfig.mock.calls[0]![0] as DispatcherConfig).bots?.paths).toEqual(["/proj/examples"]);
  });

  it("shows the per-bot vars row", async () => {
    api.getConfig.mockResolvedValue(structuredClone(saved));
    render(<SettingsDrawer open onClose={() => {}} onSaved={() => {}} />);
    const name = (await screen.findByLabelText("Bot name for vars row 1")) as HTMLInputElement;
    expect(name.value).toBe("feature-dev");
  });

  it("warns when a dispatch var name is a template, and still saves it", async () => {
    const swapped: DispatcherConfig = {
      ...structuredClone(saved),
      dispatch: { vars: { "{{issue.title}}": "" } },
    };
    api.getConfig.mockResolvedValue(structuredClone(swapped));
    render(<SettingsDrawer open onClose={() => {}} onSaved={() => {}} />);
    const name = (await screen.findByLabelText("Dispatch variable name")) as HTMLInputElement;
    expect(name.value).toBe("{{issue.title}}");
    expect(screen.getByRole("alert").textContent).toMatch(/looks like a template/i);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.saveConfig).toHaveBeenCalledTimes(1));
    expect((api.saveConfig.mock.calls[0]![0] as DispatcherConfig).dispatch?.vars).toEqual({
      "{{issue.title}}": "",
    });
  });

  it("does not warn about a valid dispatch var name", async () => {
    api.getConfig.mockResolvedValue({
      ...structuredClone(saved),
      dispatch: { vars: { task: "{{issue.title}}" } },
    });
    render(<SettingsDrawer open onClose={() => {}} onSaved={() => {}} />);
    await screen.findByLabelText("Dispatch variable name");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("starts a new dispatcher with require_assignee on", async () => {
    api.getConfig.mockResolvedValue(null);
    const sent = await openAndSave();
    expect(sent.require_assignee).toBe(true);
    expect(sent.workflow).toBe("");
  });
});
