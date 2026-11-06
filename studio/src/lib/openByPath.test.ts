import { describe, expect, it } from "vitest";

import { normalizeWorkflowPath } from "./openByPath";
import { refusalFor, workflowFileKind } from "./workflowFileKind";
import { filesCutNotice } from "./filesCutNotice";

describe("workflowFileKind", () => {
  it("tells a workflow, a packed bundle and anything else apart, ignoring case", () => {
    expect(workflowFileKind("a.bot")).toBe("bot");
    expect(workflowFileKind("DIR/A.BOT")).toBe("bot");
    expect(workflowFileKind("pack.botz")).toBe("archive");
    expect(workflowFileKind("PACK.BOTZ")).toBe("archive");
    expect(workflowFileKind("notes.txt")).toBe("other");
    expect(workflowFileKind("a.bot.yaml")).toBe("other");
  });

  it("refuses a .botz archive with the way to install it, a .txt plainly, a .bot never", () => {
    expect(refusalFor("a.bot")).toBeNull();
    expect(refusalFor("x.botz")).toContain("Import");
    expect(refusalFor("x.txt")).toBe("Only .bot files can be opened");
  });
});

describe("normalizeWorkflowPath", () => {
  it("keeps a project-relative path and turns Windows separators into slashes", () => {
    expect(normalizeWorkflowPath("workflows/dev-flow.bot")).toEqual({ ok: true, path: "workflows/dev-flow.bot" });
    expect(normalizeWorkflowPath("workflows\dev-flow.bot")).toEqual({ ok: true, path: "workflows/dev-flow.bot" });
    expect(normalizeWorkflowPath("./tests/a.bot")).toEqual({ ok: true, path: "tests/a.bot" });
  });

  it("strips the quotes a path is pasted with and surrounding blanks", () => {
    expect(normalizeWorkflowPath('  "docs/a.bot" ')).toEqual({ ok: true, path: "docs/a.bot" });
  });

  it("makes an absolute path inside the project folder relative", () => {
    expect(normalizeWorkflowPath("/home/me/proj/docs/a.bot", "/home/me/proj")).toEqual({
      ok: true,
      path: "docs/a.bot",
    });
  });

  it("compares a Windows drive path without regard to case", () => {
    expect(
      normalizeWorkflowPath("c:\Users\me\Proj\docs\a.bot", "C:\Users\me\proj"),
    ).toEqual({ ok: true, path: "docs/a.bot" });
  });

  it("refuses an absolute path outside the project folder", () => {
    const r = normalizeWorkflowPath("/etc/a.bot", "/home/me/proj");
    expect(r.ok).toBe(false);
    // A sibling that merely shares the folder's name as a prefix is outside too.
    expect(normalizeWorkflowPath("/home/me/proj-other/a.bot", "/home/me/proj").ok).toBe(false);
  });

  it("refuses an empty field, a non-.bot file, and a .botz archive, each with its own sentence", () => {
    expect(normalizeWorkflowPath("   ")).toMatchObject({ ok: false, error: "Type the path of a .bot file." });
    expect(normalizeWorkflowPath("notes.txt")).toMatchObject({ ok: false, error: "Only .bot files open in the editor." });
    const botz = normalizeWorkflowPath("x.botz");
    expect(botz.ok).toBe(false);
    if (!botz.ok) expect(botz.error).toContain(".botz archive");
  });
});

describe("filesCutNotice", () => {
  it("says how many are shown, of how many, and how to reach the rest", () => {
    const text = filesCutNotice(500, 612);
    expect(text).toContain("Showing the first 500 files of 612");
    expect(text).toContain("ITERION_STUDIO_FILES_LIMIT");
    expect(text).toContain("by its path");
  });

  it("omits the total when the server did not send one", () => {
    expect(filesCutNotice(3)).toContain("Showing the first 3 files.");
  });
});
