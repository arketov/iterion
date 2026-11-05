import { describe, expect, it } from "vitest";

import { isNavHrefAvailable } from "./navAvailability";

const info = (o: Record<string, unknown>) => o as never;

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
    expect(isNavHrefAvailable("/integrations", info({ mode: "cloud" }))).toBe(true);
    expect(isNavHrefAvailable("/editor", info({ mode: "local" }))).toBe(true);
    expect(isNavHrefAvailable("/editor", info({ mode: "cloud" }))).toBe(false);
    expect(
      isNavHrefAvailable("/editor", info({ mode: "cloud", bot_editing_enabled: true })),
    ).toBe(true);
  });
});
