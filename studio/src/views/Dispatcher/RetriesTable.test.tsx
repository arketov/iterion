// @vitest-environment jsdom
//
// The retry queue's row actions: "Retry now" and "Remove from queue" act on
// their own row and never fall through to the row click (which focuses the
// card on the board); a failed hook shows the shell it ran under.
import type { ComponentProps } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { RetryView } from "@/api/dispatcher";
import { setupMatchMedia } from "@/__tests__/a11y/axeHelpers";

import RetriesTable from "./RetriesTable";

setupMatchMedia();

afterEach(() => cleanup());

const ROWS: RetryView[] = [
  {
    issue_id: "i-1",
    identifier: "T-1",
    attempt: 2,
    due_at: "2099-01-01T00:00:00Z",
    error: "after_create hook: hook after_create: exit status 128",
    hook: "after_create",
    hook_exit_code: 128,
    hook_shell: "pwsh",
    hook_shell_path: "C:/Program Files/PowerShell/7/pwsh.exe",
    hook_shell_source: "default",
  },
  {
    issue_id: "i-2",
    identifier: "T-2",
    attempt: 1,
    due_at: "2099-01-01T00:00:00Z",
    error: "boom",
  },
];

function mount(over: Partial<ComponentProps<typeof RetriesTable>> = {}) {
  const props = {
    rows: ROWS,
    canPollDispatches: true,
    pollTitle: "Poll now",
    onFocusIssue: vi.fn(),
    onRefreshNow: vi.fn(),
    onRetryNow: vi.fn(),
    onDropRetry: vi.fn(),
    canActOnRetries: true,
    ...over,
  };
  render(
    <main>
      <RetriesTable {...props} />
    </main>,
  );
  return props;
}

describe("RetriesTable row actions", () => {
  it("runs a retry now, naming the row, without focusing the card", () => {
    const props = mount();
    fireEvent.click(screen.getByRole("button", { name: "Retry now T-2" }));
    expect(props.onRetryNow).toHaveBeenCalledWith("i-2");
    expect(props.onFocusIssue).not.toHaveBeenCalled();
  });

  it("removes a retry from the queue, naming the row, without focusing the card", () => {
    const props = mount();
    fireEvent.click(screen.getByRole("button", { name: "Remove from queue T-1" }));
    expect(props.onDropRetry).toHaveBeenCalledWith("i-1");
    expect(props.onFocusIssue).not.toHaveBeenCalled();
  });

  it("keeps the visible word first so the accessible name contains the label", () => {
    mount();
    expect(screen.getByRole("button", { name: "Retry now T-1" }).textContent).toBe("Retry now");
    expect(screen.getByRole("button", { name: "Remove from queue T-1" }).textContent).toBe(
      "Remove from queue",
    );
  });

  it("disables both actions while no dispatcher is attached", () => {
    const props = mount({ canActOnRetries: false });
    const retry = screen.getByRole("button", { name: "Retry now T-1" }) as HTMLButtonElement;
    const drop = screen.getByRole("button", { name: "Remove from queue T-1" }) as HTMLButtonElement;
    expect(retry.disabled).toBe(true);
    expect(drop.disabled).toBe(true);
    fireEvent.click(retry);
    expect(props.onRetryNow).not.toHaveBeenCalled();
  });
});

describe("RetriesTable hook shell", () => {
  it("shows the shell a failed hook ran under, only on the row that has one", () => {
    mount();
    const lines = screen.getAllByTestId("retry-shell");
    expect(lines).toHaveLength(1);
    expect(lines[0]!.textContent).toContain("pwsh");
    expect(lines[0]!.textContent).toContain("platform default");
  });
});
