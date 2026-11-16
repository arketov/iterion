import { describe, expect, it } from "vitest";

import { isNavHrefAvailable } from "./navAvailability";

const info = (o: Record<string, unknown>) => o as never;
const team = { hasActiveTeam: true, role: "member" as const, isSuperAdmin: false };

describe("isNavHrefAvailable", () => {
  it("lists only the always-on pages while server_info is loading", () => {
    expect(isNavHrefAvailable("/runs", null)).toBe(true);
    expect(isNavHrefAvailable("/insights", null)).toBe(true);
    expect(isNavHrefAvailable("/board", null)).toBe(false);
    expect(isNavHrefAvailable("/integrations", null)).toBe(false);
  });

  it("gates pages on the flags the sidebar gates on", () => {
    expect(isNavHrefAvailable("/board", info({ mode: "local" }))).toBe(false);
    expect(
      isNavHrefAvailable("/board", info({ mode: "local", native_tracker_enabled: true })),
    ).toBe(true);
    expect(
      isNavHrefAvailable("/dispatcher", info({ dispatcher_enabled: true })),
    ).toBe(true);
    expect(isNavHrefAvailable("/triggers", info({ mode: "local" }))).toBe(false);
    expect(
      isNavHrefAvailable("/triggers", info({ mode: "local", triggers_enabled: true })),
    ).toBe(true);
    // Cloud carries Schedules even with the trigger spine off.
    expect(isNavHrefAvailable("/triggers", info({ mode: "cloud" }))).toBe(true);
  });

  it("keeps Integrations cloud-only and the editor behind the bot store in cloud", () => {
    expect(isNavHrefAvailable("/integrations", info({ mode: "local" }))).toBe(false);
    expect(
      isNavHrefAvailable("/integrations", info({ mode: "cloud" }), { ...team }),
    ).toBe(true);
    expect(isNavHrefAvailable("/editor", info({ mode: "local" }))).toBe(true);
    expect(isNavHrefAvailable("/editor", info({ mode: "cloud" }))).toBe(false);
    expect(
      isNavHrefAvailable("/editor", info({ mode: "cloud", bot_editing_enabled: true })),
    ).toBe(true);
  });

  it("shows Integrations only with an active team", () => {
    expect(isNavHrefAvailable("/integrations", info({ mode: "cloud" }))).toBe(false);
    expect(
      isNavHrefAvailable("/integrations", info({ mode: "cloud" }), {
        ...team,
        hasActiveTeam: false,
      }),
    ).toBe(false);
  });

  it("shows the Config editor only to a role that can edit config shares", () => {
    const on = info({ mode: "cloud", config_shares_enabled: true });
    expect(isNavHrefAvailable("/config-editor", on)).toBe(false);
    expect(isNavHrefAvailable("/config-editor", on, { ...team, role: "member" })).toBe(false);
    expect(isNavHrefAvailable("/config-editor", on, { ...team, role: "viewer" })).toBe(false);
    expect(isNavHrefAvailable("/config-editor", on, { ...team, role: "admin" })).toBe(true);
    expect(isNavHrefAvailable("/config-editor", on, { ...team, role: "config_editor" })).toBe(true);
    expect(
      isNavHrefAvailable("/config-editor", on, { hasActiveTeam: false, role: null, isSuperAdmin: true }),
    ).toBe(true);
    // The flag still gates it for a role that could edit.
    expect(
      isNavHrefAvailable("/config-editor", info({ mode: "cloud" }), { ...team, role: "admin" }),
    ).toBe(false);
  });
});
