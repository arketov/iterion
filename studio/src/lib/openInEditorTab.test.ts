// @vitest-environment jsdom
//
// The one place that opens files, examples and imported copies into editor
// tabs. What matters: a file is read BEFORE its tab exists (so a file gone from
// disk opens no error tab and leaves the recent list), a file already open is
// focused rather than re-read over its edits, and an import keeps the file's
// own name for Save As.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as client from "@/api/client";
import { createEmptyDocument } from "@/lib/defaults";
import { getOrCreateDocumentStore } from "@/store/document";
import { useRecentsStore } from "@/store/recents";
import { useTabsStore } from "@/store/tabs";

import {
  editorFileHref,
  importFileInNewTab,
  importFileIntoStore,
  isMissingFileError,
  openFileInNewTab,
} from "./openInEditorTab";
import { ReplaceDeadlineError } from "./replaceDocument";

vi.mock("@/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/client")>();
  return { ...actual, openFile: vi.fn(), parseSource: vi.fn() };
});

type Opened = Awaited<ReturnType<typeof client.openFile>>;
const opened = (path: string, source = "workflow w:\n"): Opened =>
  ({ path, source, document: createEmptyDocument(), diagnostics: [] }) as unknown as Opened;

function deps() {
  return { navigate: vi.fn(), addToast: vi.fn() };
}

beforeEach(() => {
  useTabsStore.setState({ tabs: [], activeEditorTabId: null, currentProjectKey: null });
  useRecentsStore.setState({ recents: [] });
});
afterEach(() => {
  vi.resetAllMocks();
});

describe("isMissingFileError", () => {
  it.each([
    [new Error("file not found: a.bot"), true],
    [new Error("open /x/a.bot: no such file or directory"), true],
    [new Error("HTTP 404"), true],
    [new Error("connection reset"), false],
    [new Error("permission denied"), false],
    [new ReplaceDeadlineError("a-404.bot"), false],
  ])("%s → %s", (err, expected) => {
    expect(isMissingFileError(err)).toBe(expected);
  });
});

describe("small helpers", () => {
  it("builds the editor deep link with the path encoded", () => {
    expect(editorFileHref("bots/a b.bot")).toBe("/editor?file=bots%2Fa%20b.bot");
  });
});

describe("openFileInNewTab", () => {
  it("opens a tab bound to the file, already holding its reading, and routes to it", async () => {
    vi.mocked(client.openFile).mockResolvedValueOnce(opened("dev-flow.bot"));
    const d = deps();

    expect(await openFileInNewTab("dev-flow.bot", d)).toBe("opened");

    const tab = useTabsStore.getState().tabs.find((t) => t.params.file === "dev-flow.bot");
    expect(tab).toBeTruthy();
    const doc = getOrCreateDocumentStore(tab!.id).getState();
    expect(doc.currentFilePath).toBe("dev-flow.bot");
    expect(doc.isDirty()).toBe(false);
    expect(client.openFile).toHaveBeenCalledTimes(1);
    expect(d.navigate).toHaveBeenCalledWith("/editor?file=dev-flow.bot");
    expect(useRecentsStore.getState().recents).toEqual(["dev-flow.bot"]);
  });

  it("a file gone from disk: no tab, removed from recents, said once", async () => {
    useRecentsStore.setState({ recents: ["gone.bot", "kept.bot"] });
    vi.mocked(client.openFile).mockRejectedValueOnce(new Error("file not found: gone.bot"));
    const d = deps();

    expect(await openFileInNewTab("gone.bot", d)).toBe("missing");

    expect(useTabsStore.getState().tabs).toHaveLength(0);
    expect(useRecentsStore.getState().recents).toEqual(["kept.bot"]);
    expect(d.addToast).toHaveBeenCalledWith("Removed missing file from recents: gone.bot", "warning");
    expect(d.navigate).not.toHaveBeenCalled();
  });

  it("any other failure keeps the recent entry and opens nothing", async () => {
    useRecentsStore.setState({ recents: ["flaky.bot"] });
    vi.mocked(client.openFile).mockRejectedValueOnce(new Error("connection reset"));
    const d = deps();

    expect(await openFileInNewTab("flaky.bot", d)).toBe("failed");

    expect(useTabsStore.getState().tabs).toHaveLength(0);
    expect(useRecentsStore.getState().recents).toEqual(["flaky.bot"]);
    expect(d.addToast).toHaveBeenCalledWith(expect.stringContaining("Open failed"), "error", undefined);
    expect(d.navigate).not.toHaveBeenCalled();
  });

  it("a file already open is focused without a request, and its edits stay", async () => {
    vi.mocked(client.openFile).mockResolvedValueOnce(opened("a.bot"));
    await openFileInNewTab("a.bot", deps());
    const first = useTabsStore.getState().tabs.find((t) => t.params.file === "a.bot")!;
    // Another tab takes the focus.
    useTabsStore.getState().newEditorTab();
    expect(useTabsStore.getState().activeEditorTabId).not.toBe(first.id);
    vi.mocked(client.openFile).mockClear();
    const d = deps();

    expect(await openFileInNewTab("a.bot", d)).toBe("focused");

    expect(client.openFile).not.toHaveBeenCalled();
    expect(useTabsStore.getState().activeEditorTabId).toBe(first.id);
    expect(useTabsStore.getState().tabs.filter((t) => t.params.file === "a.bot")).toHaveLength(1);
    expect(d.navigate).toHaveBeenCalledWith("/editor?file=a.bot");
  });

  it("focuses a draft tab that was saved under the file's name instead of opening a second one", async () => {
    // A draft saved with Save As carries both `draft` and `file`.
    useTabsStore.setState({
      tabs: [{ id: "t1", kind: "editor", params: { draft: "r1", file: "d.bot" }, label: "d.bot", hydrated: true }],
      activeEditorTabId: null,
    });
    const d = deps();

    expect(await openFileInNewTab("d.bot", d)).toBe("focused");

    expect(useTabsStore.getState().tabs).toHaveLength(1);
    expect(useTabsStore.getState().activeEditorTabId).toBe("t1");
    expect(client.openFile).not.toHaveBeenCalled();
  });

  it("a tab already on the name the server answers with is focused, not overwritten", async () => {
    // The picker said "./a.bot", the server names the file "a.bot".
    vi.mocked(client.openFile).mockResolvedValueOnce(opened("a.bot", "first reading"));
    await openFileInNewTab("a.bot", deps());
    const tab = useTabsStore.getState().tabs.find((t) => t.params.file === "a.bot")!;
    getOrCreateDocumentStore(tab.id).getState().setCurrentSource("edited by the author");
    vi.mocked(client.openFile).mockResolvedValueOnce(opened("a.bot", "second reading"));

    expect(await openFileInNewTab("./a.bot", deps())).toBe("focused");

    expect(getOrCreateDocumentStore(tab.id).getState().currentSource).toBe("edited by the author");
    expect(useTabsStore.getState().tabs.filter((t) => t.kind === "editor")).toHaveLength(1);
  });
});

describe("importing a copy from disk", () => {
  const imported = {
    document: createEmptyDocument(),
    diagnostics: [],
  } as unknown as Awaited<ReturnType<typeof client.parseSource>>;

  function botFile(name: string, text = "workflow w:\n") {
    const file = new File([text], name, { type: "text/plain" });
    Object.defineProperty(file, "text", { value: async () => text });
    return file;
  }

  it("importFileIntoStore binds no path, keeps the text and the file's own name", async () => {
    vi.mocked(client.parseSource).mockResolvedValueOnce(imported);
    const store = getOrCreateDocumentStore("import-target");

    expect(await importFileIntoStore(store, botFile("dev-flow.bot"))).toBe("applied");

    const s = store.getState();
    expect(s.currentFilePath).toBeNull();
    expect(s.currentSource).toBe("workflow w:\n");
    expect(s.importedFileName).toBe("dev-flow.bot");
  });

  it("importFileInNewTab names the new tab after the file and routes to the editor", async () => {
    vi.mocked(client.parseSource).mockResolvedValueOnce(imported);
    const d = deps();

    await importFileInNewTab(botFile("dev-flow.bot"), d);

    const tab = useTabsStore.getState().tabs.find((t) => t.label === "dev-flow.bot");
    expect(tab).toBeTruthy();
    expect(getOrCreateDocumentStore(tab!.id).getState().importedFileName).toBe("dev-flow.bot");
    expect(d.navigate).toHaveBeenCalledWith("/editor");
  });

  it("a failed import drops the untouched tab and says why", async () => {
    vi.mocked(client.parseSource).mockRejectedValueOnce(new Error("boom"));
    const d = deps();

    await importFileInNewTab(botFile("bad.bot"), d);

    expect(useTabsStore.getState().tabs).toHaveLength(0);
    expect(d.addToast).toHaveBeenCalledWith(expect.stringContaining("Open failed: boom"), "error", undefined);
    expect(d.navigate).not.toHaveBeenCalled();
  });
});
