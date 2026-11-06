import { describe, expect, it } from "vitest";

import {
  SOURCE_KIND_ORDER,
  SOURCE_META,
  metaForSource,
  normalizeSourceKind,
  runSourceKind,
} from "./runSourceMeta";

describe("normalizeSourceKind", () => {
  it("passes every known kind through", () => {
    for (const kind of SOURCE_KIND_ORDER) {
      expect(normalizeSourceKind(kind)).toBe(kind);
    }
  });

  // Regression: cron-fired runs were rendered "Manual" because the studio
  // didn't know the backend's "schedule" kind and normalised it away.
  it("keeps schedule distinct from manual", () => {
    expect(normalizeSourceKind("schedule")).toBe("schedule");
    expect(runSourceKind({ source_kind: "schedule" })).toBe("schedule");
    expect(runSourceKind({ source_kind: "studio_chat" })).toBe("studio_chat");
    expect(metaForSource("schedule").label).toBe("Schedule");
  });

  it("keeps Studio chat distinct from manual launches", () => {
    expect(metaForSource("studio_chat").label).toBe("Assistant");
    expect(normalizeSourceKind("studio_chat")).toBe("studio_chat");
  });

  // Regression: board-launched runs carry an empty kind and rendered "Manual".
  it("keeps pipeline board launches distinct from manual", () => {
    expect(normalizeSourceKind("pipeline_board")).toBe("pipeline_board");
    expect(runSourceKind({ source_kind: "pipeline_board" })).toBe("pipeline_board");
    expect(metaForSource("pipeline_board").label).toBe("Pipeline board");
  });

  it("falls back to manual for empty and unknown values", () => {
    expect(normalizeSourceKind(undefined)).toBe("manual");
    expect(normalizeSourceKind("")).toBe("manual");
    expect(normalizeSourceKind("from-the-future")).toBe("manual");
  });
});

describe("SOURCE_META", () => {
  it("covers every ordered kind", () => {
    for (const kind of SOURCE_KIND_ORDER) {
      expect(SOURCE_META[kind]).toBeDefined();
    }
    expect(Object.keys(SOURCE_META).sort()).toEqual(
      [...SOURCE_KIND_ORDER].sort(),
    );
  });
});
