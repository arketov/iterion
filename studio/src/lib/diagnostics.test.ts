import { describe, expect, it } from "vitest";

import { groupDiagnostics } from "./diagnostics";

describe("groupDiagnostics structured issues", () => {
  it("copies the position fields of an issue", () => {
    const g = groupDiagnostics([], [], null, [
      {
        severity: "error",
        message: "m",
        code: "C102",
        file: "x.bot",
        line: 6,
        column: 3,
        end_line: 6,
        end_column: 20,
        property: "compress",
      },
    ]);
    expect(g.all[0]).toMatchObject({
      file: "x.bot",
      line: 6,
      column: 3,
      end_line: 6,
      end_column: 20,
      property: "compress",
    });
  });

  it("leaves the position undefined when the issue has none", () => {
    const g = groupDiagnostics([], [], null, [{ severity: "warning", message: "m", code: "C337" }]);
    const d = g.all[0]!;
    expect(d.file).toBeUndefined();
    expect(d.line).toBeUndefined();
    expect(d.column).toBeUndefined();
    expect(d.end_line).toBeUndefined();
    expect(d.end_column).toBeUndefined();
    expect(d.property).toBeUndefined();
  });
});
