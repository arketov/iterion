import { describe, expect, it } from "vitest";

import { EVENT_BADGE, previewData } from "./eventModel";

describe("previewData", () => {
  it("distinguishes a proactive cooldown skip from a fresh fallback", () => {
    const preview = previewData({
      reason: "usage_window",
      cooldown: true,
      cooldown_until: "2026-08-24T12:20:00Z",
      attempts: 0,
    });

    expect(preview).toContain("cooldown=true");
    expect(preview).toContain("cooldown_until=2026-08-24T12:20:00Z");
    expect(preview).toContain("attempts=0");
  });
});

describe("previewData attempt", () => {
  it("shows an attempt object as a short id and start time, not [object Object]", () => {
    const preview = previewData({
      attempt: { id: "01a1b07e-8568-7475-ad86-aa13ef58de7a", started_at: "2026-11-06T10:00:00Z" },
    });
    expect(preview).not.toContain("[object Object]");
    expect(preview).toBe("attempt=01a1b07e @2026-11-06T10:00:00Z");
  });

  it("keeps a scalar attempt as a number", () => {
    expect(previewData({ attempt: 2 })).toBe("attempt=2");
  });

  it("renders an attempt object without id as compact JSON", () => {
    const preview = previewData({ attempt: { foo: "bar" } });
    expect(preview).toBe('attempt={"foo":"bar"}');
  });
});

it("shows a schema mismatch as a warning with field names", () => {
  expect(EVENT_BADGE.node_output_schema_mismatch).toBe("bg-warning-soft text-warning-fg");
  const preview = previewData({ schema: "verdict", fields: ["ok", "reason"] });
  expect(preview).toContain("schema=verdict");
  expect(preview).toContain("fields=ok,reason");
});
