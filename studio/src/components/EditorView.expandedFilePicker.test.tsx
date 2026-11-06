// @vitest-environment jsdom
//
// "Open file…" raises the global `filePickerOpen` flag; the picker itself is
// drawn by the Toolbar, which the expanded layout does not mount. The command
// did nothing there and the flag stayed up to surface later. The expanded
// editor must show the picker and take a pick into its own tab.
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  openFile: vi.fn(),
  listFiles: vi.fn(),
  listFilesPage: vi.fn(async () => ({ files: [] })),
  listExampleEntries: vi.fn(),
  parseBotSourceEditorPath: () => null,
  inferCatalogBotId: () => null,
  botSourceEditorPath: () => "",
}));
vi.mock("@/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/client")>()),
  ...api,
}));
vi.mock("wouter", () => ({
  useLocation: () => ["/editor", vi.fn()],
  useSearch: () => "",
  Link: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
}));
vi.mock("@/auth/AuthContext", () => ({ useAuth: () => ({ activeTeamID: "t" }), useMaybeAuth: () => null }));
vi.mock("@/components/Canvas/Canvas", () => ({ default: () => <div /> }));
vi.mock("@/components/Inspector/Inspector", () => ({ default: () => <div /> }));
vi.mock("@/components/Diagnostics/DiagnosticsPanel", () => ({ default: () => <div /> }));
vi.mock("@/components/Library/LibraryPanel", () => ({ default: () => <div /> }));
vi.mock("@/components/Canvas/SubNodePalette", () => ({ default: () => <div /> }));
vi.mock("@/components/SourceView/SourceView", () => ({ default: () => <div /> }));
vi.mock("@/hooks/useAutoValidation", () => ({ useAutoValidation: () => {} }));
vi.mock("@/hooks/useAutoOpenDiagnosticsOnError", () => ({ useAutoOpenDiagnosticsOnError: () => {} }));
vi.mock("@/hooks/useFileWatcher", () => ({ useFileWatcher: () => {} }));
vi.mock("@/lib/chatDock/pageContext", () => ({ useAssistantPageContext: () => {} }));
vi.mock("@/components/ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/components/ui")>()),
  DesktopOnlyNotice: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

import { createEmptyDocument } from "@/lib/defaults";
import { DocumentStoreProvider, getOrCreateDocumentStore } from "@/store/document";
import { SelectionStoreProvider, getOrCreateSelectionStore } from "@/store/selection";
import { useRecentsStore } from "@/store/recents";
import { useTabsStore } from "@/store/tabs";
import { useUIStore } from "@/store/ui";

import EditorView from "./EditorView";

beforeEach(() => {
  vi.resetAllMocks();
  window.localStorage.clear();
  useTabsStore.setState({ tabs: [], activeEditorTabId: null, activeRunTabId: null });
  useUIStore.setState({ toasts: [], filePickerOpen: false, expanded: true });
  api.listFiles.mockResolvedValue([]);
  api.listExampleEntries.mockResolvedValue([]);
  api.listFilesPage.mockResolvedValue({ files: [] });
});
afterEach(() => {
  cleanup();
  useUIStore.setState({ filePickerOpen: false, expanded: false });
});

describe("Open file… in the expanded editor", () => {
  it("shows the file picker and opens the pick into the tab", async () => {
    useRecentsStore.setState({ recents: ["bots/r.bot"] } as never);
    api.openFile.mockImplementation(async (path: string) => ({
      source: "R\n",
      document: { ...createEmptyDocument(), comments: [{ text: "R" }] },
      diagnostics: [],
      path,
    }));
    const store = getOrCreateDocumentStore("expanded-tab");
    store.getState().setDocument(createEmptyDocument());
    store.getState().markSaved();
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <DocumentStoreProvider store={store}>
          <SelectionStoreProvider store={getOrCreateSelectionStore("expanded-tab")}>
            <EditorView active />
          </SelectionStoreProvider>
        </DocumentStoreProvider>
      </QueryClientProvider>,
    );
    // The expanded layout has no Toolbar.
    expect(screen.queryByRole("button", { name: /Keyboard shortcuts/ })).toBeNull();

    act(() => useUIStore.getState().setFilePickerOpen(true));
    await waitFor(() => expect(document.querySelectorAll('[role="dialog"]').length).toBe(1));
    const dialog = document.querySelector('[role="dialog"]') as HTMLElement;
    fireEvent.click(within(dialog).getByText("bots/r.bot"));
    await waitFor(() => expect(store.getState().currentFilePath).toBe("bots/r.bot"));
  });
});
